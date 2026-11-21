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
	"path/filepath"
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
	ExistingDaemon bool          `json:"existingDaemon,omitempty"`
	Daemon         *daemonStatus `json:"daemon,omitempty"`
}

func newServiceCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Keep `ao serve` running across reboots",
		Long: "Keep the Orchestrator daemon running across logins and reboots, with no sudo.\n\n" +
			"macOS: a launchd user agent in ~/Library/LaunchAgents.\n" +
			"Linux: a systemd --user unit in ~/.config/systemd/user.\n" +
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
	b.WriteString("KillMode=process\nRestart=on-failure\nRestartSec=10\nStandardOutput=null\nStandardError=null\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
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
		return c.serviceCmd(ctx, "systemctl", "--user", "is-enabled", "--quiet", spec.Name+".service") == nil
	}
	return false
}

func (c *commandContext) serviceInstall(ctx context.Context, opts serviceOptions) (serviceStatus, error) {
	spec, err := c.currentServiceSpec(opts.port)
	if err != nil {
		return serviceStatus{}, err
	}
	exists, owned, err := ownedServiceFile(spec.File)
	if err != nil {
		return serviceStatus{}, err
	}
	if exists && !owned {
		return serviceStatus{}, fmt.Errorf("%s exists and was not written by `ao service install`; leaving it alone", spec.File)
	}
	before, err := c.inspectDaemon(ctx)
	if err != nil {
		return serviceStatus{}, err
	}
	// The daemon a previous install of this service runs is not "existing":
	// the reinstall replaces it.
	oldJobPID := c.servicePID(ctx, spec)
	content := renderLaunchdPlist(spec)
	if spec.Manager == serviceManagerSystemd {
		content = renderSystemdUnit(spec)
	}
	if err := os.MkdirAll(filepath.Dir(spec.File), 0o700); err != nil {
		return serviceStatus{}, err
	}
	if err := os.WriteFile(spec.File, []byte(content), 0o600); err != nil {
		return serviceStatus{}, err
	}
	// Leave nothing behind when the service manager refuses the file, unless
	// it replaced an earlier install of ours.
	enabled := false
	rollback := func(cause error) (serviceStatus, error) {
		if !exists {
			if enabled {
				// Keep the unit when its enablement links cannot be removed,
				// so `ao service uninstall` can still clean them up.
				if err := c.serviceCmd(ctx, "systemctl", "--user", "disable", spec.Name+".service"); err != nil {
					return serviceStatus{}, fmt.Errorf("%w; rollback also failed, so %s was kept for `ao service uninstall`: %w", cause, spec.File, err)
				}
			}
			_ = os.Remove(spec.File)
		}
		return serviceStatus{}, cause
	}
	switch spec.Manager {
	case serviceManagerLaunchd:
		if exists || c.serviceLoaded(ctx, spec) {
			// Reload so a changed binary path, port or env takes effect.
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
	if opts.noWait {
		return st, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return serviceStatus{}, err
	}
	if err := c.waitServiceAttached(ctx, spec, cfg, opts.timeout); err != nil {
		return serviceStatus{}, fmt.Errorf("service installed, but %w", err)
	}
	if _, err := c.waitServeReady(ctx, cfg, 0, nil, opts.timeout, false); err != nil {
		return serviceStatus{}, fmt.Errorf("service installed, but %w", err)
	}
	ds, err := c.inspectDaemon(ctx)
	if err != nil {
		return serviceStatus{}, err
	}
	st.Daemon = &ds
	return st, nil
}

// waitServiceAttached waits until the service's serve has started running or
// watching a daemon, so a command run right after install (such as `ao stop`)
// reaches it and is not mistaken for a missing daemon.
func (c *commandContext) waitServiceAttached(ctx context.Context, spec serviceSpec, cfg config.Config, timeout time.Duration) error {
	deadline := c.deps.Now().Add(timeout)
	for {
		if pid := c.servicePID(ctx, spec); pid > 0 {
			if _, err := os.Stat(serveAttachMarker(cfg.RunFilePath, pid)); err == nil {
				return nil
			}
		}
		if !c.deps.Now().Before(deadline) {
			return fmt.Errorf("the service did not start within %s; check it with `ao service status`", timeout)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		c.deps.Sleep(250 * time.Millisecond)
	}
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
		if c.serviceLoaded(ctx, spec) {
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
