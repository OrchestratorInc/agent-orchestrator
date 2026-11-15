package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

var releaseRepo = "Untrivial-ai/agent-orchestrator"

const appBundleName = "Agent Orchestrator.app"

const appStateFileName = "app-state.json"

type appState struct {
	SchemaVersion    int    `json:"schemaVersion"`
	AppPath          string `json:"appPath"`
	Version          string `json:"version"`
	InstalledAt      string `json:"installedAt"`
	LastReconciledAt string `json:"lastReconciledAt"`
	InstallSource    string `json:"installSource"`
}

type startOptions struct {
	json bool
}

type startResult struct {
	Resolved bool   `json:"resolved"`
	Fetched  bool   `json:"fetched"`
	Opened   bool   `json:"opened"`
	AppPath  string `json:"appPath"`
}

func newStartCommand(ctx *commandContext) *cobra.Command {
	opts := startOptions{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Fetch (if needed) and open the Agent Orchestrator desktop app",
		Long: "Fetch (if needed) and open the Agent Orchestrator desktop app.\n\n" +
			"The desktop app now owns the daemon, state, and updates. `ao start` no\n" +
			"longer runs a daemon: it resolves the installed app (or downloads the\n" +
			"latest release), opens it, and exits.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runStart(cmd.Context(), cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output start result as JSON")
	return cmd
}

func (c *commandContext) runStart(ctx context.Context, cmd *cobra.Command, opts startOptions) error {
	out := cmd.OutOrStdout()
	res := startResult{}

	appPath := c.resolveApp()
	res.Resolved = appPath != ""

	var err error
	if appPath == "" {
		appPath, err = c.fetchApp(ctx, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		res.Fetched = true
	}
	res.AppPath = appPath

	if runtime.GOOS == "linux" {
		if err := c.registerLinuxProtocolHandler(ctx, appPath); err != nil {
			return err
		}
	}

	opened, err := c.openApp(ctx, appPath)
	if err != nil {
		return err
	}
	res.Opened = opened

	if opts.json {
		return writeJSON(out, res)
	}

	c.printDeprecationNotice(out)
	if !opened {
		c.printManualOpen(out, appPath)
	}
	return nil
}

func (c *commandContext) resolveApp() string {
	if p := preferredAppPath(); p != "" && isUsableBundle(p) {
		return p
	}
	if p := c.markerAppPath(); p != "" && isUsableBundle(p) {
		return p
	}
	for _, p := range appScanLocations() {
		if isUsableBundle(p) {
			return p
		}
	}
	return ""
}

var preferredAppPath = darwinApplicationsBundle

func darwinApplicationsBundle() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return "/Applications/" + appBundleName
}

var appScanLocations = knownAppLocations

func (c *commandContext) markerAppPath() string {
	dir, err := aoStateDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, appStateFileName))
	if err != nil {
		return ""
	}
	var st appState
	if err := json.Unmarshal(data, &st); err != nil {
		return ""
	}
	return st.AppPath
}

func aoStateDir() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	return filepath.Dir(cfg.RunFilePath), nil
}

func knownAppLocations() []string {
	switch runtime.GOOS {
	case "darwin":
		paths := []string{"/Applications/" + appBundleName}
		if home, err := os.UserHomeDir(); err == nil {
			paths = append(paths, filepath.Join(home, "Applications", appBundleName))
		}
		return paths
	case "windows":
		var paths []string
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			paths = append(paths, windowsInstalledExe(local))
		}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			paths = append(paths, filepath.Join(pf, "Agent Orchestrator", "agent-orchestrator.exe"))
		}
		return paths
	case "linux":
		paths := []string{linuxAppImagePath()}
		if home, err := os.UserHomeDir(); err == nil {
			paths = append(paths, filepath.Join(home, "Applications", "agent-orchestrator.AppImage"))
		}
		return paths
	default:
		return nil
	}
}

func windowsInstalledExe(localAppData string) string {
	return filepath.Join(localAppData, "Programs", "Agent Orchestrator", "agent-orchestrator.exe")
}

func linuxAppImagePath() string {
	dir, err := aoStateDir()
	if err != nil {
		return "agent-orchestrator.AppImage"
	}
	return filepath.Join(dir, "agent-orchestrator.AppImage")
}

const linuxDesktopEntryName = "agent-orchestrator-ao-app.desktop"

func linuxApplicationsDir() (string, error) {
	if dataHome := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dataHome) {
		return filepath.Join(dataHome, "applications"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for Linux protocol registration: %w", err)
	}
	return filepath.Join(home, ".local", "share", "applications"), nil
}

