package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemon"
)

// defaultServeReadyTimeout bounds how long `ao serve` waits for a daemon it
// started to answer /readyz. A first boot migrates a fresh database and probes
// installed agents, so it is generous.
const defaultServeReadyTimeout = 2 * time.Minute

// serveOwner is the run-file owner `ao serve` stamps on its daemon. The desktop
// app keeps a "persistent" daemon alive on attach and on quit instead of
// replacing it (frontend/src/shared/daemon-takeover.ts).
const serveOwner = "persistent"

// servePostHogKey is the public PostHog project key the desktop app stamps on
// the daemons it starts. Keep it equal to DEFAULT_POSTHOG_PROJECT_KEY in
// frontend/src/shared/posthog-config.ts (serve_test.go checks this).
const servePostHogKey = "phc_uXAqS8nokL2QLSGBZSEMHTUNVXsFeXu3SrcWG7fjEyVH"

// serveStatus values are part of the `ao serve --json` contract.
const (
	serveStatusReady = "ready"
)

type serveOptions struct {
	port       int
	foreground bool
	json       bool
	timeout    time.Duration
}

// serveResult is the stable `ao serve --json` output. `ao setup` and agents
// parse it: only add fields, never rename or remove one.
type serveResult struct {
	Status  string `json:"status"`
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	URL     string `json:"url"`
	Reused  bool   `json:"reused"`
	RunFile string `json:"runFile"`
	DataDir string `json:"dataDir"`
}

// startedProcess is a detached child the CLI launched. Exited delivers the
// child's exit error (nil on a clean exit) once it ends.
type startedProcess struct {
	PID    int
	Exited <-chan error
}

func newServeCommand(ctx *commandContext) *cobra.Command {
	opts := serveOptions{timeout: defaultServeReadyTimeout}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Orchestrator daemon without the desktop app",
		Long: "Run the Orchestrator daemon without the desktop app.\n\n" +
			"If a daemon is already serving this data directory, serve reports it and exits 0.\n" +
			"Otherwise it starts one in the background, waits until it is ready, and prints its URL.\n" +
			"The desktop app attaches to this daemon and keeps it running when the app quits.\n\n" +
			"Stop it with `ao stop`. Keep it running across reboots with `ao service install`.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.port < 0 || opts.port > 65535 {
				return usageError{fmt.Errorf("invalid --port %d: out of range 1-65535", opts.port)}
			}
			if opts.timeout <= 0 {
				return usageError{fmt.Errorf("invalid --timeout %s: must be positive", opts.timeout)}
			}
			res, err := ctx.serve(cmd.Context(), cmd.ErrOrStderr(), opts)
			if err != nil {
				return err
			}
			if res == nil {
				// The foreground daemon ran and exited cleanly.
				return nil
			}
			return writeServeResult(cmd.OutOrStdout(), *res, opts.json)
		},
	}
	cmd.Flags().IntVar(&opts.port, "port", 0, "Loopback port for a new daemon (default: AO_PORT, else 3001)")
	cmd.Flags().BoolVar(&opts.foreground, "foreground", false, "Run the daemon in this process instead of in the background")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the result as JSON")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultServeReadyTimeout, "How long to wait for a new daemon to become ready")
	return cmd
}

func writeServeResult(w io.Writer, res serveResult, asJSON bool) error {
	if asJSON {
		return writeJSON(w, res)
	}
	verb := "started"
	if res.Reused {
		verb = "already running"
	}
	_, err := fmt.Fprintf(w, "Orchestrator daemon %s at %s (pid %d)\n  data dir: %s\n  stop it with: ao stop\n", verb, res.URL, res.PID, res.DataDir)
	return err
}

// serve reuses a daemon that already serves this data dir, or starts one. It
// returns nil, nil after a foreground daemon exits cleanly.
func (c *commandContext) serve(ctx context.Context, errOut io.Writer, opts serveOptions) (*serveResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	// A second daemon on the same data dir would block on its lock for
	// minutes and then exit 75, so an existing one must be found first.
	st, err := c.inspectDaemon(ctx)
	if err != nil {
		return nil, err
	}
	switch st.State {
	case stateReady:
		if opts.port != 0 && opts.port != st.Port {
			_, _ = fmt.Fprintf(errOut, "warning: a daemon is already serving on port %d; --port %d was ignored\n", st.Port, opts.port)
		}
		res := newServeResult(st, true)
		return &res, nil
	case stateNotReady:
		// Ours, alive and still booting: wait for it instead of starting another.
		return c.waitServeReady(ctx, cfg, 0, nil, opts.timeout, true)
	case stateUnhealthy:
		return nil, fmt.Errorf("a daemon (pid %d) is running for %s but is not healthy: %s; check it with `ao status` or stop it with `ao stop`", st.PID, cfg.DataDir, st.Error)
	}

	env := serveEnv(os.Environ(), opts.port, telemetryOptedOut(cfg.DataDir), Version)
	if opts.foreground {
		return nil, runForegroundDaemon(env)
	}

	exe, err := c.deps.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve ao executable: %w", err)
	}
	child, err := c.deps.StartDetached(processStartConfig{Path: exe, Args: []string{"daemon"}, Env: env})
	if err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	return c.waitServeReady(ctx, cfg, child.PID, child.Exited, opts.timeout, false)
}

