package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

const (
	serviceManagerLaunchd = "launchd"
	serviceManagerSystemd = "systemd"

	// serviceLaunchdLabel and serviceSystemdUnit name the default service. A
	// service for a non-default AO_DATA_DIR/AO_RUN_FILE gets a suffix, so a dev
	// or sandbox install never replaces the real one.
	serviceLaunchdLabel = "dev.agent-orchestrator.serve"
	serviceSystemdUnit  = "agent-orchestrator-serve"

	// serviceMarker is written into every file `ao service install` creates.
	// `uninstall` and a repeat `install` only touch a file that carries it.
	serviceMarker = "Written by `ao service install`. Remove it with `ao service uninstall`."
)

// serviceGOOS is the platform the service commands target; tests override it.
var serviceGOOS = runtime.GOOS

// serviceEnvKeys are carried from the installing shell into the service, so
// the daemon finds the same agents, state and analytics choice it would when
// started by hand. launchd and systemd start services with a minimal PATH.
var serviceEnvKeys = []string{"PATH", "AO_DATA_DIR", "AO_RUN_FILE", "AO_PORT", "DO_NOT_TRACK"}

var errServiceUnsupported = errors.New("`ao service` is not supported on this platform yet; run `ao serve` from a startup task instead")

type serviceOptions struct {
	port    int
	json    bool
	noWait  bool
	timeout time.Duration
}

// serviceSpec is everything that identifies and defines the service.
type serviceSpec struct {
	Manager string
	Name    string // launchd label, or systemd unit name without ".service"
	File    string
	Args    []string
	Env     map[string]string
}

// serviceStatus is the `ao service ... --json` output. Only add fields.
type serviceStatus struct {
	Manager   string `json:"manager"`
	Name      string `json:"name"`
	File      string `json:"file"`
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"`
	// ExistingDaemon is set by install when a daemon was already serving
	// this data dir. The service then watches it and starts its own if it
	// stops with the app or crashes.
	ExistingDaemon bool `json:"existingDaemon,omitempty"`
	// Enabled is whether a systemd unit is enabled; Loaded is whether it is
	// running (is-active).
	Enabled *bool `json:"enabled,omitempty"`
	// Linger is whether systemd keeps this user's services running while the
	// user is logged out (systemd only, when loginctl answers). Without it the
	// service starts at login and stops at logout; LingerFix is the command
	// that turns it on.
	// SelfHostedService is the service file scripts/setup-self-hosted.sh
	// installed on this machine, when there is one.
	SelfHostedService string        `json:"selfHostedService,omitempty"`
	Linger            *bool         `json:"linger,omitempty"`
	LingerFix         string        `json:"lingerFix,omitempty"`
	Daemon            *daemonStatus `json:"daemon,omitempty"`
}

func newServiceCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Keep `ao serve` running across reboots",
		Long: "Keep the Orchestrator daemon running across logins and reboots, with no sudo.\n\n" +
			"macOS: a launchd user agent in ~/Library/LaunchAgents.\n" +
			"Linux: a systemd --user unit in ~/.config/systemd/user. It runs while you are logged out\n" +
			"only with lingering (`loginctl enable-linger`); install and status report it.\n" +
			"Windows: not supported yet.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newServiceInstallCommand(ctx), newServiceStatusCommand(ctx), newServiceUninstallCommand(ctx))
	return cmd
}

func newServiceInstallCommand(ctx *commandContext) *cobra.Command {
	opts := serviceOptions{timeout: defaultServeReadyTimeout}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install and start a user service that runs `ao serve`",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.port < 0 || opts.port > 65535 {
				return usageError{fmt.Errorf("invalid --port %d: out of range 1-65535", opts.port)}
			}
			if opts.timeout <= 0 {
				return usageError{fmt.Errorf("invalid --timeout %s: must be positive", opts.timeout)}
			}
			if err := applyPortFlag(opts.port); err != nil {
				return err
			}
			st, err := ctx.serviceInstall(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return writeServiceStatus(cmd.OutOrStdout(), st, opts.json, "installed")
		},
	}
	cmd.Flags().IntVar(&opts.port, "port", 0, "Loopback port the service's daemon uses (default: AO_PORT, else 3001)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the result as JSON")
	cmd.Flags().BoolVar(&opts.noWait, "no-wait", false, "Return once the service is loaded, without waiting for the daemon")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultServeReadyTimeout, "How long to wait for the daemon to become ready")
	return cmd
}