func desktopExecPath(appPath string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		"$", `\$`,
		"%", "%%",
	).Replace(appPath)
	return `"` + escaped + `"`
}

func (c *commandContext) registerLinuxProtocolHandler(
	ctx context.Context,
	appPath string,
) error {
	applicationsDir, err := linuxApplicationsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(applicationsDir, 0o750); err != nil {
		return fmt.Errorf("create Linux applications directory: %w", err)
	}
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Orchestrator
Exec=%s %%u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/ao-app;
`, desktopExecPath(appPath))
	target := filepath.Join(applicationsDir, linuxDesktopEntryName)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, []byte(entry), 0o600); err != nil {
		return fmt.Errorf("write Linux desktop entry: %w", err)
	}
	if err := os.Chmod(temporary, 0o644); err != nil { //nolint:gosec // G302: desktop entries must be world-readable
		_ = os.Remove(temporary)
		return fmt.Errorf("chmod Linux desktop entry: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("install Linux desktop entry: %w", err)
	}
	if out, err := c.deps.CommandOutput(
		ctx,
		"xdg-mime",
		"default",
		linuxDesktopEntryName,
		"x-scheme-handler/ao-app",
	); err != nil {
		return fmt.Errorf(
			"register ao-app URL handler with xdg-mime (install xdg-utils or use the .deb/.rpm package): %w: %s",
			err,
			out,
		)
	}
	out, err := c.deps.CommandOutput(
		ctx,
		"xdg-mime",
		"query",
		"default",
		"x-scheme-handler/ao-app",
	)
	if err != nil {
		return fmt.Errorf("verify ao-app URL handler: %w: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != linuxDesktopEntryName {
		return fmt.Errorf(
			"verify ao-app URL handler: xdg-mime selected %q instead of %q",
			strings.TrimSpace(string(out)),
			linuxDesktopEntryName,
		)
	}
	return nil
}

func isUsableBundle(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	if runtime.GOOS == "darwin" {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}

func (c *commandContext) fetchApp(ctx context.Context, w io.Writer) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return c.fetchAppDarwin(ctx, w)
	case "windows":
		return c.fetchAppWindows(ctx, w)
	case "linux":
		return c.fetchAppLinux(ctx, w)
	default:
		return "", fmt.Errorf("ao start: fetch not supported on %s", runtime.GOOS)
	}
}

func (c *commandContext) fetchAppDarwin(ctx context.Context, w io.Writer) (string, error) {
	asset, err := assetName()
	if err != nil {
		return "", err
	}
	url := downloadURL(asset)

	stateDir, err := aoStateDir()
	if err != nil {
		return "", err
	}
	staging := filepath.Join(stateDir, "staging")
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clear staging dir: %w", err)
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	zipPath := filepath.Join(staging, asset)
	if err := c.download(ctx, w, url, asset, zipPath); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}

	_, _ = fmt.Fprintln(w, "Unpacking...")
	if out, err := c.deps.CommandOutput(ctx, "ditto", "-x", "-k", zipPath, staging); err != nil {
		return "", fmt.Errorf("ditto unpack: %w: %s", err, out)
	}

	appPath := filepath.Join(staging, appBundleName)
	if !isUsableBundle(appPath) {
		return "", fmt.Errorf("ao start: %s not found in unpacked release at %s", appBundleName, staging)
	}
	return appPath, nil
}

func (c *commandContext) fetchAppWindows(ctx context.Context, w io.Writer) (string, error) {
	asset, err := assetName()
	if err != nil {
		return "", err
	}
	url := downloadURL(asset)

	stateDir, err := aoStateDir()
	if err != nil {
		return "", err
	}
	staging := filepath.Join(stateDir, "staging")
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clear staging dir: %w", err)
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}

	installerPath := filepath.Join(staging, asset)
	if err := c.download(ctx, w, url, asset, installerPath); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}

	_, _ = fmt.Fprintln(w, "Installing...")
	if out, err := c.deps.CommandOutput(ctx, installerPath, "/S"); err != nil {
		return "", fmt.Errorf("silent install: %w: %s", err, out)
	}

	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", fmt.Errorf("ao start: LOCALAPPDATA not set; cannot locate installed app")
	}
	appPath := windowsInstalledExe(local)
	if !isUsableBundle(appPath) {
		return "", fmt.Errorf("ao start: installed app not found at %s", appPath)
	}
	return appPath, nil
}

func (c *commandContext) fetchAppLinux(ctx context.Context, w io.Writer) (string, error) {
	asset, err := assetName()
	if err != nil {
		return "", err
	}
	url := downloadURL(asset)

	appPath := linuxAppImagePath()
	if err := os.MkdirAll(filepath.Dir(appPath), 0o750); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	tmpPath := appPath + ".part"
	if err := c.download(ctx, w, url, asset, tmpPath); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	_, _ = fmt.Fprintln(w, "Installing...")
	if err := os.Chmod(tmpPath, 0o755); err != nil { //nolint:gosec // G302: AppImage must be executable
		return "", fmt.Errorf("chmod AppImage: %w", err)
	}
	if err := os.Rename(tmpPath, appPath); err != nil {
		return "", fmt.Errorf("install AppImage: %w", err)
	}
	if !isUsableBundle(appPath) {
		return "", fmt.Errorf("ao start: AppImage not found at %s", appPath)
	}
	return appPath, nil
}

func (c *commandContext) download(ctx context.Context, w io.Writer, url, asset, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	client := *c.deps.HTTPClient
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	total := resp.ContentLength
	if total > 0 {
		_, _ = fmt.Fprintf(w, "Downloading Agent Orchestrator (%s, ~%s) from %s...\n", asset, humanBytes(total), releaseRepo)
	} else {
		_, _ = fmt.Fprintf(w, "Downloading Agent Orchestrator (%s) from %s...\n", asset, releaseRepo)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	pw := &progressWriter{w: w, total: total, tty: writerIsInteractive(w)}
	if _, err := io.Copy(f, io.TeeReader(resp.Body, pw)); err != nil {
		return err
	}
	pw.done()
	return f.Close()
}

type progressWriter struct {
	w       io.Writer
	total   int64
	tty     bool
	written int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	if p.tty {
		if p.total > 0 {
			pct := p.written * 100 / p.total
			_, _ = fmt.Fprintf(p.w, "\r  %d%% (%s / %s)", pct, humanBytes(p.written), humanBytes(p.total))
		} else {
			_, _ = fmt.Fprintf(p.w, "\r  %s", humanBytes(p.written))
		}
	}
	return n, nil
}

func (p *progressWriter) done() {
	if p.tty {
		_, _ = fmt.Fprintln(p.w)
		return
	}
	_, _ = fmt.Fprintf(p.w, "Downloaded %s.\n", humanBytes(p.written))
}

func writerIsInteractive(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func assetName() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		arch, err := assetArch(runtime.GOARCH)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("agent-orchestrator-darwin-%s.zip", arch), nil
	case "windows":
		if _, err := requireAMD64(); err != nil {
			return "", err
		}
		return "agent-orchestrator-win32-x64.exe", nil
	case "linux":
		if _, err := requireAMD64(); err != nil {
			return "", err
		}
		return "agent-orchestrator-linux-x64.AppImage", nil
	default:
		return "", fmt.Errorf("ao start: no release asset for %s", runtime.GOOS)
	}
}

func requireAMD64() (string, error) {
	if runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("ao start: unsupported architecture %q on %s (only amd64 is published)", runtime.GOARCH, runtime.GOOS)
	}
	return "x64", nil
}

func assetArch(goarch string) (string, error) {
	switch goarch {
	case "arm64":
		return "arm64", nil
	case "amd64":
		return "x64", nil
	default:
		return "", fmt.Errorf("ao start: unsupported architecture %q", goarch)
	}
}

func downloadURL(asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", releaseRepo, asset)
}

func (c *commandContext) openApp(ctx context.Context, appPath string) (bool, error) {
	switch runtime.GOOS {
	case "darwin":
		if out, err := c.deps.CommandOutput(ctx, "open", appPath, "--args", "--installed-via=npm-bootstrap"); err != nil {
			return false, fmt.Errorf("open %s: %w: %s", appPath, err, out)
		}
		return true, nil
	case "windows", "linux":
		err := c.deps.StartProcess(processStartConfig{
			Path: appPath,
			Args: []string{"--installed-via=npm-bootstrap"},
		})
		if err != nil {
			return false, nil //nolint:nilerr // launch failure is reported via the bool, not as an error
		}
		return true, nil
	default:
		return false, nil
	}
}

func (c *commandContext) printDeprecationNotice(w io.Writer) {
	_, _ = fmt.Fprint(w, "Agent Orchestrator is now a desktop app, and the npm `ao` is just its launcher.\n"+
		"The app is distributed from the website and GitHub Releases; it owns the daemon and updates itself.\n"+
		"You can keep running `ao start` to fetch (if needed) and open it.\n")
}

func (c *commandContext) printManualOpen(w io.Writer, appPath string) {
	_, _ = fmt.Fprintf(w, "Could not open the app automatically. Open it manually: %s\n", appPath)
}