// waitServeReady polls the run file and /readyz until a daemon for this data
// dir is ready. wantPID, when set, is the daemon this command started; exited
// reports that child ending early.
func (c *commandContext) waitServeReady(ctx context.Context, cfg config.Config, wantPID int, exited <-chan error, timeout time.Duration, reused bool) (*serveResult, error) {
	deadline := c.deps.Now().Add(timeout)
	logPath := filepath.Join(cfg.DataDir, "daemon.log")
	var last daemonStatus
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case exitErr := <-exited:
			return nil, daemonExitedError(exitErr, cfg.DataDir, logPath)
		default:
		}
		st, err := c.inspectDaemon(ctx)
		if err != nil {
			return nil, err
		}
		last = st
		// Another daemon that won a concurrent start is just as good: it
		// serves the same data dir.
		if st.State == stateReady {
			res := newServeResult(st, reused || (wantPID != 0 && st.PID != wantPID))
			return &res, nil
		}
		if !c.deps.Now().Before(deadline) {
			detail := string(last.State)
			if last.Error != "" {
				detail += ": " + last.Error
			}
			return nil, fmt.Errorf("daemon did not become ready within %s (last state %s); see %s", timeout, detail, logPath)
		}
		c.deps.Sleep(200 * time.Millisecond)
	}
}

func daemonExitedError(exitErr error, dataDir, logPath string) error {
	var ee *exec.ExitError
	if errors.As(exitErr, &ee) && ee.ExitCode() == exitDataDirLocked {
		return fmt.Errorf("daemon exited: another Orchestrator process holds the data dir %s; see %s", dataDir, logPath)
	}
	if exitErr == nil {
		return fmt.Errorf("daemon exited before it became ready; see %s", logPath)
	}
	return fmt.Errorf("daemon exited before it became ready: %w; see %s", exitErr, logPath)
}

func newServeResult(st daemonStatus, reused bool) serveResult {
	return serveResult{
		Status:  serveStatusReady,
		Port:    st.Port,
		PID:     st.PID,
		URL:     fmt.Sprintf("http://%s:%d", config.LoopbackHost, st.Port),
		Reused:  reused,
		RunFile: st.RunFile,
		DataDir: st.DataDir,
	}
}

// telemetryOptedOut reports whether the user turned off "Share usage
// analytics" in the desktop app, which leaves a marker in the data dir.
func telemetryOptedOut(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, telemetryadapter.OptOutFile))
	return err == nil
}

// serveEnv builds the daemon's environment from the caller's. It marks the
// daemon persistent, applies --port, and turns usage analytics on the way the
// desktop app does (frontend/src/main.ts telemetryOverrides), unless the user
// opted out in the app, set AO_TELEMETRY_REMOTE themselves, or set
// DO_NOT_TRACK. Any other explicit AO_TELEMETRY_* value wins.
func serveEnv(base []string, port int, optedOut bool, version string) []string {
	env := envMap(base)
	env["AO_OWNER"] = serveOwner
	if port != 0 {
		env["AO_PORT"] = strconv.Itoa(port)
	}
	setDefault := func(key, value string) {
		if _, ok := env[key]; !ok {
			env[key] = value
		}
	}
	remote := "posthog"
	// Unreleased builds never export, matching the app's dev builds, so a dev
	// loop or CI run does not count as an install.
	if optedOut || doNotTrack(env["DO_NOT_TRACK"]) || version == "" || version == "dev" {
		remote = "off"
	}
	if optedOut {
		// The app's switch beats an inherited env default.
		env["AO_TELEMETRY_REMOTE"] = "off"
	}
	setDefault("AO_TELEMETRY_REMOTE", remote)
	setDefault("AO_TELEMETRY_EVENTS", "on")
	setDefault("AO_TELEMETRY_POSTHOG_KEY", servePostHogKey)
	setDefault("AO_TELEMETRY_POSTHOG_HOST", config.DefaultTelemetryPostHogHost)
	if version != "" && version != "dev" {
		setDefault("AO_TELEMETRY_APP_VERSION", version)
	}
	return envList(env)
}

// doNotTrack honours the DO_NOT_TRACK convention: any value but empty, "0"
// or "false" opts out.
func doNotTrack(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	return v != "" && v != "0" && v != "false"
}

func envMap(base []string) map[string]string {
	env := make(map[string]string, len(base))
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		env[k] = v
	}
	return env
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// runForegroundDaemon runs the daemon in this process with env applied, so
// a supervisor (launchd, systemd, a terminal) owns it directly.
func runForegroundDaemon(env []string) error {
	for k, v := range envMap(env) {
		if os.Getenv(k) == v {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return daemon.Run(Version)
}