func newServiceStatusCommand(ctx *commandContext) *cobra.Command {
	var opts serviceOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the `ao serve` user service is installed and running",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := ctx.serviceStatus(cmd.Context())
			if err != nil {
				return err
			}
			return writeServiceStatus(cmd.OutOrStdout(), st, opts.json, "")
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output status as JSON")
	return cmd
}

func newServiceUninstallCommand(ctx *commandContext) *cobra.Command {
	var opts serviceOptions
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the `ao serve` user service",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := ctx.serviceUninstall(cmd.Context())
			if err != nil {
				return err
			}
			return writeServiceStatus(cmd.OutOrStdout(), st, opts.json, "uninstalled")
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the result as JSON")
	return cmd
}

func writeServiceStatus(w io.Writer, st serviceStatus, asJSON bool, action string) error {
	if asJSON {
		return writeJSON(w, st)
	}
	var b strings.Builder
	switch action {
	case "installed":
		fmt.Fprintf(&b, "Orchestrator service installed (%s %s)\n", st.Manager, st.Name)
	case "uninstalled":
		fmt.Fprintf(&b, "Orchestrator service uninstalled (%s %s)\n", st.Manager, st.Name)
	default:
		fmt.Fprintf(&b, "Orchestrator service (%s %s)\n", st.Manager, st.Name)
	}
	fmt.Fprintf(&b, "  file: %s\n  installed: %t\n  loaded: %t\n", st.File, st.Installed, st.Loaded)
	if st.Enabled != nil {
		fmt.Fprintf(&b, "  enabled: %t\n", *st.Enabled)
	}
	if st.SelfHostedService != "" {
		fmt.Fprintf(&b, "  self-hosted host service: %s\n", st.SelfHostedService)
	}
	if st.Linger != nil {
		fmt.Fprintf(&b, "  linger: %t\n", *st.Linger)
		if !*st.Linger {
			fmt.Fprintf(&b, "  note: without lingering the service starts when you log in and stops when you log out; to keep it running on a headless machine, run: %s\n", st.LingerFix)
		}
	}
	if st.ExistingDaemon {
		b.WriteString("  note: a daemon was already running, so the service watches it and starts its own if it crashes or quits with the app\n")
	}
	if st.Daemon != nil {
		fmt.Fprintf(&b, "  daemon: %s", st.Daemon.State)
		if st.Daemon.Port != 0 {
			fmt.Fprintf(&b, " (pid %d, http://%s:%d)", st.Daemon.PID, config.LoopbackHost, st.Daemon.Port)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// buildServiceSpec derives the service from the platform, the ao binary and
// the installing shell's environment.
func buildServiceSpec(goos, home, exe string, env map[string]string, port int) (serviceSpec, error) {
	suffix := ""
	if env["AO_DATA_DIR"] != "" || env["AO_RUN_FILE"] != "" {
		sum := sha256.Sum256([]byte(env["AO_DATA_DIR"] + "\x00" + env["AO_RUN_FILE"]))
		suffix = "-" + hex.EncodeToString(sum[:4])
	}
	args := []string{exe, "serve", "--foreground"}
	if port != 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	carried := map[string]string{}
	for _, k := range serviceEnvKeys {
		if v := env[k]; v != "" {
			carried[k] = v
		}
	}
	for k, v := range env {
		if strings.HasPrefix(k, "AO_TELEMETRY_") && v != "" {
			carried[k] = v
		}
	}
	switch goos {
	case "darwin":
		name := serviceLaunchdLabel + suffix
		return serviceSpec{Manager: serviceManagerLaunchd, Name: name, File: filepath.Join(home, "Library", "LaunchAgents", name+".plist"), Args: args, Env: carried}, nil
	case "linux":
		configHome := env["XDG_CONFIG_HOME"]
		if configHome == "" || !filepath.IsAbs(configHome) {
			configHome = filepath.Join(home, ".config")
		}
		name := serviceSystemdUnit + suffix
		return serviceSpec{Manager: serviceManagerSystemd, Name: name, File: filepath.Join(configHome, "systemd", "user", name+".service"), Args: args, Env: carried}, nil
	default:
		return serviceSpec{}, errServiceUnsupported
	}
}

func (c *commandContext) currentServiceSpec(port int) (serviceSpec, error) {
	if serviceGOOS != "darwin" && serviceGOOS != "linux" {
		return serviceSpec{}, errServiceUnsupported
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return serviceSpec{}, fmt.Errorf("resolve home dir: %w", err)
	}
	exe, err := c.deps.Executable()
	if err != nil {
		return serviceSpec{}, fmt.Errorf("resolve ao executable: %w", err)
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	env := envMap(os.Environ())
	// Store absolute state paths, since the service does not start in this
	// shell's working directory.
	_, dataDir, runFile, err := config.ResolvePaths()
	if err != nil {
		return serviceSpec{}, err
	}
	if env["AO_DATA_DIR"] != "" {
		env["AO_DATA_DIR"] = dataDir
	}
	if env["AO_RUN_FILE"] != "" {
		env["AO_RUN_FILE"] = runFile
	}
	return buildServiceSpec(serviceGOOS, home, exe, env, port)
}

// renderLaunchdPlist renders a launchd user agent. KeepAlive restarts the
// daemon only after a crash: `ao stop` exits cleanly and stays stopped.
func renderLaunchdPlist(spec serviceSpec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<!-- " + serviceMarker + " -->\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", xmlEscape(spec.Name))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range spec.Args {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("\t</array>\n")
	if len(spec.Env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, k := range sortedKeys(spec.Env) {
			fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", xmlEscape(k), xmlEscape(spec.Env[k]))
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	b.WriteString("\t<key>ThrottleInterval</key>\n\t<integer>10</integer>\n")
	// Like KillMode=process below: stopping the service must not kill session
	// processes the next daemon adopts.
	b.WriteString("\t<key>AbandonProcessGroup</key>\n\t<true/>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// renderSystemdUnit renders a systemd --user unit. Restart=on-failure keeps a
// clean `ao stop` stopped.
func renderSystemdUnit(spec serviceSpec) string {
	var b strings.Builder
	b.WriteString("# " + serviceMarker + "\n")
	b.WriteString("[Unit]\nDescription=Orchestrator daemon (ao serve)\n\n[Service]\nType=simple\n")
	quoted := make([]string, len(spec.Args))
	for i, a := range spec.Args {
		quoted[i] = systemdQuote(strings.ReplaceAll(a, "$", "$$"))
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(quoted, " "))
	for _, k := range sortedKeys(spec.Env) {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(k+"="+spec.Env[k]))
	}
	// KillMode=process, as in scripts/setup-self-hosted.sh: a cgroup kill
	// would stop sessions the next daemon must adopt.
	b.WriteString("KillMode=process\nRestart=on-failure\nRestartSec=10\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	// A raw line break would end the directive; systemd decodes C escapes in
	// quoted values.
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return `"` + s + `"`
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ownedServiceFile reports whether path exists and was written by install.
func ownedServiceFile(path string) (exists, owned bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, bytes.Contains(data, []byte(serviceMarker)), nil
}

func (c *commandContext) serviceCmd(ctx context.Context, name string, args ...string) error {
	out, err := c.deps.CommandOutput(ctx, name, args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func (c *commandContext) serviceLoaded(ctx context.Context, spec serviceSpec) bool {
	switch spec.Manager {
	case serviceManagerLaunchd:
		return c.serviceCmd(ctx, "launchctl", "print", launchdDomain()+"/"+spec.Name) == nil
	case serviceManagerSystemd:
		return c.serviceCmd(ctx, "systemctl", "--user", "is-active", "--quiet", spec.Name+".service") == nil
	}
	return false
}

func (c *commandContext) serviceInstall(ctx context.Context, opts serviceOptions) (serviceStatus, error) {
	spec, err := c.currentServiceSpec(opts.port)
	if err != nil {
		return serviceStatus{}, err
	}
	if spec.Manager == serviceManagerSystemd && strings.Contains(spec.Args[0], "$") {
		// systemd expands $ in ExecStart, and its ":" prefix that turns
		// expansion off needs systemd 249+. status and uninstall still work.
		return serviceStatus{}, fmt.Errorf("the ao binary path %q contains a $, which a systemd unit cannot run reliably; install ao in a path without one", spec.Args[0])
	}
	if err := refuseSelfHostedConflict(spec); err != nil {
		return serviceStatus{}, err
	}
	exists, owned, err := ownedServiceFile(spec.File)
	if err != nil {
		return serviceStatus{}, err
	}
	if exists && !owned {
		return serviceStatus{}, fmt.Errorf("%s exists and was not written by `ao service install`; leaving it alone", spec.File)
	}
	if !exists && spec.Manager == serviceManagerLaunchd {
		// The label is loaded but not from our file: another definition owns
		// it, and booting it out could stop something that isn't ours.
		if loaded, err := c.launchdJobLoaded(ctx, spec); err != nil {
			return serviceStatus{}, err
		} else if loaded {
			return serviceStatus{}, fmt.Errorf("a launchd job named %s is already loaded, but not from %s; leaving it alone. If it is a leftover of ours, remove it with: launchctl bootout %s/%s", spec.Name, spec.File, launchdDomain(), spec.Name)
		}
	}
	before, err := c.inspectDaemon(ctx)
	if err != nil {
		return serviceStatus{}, err
	}
	if (before.State == stateReady || before.State == stateNotReady) && before.workingDirectory != "" {
		// The run file may name a daemon for another data dir, as when only
		// AO_DATA_DIR changed: the service's serve would refuse it.
		_, dataDir, _, err := config.ResolvePaths()
		if err != nil {
			return serviceStatus{}, err
		}
		if err := checkDaemonDataDir(before, dataDir); err != nil {
			return serviceStatus{}, err
		}
	}
	// The daemon a previous install of this service runs is not "existing":
	// the reinstall replaces it.
	oldJobPID := c.servicePID(ctx, spec)
	content := renderLaunchdPlist(spec)
	if spec.Manager == serviceManagerSystemd {
		content = renderSystemdUnit(spec)
	}
	// A reinstall replaces our earlier definition; keep it, and whether that
	// service was running, so a failed reinstall can put it back.
	var previous []byte
	previousRunning, previousEnabled := false, false
	if exists {
		if previous, err = os.ReadFile(spec.File); err != nil {
			return serviceStatus{}, err
		}
		previousRunning = c.serviceLoaded(ctx, spec)
		previousEnabled = spec.Manager != serviceManagerSystemd ||
			c.serviceCmd(ctx, "systemctl", "--user", "is-enabled", "--quiet", spec.Name+".service") == nil
	}
	if err := os.MkdirAll(filepath.Dir(spec.File), 0o700); err != nil {
		return serviceStatus{}, err
	}
	if err := writeFileAtomic(spec.File, []byte(content)); err != nil {
		return serviceStatus{}, err
	}
	// Leave nothing behind when the service manager refuses the file; after
	// a failed reinstall, restore the earlier definition and its state.
	enabled := false
	rollback := func(cause error) (serviceStatus, error) {
		if exists {
			if err := c.restorePreviousService(ctx, spec, previous, previousRunning, previousEnabled); err != nil {
				return serviceStatus{}, fmt.Errorf("%w; restoring the previous service also failed: %w", cause, err)
			}
			return serviceStatus{}, cause
		}
		if enabled {
			// Keep the unit when its enablement links cannot be removed, so
			// `ao service uninstall` can still clean them up.
			if err := c.serviceCmd(ctx, "systemctl", "--user", "disable", spec.Name+".service"); err != nil {
				return serviceStatus{}, fmt.Errorf("%w; rollback also failed, so %s was kept for `ao service uninstall`: %w", cause, spec.File, err)
			}
		}
		_ = os.Remove(spec.File)
		return serviceStatus{}, cause
	}
	switch spec.Manager {
	case serviceManagerLaunchd:
		if exists {
			// Reload our earlier install so a changed binary, port or env
			// takes effect.
			_ = c.serviceCmd(ctx, "launchctl", "bootout", launchdDomain()+"/"+spec.Name)
		}
		if err := c.launchdBootstrap(ctx, spec); err != nil {
			return rollback(err)
		}
	case serviceManagerSystemd:
		if err := c.serviceCmd(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return rollback(fmt.Errorf("%w (is a systemd user session running?)", err))
		}
		if err := c.serviceCmd(ctx, "systemctl", "--user", "enable", spec.Name+".service"); err != nil {
			return rollback(err)
		}
		enabled = true
		if err := c.serviceCmd(ctx, "systemctl", "--user", "restart", spec.Name+".service"); err != nil {
			return rollback(err)
		}
	}
	st := serviceStatus{Manager: spec.Manager, Name: spec.Name, File: spec.File, Installed: true, Loaded: true, ExistingDaemon: before.State == stateReady && before.PID != oldJobPID}
	c.fillLinger(ctx, spec, &st)
	if opts.noWait {
		return st, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return serviceStatus{}, err
	}
	servePID, err := c.waitServiceAttached(ctx, spec, cfg, opts.timeout)
	if err != nil {
		return serviceStatus{}, fmt.Errorf("service installed, but %w", err)
	}
	if _, err := c.waitServeReady(ctx, cfg, 0, nil, opts.timeout, false, nil); err != nil {
		return serviceStatus{}, fmt.Errorf("service installed, but %w", err)
	}
	// Ready must mean supervised: the attached serve is still running.
	if _, err := os.Stat(serveAttachMarker(cfg.RunFilePath, servePID)); err != nil || c.servicePID(ctx, spec) != servePID {
		return serviceStatus{}, fmt.Errorf("service installed, but its ao serve (pid %d) exited after starting; check it with `ao service status`", servePID)
	}
	ds, err := c.inspectDaemon(ctx)
	if err != nil {
		return serviceStatus{}, err
	}
	st.Daemon = &ds
	if ds.State != stateReady || checkDaemonDataDir(ds, cfg.DataDir) != nil {
		return st, fmt.Errorf("service installed, but the daemon is %s now; check it with `ao service status`", ds.State)
	}
	return st, nil
}

// waitServiceAttached waits until the service's serve has started running or
// watching a daemon, so a command run right after install (such as `ao stop`)
// reaches it and is not mistaken for a missing daemon.
func (c *commandContext) waitServiceAttached(ctx context.Context, spec serviceSpec, cfg config.Config, timeout time.Duration) (int, error) {
	deadline := c.deps.Now().Add(timeout)
	for {
		if pid := c.servicePID(ctx, spec); pid > 0 {
			if _, err := os.Stat(serveAttachMarker(cfg.RunFilePath, pid)); err == nil {
				return pid, nil
			}
		}
		if !c.deps.Now().Before(deadline) {
			return 0, fmt.Errorf("the service did not start within %s; check it with `ao service status`", timeout)
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		c.deps.Sleep(250 * time.Millisecond)
	}
}

// selfHostedServiceNames are the user services scripts/setup-self-hosted.sh
// installs for a self-hosted host, by service manager. They supervise
// `ao daemon` themselves and are left as they are.
var selfHostedServiceNames = map[string]string{
	serviceManagerLaunchd: "dev.aoagents.self-hosted.plist",
	serviceManagerSystemd: "ao-self-hosted.service",
}

// selfHostedService returns the self-hosted host service file next to where
// spec would be written, and the data dir its daemon uses, when it exists.
func selfHostedService(spec serviceSpec) (file, dataDir string) {
	name := selfHostedServiceNames[spec.Manager]
	if name == "" {
		return "", ""
	}
	home, _ := os.UserHomeDir()
	// Next to our file, and where the setup script itself writes: it ignores
	// XDG_CONFIG_HOME and always uses ~/.config/systemd/user.
	candidates := []string{filepath.Join(filepath.Dir(spec.File), name)}
	if spec.Manager == serviceManagerSystemd && home != "" {
		candidates = append(candidates, filepath.Join(home, ".config", "systemd", "user", name))
	}
	var data []byte
	for _, candidate := range candidates {
		if b, err := os.ReadFile(candidate); err == nil {
			file, data = candidate, b
			break
		}
	}
	if file == "" {
		return "", ""
	}
	dataDir = filepath.Join(home, ".ao", "data")
	// The script's runner exports AO_DATA_DIR only when it was set at setup.
	if runner := selfHostedRunnerRE.Find(data); runner != nil {
		if body, err := os.ReadFile(string(runner)); err == nil {
			if m := selfHostedDataDirRE.FindSubmatch(body); m != nil {
				dataDir = shellUnquote(strings.TrimSpace(string(m[1])))
			}
		}
	}
	return file, dataDir
}

var (
	selfHostedRunnerRE  = regexp.MustCompile(`/[^"<>\n]*run-daemon\.sh`)
	selfHostedDataDirRE = regexp.MustCompile(`(?m)^export AO_DATA_DIR=(.+)$`)
)

// shellUnquote decodes one word as bash wrote it with printf %q, without
// running anything: backslash escapes, 'single', "double" and $'ANSI-C'
// quoting. Plain words come back unchanged.
func shellUnquote(word string) string {
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		ch := word[i]
		switch {
		case ch == '\\' && i+1 < len(word):
			i++
			b.WriteByte(word[i])
		case ch == '\'':
			end := strings.IndexByte(word[i+1:], '\'')
			if end < 0 {
				b.WriteString(word[i+1:])
				return b.String()
			}
			b.WriteString(word[i+1 : i+1+end])
			i += end + 1
		case ch == '"':
			for i++; i < len(word) && word[i] != '"'; i++ {
				if word[i] == '\\' && i+1 < len(word) && strings.IndexByte("$`\"\\\n", word[i+1]) >= 0 {
					i++
				}
				b.WriteByte(word[i])
			}
		case ch == '$' && i+1 < len(word) && word[i+1] == '\'':
			i = decodeANSIC(word, i+2, &b)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// decodeANSIC decodes the body of a $'...' word starting at word[i] into b,
// as bash does: single-letter escapes, octal \nnn, hex \xHH and \uHHHH /
// \UHHHHHHHH. printf %q writes non-ASCII bytes as octal under LC_ALL=C. It
// returns the index of the closing quote.
func decodeANSIC(word string, i int, b *strings.Builder) int {
	simple := map[byte]byte{'a': '\a', 'b': '\b', 'e': 0x1b, 'E': 0x1b, 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"', '?': '?'}
	digits := func(start, limit int, ok func(byte) bool) int {
		end := start
		for end < len(word) && end-start < limit && ok(word[end]) {
			end++
		}
		return end
	}
	isOct := func(c byte) bool { return c >= '0' && c <= '7' }
	isHex := func(c byte) bool { return strings.IndexByte("0123456789abcdefABCDEF", c) >= 0 }
	for ; i < len(word) && word[i] != '\''; i++ {
		if word[i] != '\\' || i+1 >= len(word) {
			b.WriteByte(word[i])
			continue
		}
		i++
		c := word[i]
		if v, ok := simple[c]; ok {
			b.WriteByte(v)
			continue
		}
		switch {
		case isOct(c):
			end := digits(i, 3, isOct)
			var v byte // wraps to one byte, as bash does for \777
			for _, d := range []byte(word[i:end]) {
				v = v*8 + (d - '0')
			}
			b.WriteByte(v)
			i = end - 1
		case c == 'x':
			end := digits(i+1, 2, isHex)
			if end == i+1 {
				b.WriteString(`\x`)
				continue
			}
			var v byte
			for _, d := range []byte(word[i+1 : end]) {
				v = v*16 + hexVal(d)
			}
			b.WriteByte(v)
			i = end - 1
		case c == 'u' || c == 'U':
			n := 4
			if c == 'U' {
				n = 8
			}
			end := digits(i+1, n, isHex)
			if end == i+1 {
				b.WriteByte('\\')
				b.WriteByte(c)
				continue
			}
			var r rune
			for _, d := range []byte(word[i+1 : end]) {
				r = r*16 + rune(hexVal(d))
			}
			b.WriteRune(r) // an out-of-range code point is written as U+FFFD
			i = end - 1
		default:
			b.WriteByte('\\')
			b.WriteByte(c)
		}
	}
	return i
}

func hexVal(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	default:
		return c - '0'
	}
}

// refuseSelfHostedConflict stops an install that would put a second service
// on the data dir a self-hosted host service already keeps running: the two
// would fight over one daemon, and the script's service restarts it after
// `ao stop`.
func refuseSelfHostedConflict(spec serviceSpec) error {
	file, theirs := selfHostedService(spec)
	if file == "" {
		return nil
	}
	_, ours, _, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	if !sameDir(theirs, ours) {
		return nil
	}
	remove := fmt.Sprintf("launchctl bootout %s/dev.aoagents.self-hosted && rm %q", launchdDomain(), file)
	if spec.Manager == serviceManagerSystemd {
		remove = fmt.Sprintf("systemctl --user disable --now ao-self-hosted.service && rm %q", file)
	}
	return fmt.Errorf("this machine already keeps Orchestrator running with the self-hosted host service %s, on the same data dir %s; a second service would fight it. Keep using that service, or remove it first: %s", file, ours, remove)
}

// fillLinger records, for a systemd service, whether the user lingers and the
// command that turns it on. It leaves both unset when loginctl cannot answer.
func (c *commandContext) fillLinger(ctx context.Context, spec serviceSpec, st *serviceStatus) {
	if spec.Manager != serviceManagerSystemd {
		return
	}
	name := currentUserName()
	if name == "" {
		return
	}
	out, err := c.deps.CommandOutput(ctx, "loginctl", "show-user", name, "-p", "Linger", "--value")
	if err != nil {
		return
	}
	linger := strings.TrimSpace(string(out)) == "yes"
	st.Linger = &linger
	if !linger {
		st.LingerFix = "loginctl enable-linger " + name
	}
}

func currentUserName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// servicePID is the PID of the service's running process, or 0.
func (c *commandContext) servicePID(ctx context.Context, spec serviceSpec) int {
	var out []byte
	var err error
	switch spec.Manager {
	case serviceManagerLaunchd:
		out, err = c.deps.CommandOutput(ctx, "launchctl", "print", launchdDomain()+"/"+spec.Name)
		if err != nil {
			return 0
		}
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && strings.TrimSpace(k) == "pid" {
				pid, _ := strconv.Atoi(strings.TrimSpace(v))
				return pid
			}
		}
	case serviceManagerSystemd:
		out, err = c.deps.CommandOutput(ctx, "systemctl", "--user", "show", "-p", "MainPID", "--value", spec.Name+".service")
		if err != nil {
			return 0
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		return pid
	}
	return 0
}

// launchdJobLoaded asks launchd whether the job is loaded. It tells a job
// launchd does not know (exit 113, "Could not find service") from a query
// that failed, which it returns as an error.
func (c *commandContext) launchdJobLoaded(ctx context.Context, spec serviceSpec) (bool, error) {
	out, err := c.deps.CommandOutput(ctx, "launchctl", "print", launchdDomain()+"/"+spec.Name)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if (errors.As(err, &exitErr) && exitErr.ExitCode() == 113) || strings.Contains(err.Error(), "exit status 113") || strings.Contains(string(out), "Could not find service") {
		return false, nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return false, fmt.Errorf("launchctl print: %w: %s", err, msg)
	}
	return false, fmt.Errorf("launchctl print: %w", err)
}

// restorePreviousService puts back the definition a failed reinstall
// replaced, and starts it again if it was running before.
func (c *commandContext) restorePreviousService(ctx context.Context, spec serviceSpec, previous []byte, wasRunning, wasEnabled bool) error {
	if err := writeFileAtomic(spec.File, previous); err != nil {
		return err
	}
	switch spec.Manager {
	case serviceManagerLaunchd:
		if wasRunning {
			return c.launchdBootstrap(ctx, spec)
		}
	case serviceManagerSystemd:
		if err := c.serviceCmd(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if !wasEnabled {
			// The failed install enabled it; a running unit keeps running.
			if err := c.serviceCmd(ctx, "systemctl", "--user", "disable", spec.Name+".service"); err != nil {
				return err
			}
		}
		if wasRunning {
			return c.serviceCmd(ctx, "systemctl", "--user", "restart", spec.Name+".service")
		}
	}
	return nil
}

// writeFileAtomic replaces path with data through a temporary file in the same
// directory, owner-only like the files install writes, so a failed write never leaves a truncated service definition
// (whose missing ownership marker would then block a retry).
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // a no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// launchdServiceGoneWait bounds how long install waits for launchd to tear a
// booted-out job down; launchd allows a job 20 s to exit before killing it.
const launchdServiceGoneWait = 30 * time.Second

// launchdBootstrap loads the plist. `launchctl bootout` returns before launchd
// has removed a running job, and bootstrapping the same label meanwhile fails
// with "Bootstrap failed: 5", so it waits for the old job to go first and
// retries a refused bootstrap a few times.
func (c *commandContext) launchdBootstrap(ctx context.Context, spec serviceSpec) error {
	deadline := c.deps.Now().Add(launchdServiceGoneWait)
	for c.serviceLoaded(ctx, spec) && c.deps.Now().Before(deadline) {
		c.deps.Sleep(250 * time.Millisecond)
	}
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			c.deps.Sleep(time.Second)
		}
		if err = c.serviceCmd(ctx, "launchctl", "bootstrap", launchdDomain(), spec.File); err == nil {
			return nil
		}
	}
	return err
}

func (c *commandContext) serviceStatus(ctx context.Context) (serviceStatus, error) {
	spec, err := c.currentServiceSpec(0)
	if err != nil {
		return serviceStatus{}, err
	}
	exists, owned, err := ownedServiceFile(spec.File)
	if err != nil {
		return serviceStatus{}, err
	}
	st := serviceStatus{Manager: spec.Manager, Name: spec.Name, File: spec.File, Installed: exists && owned}
	if st.Installed {
		st.Loaded = c.serviceLoaded(ctx, spec)
		if spec.Manager == serviceManagerSystemd {
			enabled := c.serviceCmd(ctx, "systemctl", "--user", "is-enabled", "--quiet", spec.Name+".service") == nil
			st.Enabled = &enabled
		}
	}
	c.fillLinger(ctx, spec, &st)
	if file, _ := selfHostedService(spec); file != "" {
		st.SelfHostedService = file
	}
	ds, err := c.inspectDaemon(ctx)
	if err != nil {
		return serviceStatus{}, err
	}
	st.Daemon = &ds
	return st, nil
}

func (c *commandContext) serviceUninstall(ctx context.Context) (serviceStatus, error) {
	spec, err := c.currentServiceSpec(0)
	if err != nil {
		return serviceStatus{}, err
	}
	exists, owned, err := ownedServiceFile(spec.File)
	if err != nil {
		return serviceStatus{}, err
	}
	st := serviceStatus{Manager: spec.Manager, Name: spec.Name, File: spec.File}
	if !exists {
		return st, nil
	}
	if !owned {
		return serviceStatus{}, fmt.Errorf("%s was not written by `ao service install`; leaving it alone", spec.File)
	}
	switch spec.Manager {
	case serviceManagerLaunchd:
		loaded, err := c.launchdJobLoaded(ctx, spec)
		if err != nil {
			// Keep the plist so a retry can still stop the job.
			return serviceStatus{}, fmt.Errorf("check whether %s is loaded before removing %s: %w", spec.Name, spec.File, err)
		}
		if loaded {
			if err := c.serviceCmd(ctx, "launchctl", "bootout", launchdDomain()+"/"+spec.Name); err != nil {
				return serviceStatus{}, err
			}
		}
	case serviceManagerSystemd:
		// disable --now also stops it. Keep the unit when that fails, so a
		// retry can still stop the service.
		if err := c.serviceCmd(ctx, "systemctl", "--user", "disable", "--now", spec.Name+".service"); err != nil {
			return serviceStatus{}, fmt.Errorf("stop the service before removing %s: %w", spec.File, err)
		}
	}
	if err := os.Remove(spec.File); err != nil && !errors.Is(err, os.ErrNotExist) {
		return serviceStatus{}, err
	}
	if spec.Manager == serviceManagerSystemd {
		_ = c.serviceCmd(ctx, "systemctl", "--user", "daemon-reload")
	}
	return st, nil
}
