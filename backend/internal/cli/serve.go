package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemon"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionenv"
)

// defaultServeReadyTimeout bounds how long `ao serve` waits for a daemon it
// started to answer /readyz. A first boot migrates a fresh database and probes
// installed agents, so it is generous.
const defaultServeReadyTimeout = 2 * time.Minute

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
	background bool
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
	// owner is the daemon's run-file owner, from the same read as PID.
	owner string
}

// startedProcess is a detached child the CLI launched. Exited delivers the
// child's exit error (nil on a clean exit) once it ends.
type startedProcess struct {
	PID    int
	Exited <-chan error
	// Kill stops the child; nil when the starter cannot.
	Kill func() error
}

func newServeCommand(ctx *commandContext) *cobra.Command {
	opts := serveOptions{timeout: defaultServeReadyTimeout}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Orchestrator daemon without the desktop app",
		Long: "Run the Orchestrator daemon without the desktop app.\n\n" +
			"If a daemon is already serving this data directory, serve reports it and exits 0.\n" +
			"Otherwise it starts one in the background, waits until it is ready, and prints its URL.\n" +
			"When the desktop app opens, it swaps in its own daemon on the same data directory, which keeps running after the app quits.\n\n" +
			"With --foreground, serve runs the daemon in this process, or watches the one already running, until no daemon is left.\n" +
			"It exits 0 after a clean stop and 1 when the daemon crashed or quit with the app, so a service manager restarts it.\n\n" +
			"Stop it with `ao stop`. Keep it running across reboots with `ao service install`.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.port < 0 || opts.port > 65535 {
				return usageError{fmt.Errorf("invalid --port %d: out of range 1-65535", opts.port)}
			}
			if opts.timeout <= 0 {
				return usageError{fmt.Errorf("invalid --timeout %s: must be positive", opts.timeout)}
			}
			if opts.background && opts.foreground {
				return usageError{errors.New("--background and --foreground cannot be used together")}
			}
			if err := applyPortFlag(opts.port); err != nil {
				return err
			}
			res, err := ctx.serve(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
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
	cmd.Flags().BoolVar(&opts.foreground, "foreground", false, "Run (or watch) the daemon in this process until no daemon is left")
	cmd.Flags().BoolVar(&opts.background, "background", false, "Start the daemon in the background and return once it is ready (the default)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the result as JSON")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultServeReadyTimeout, "How long to wait for a background or booting daemon to become ready (a --foreground in-process daemon starts without a deadline)")
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

// applyPortFlag makes an explicit --port the effective AO_PORT before the
// configuration is loaded, so an invalid inherited AO_PORT does not fail a
// command that overrides it. Without the flag, AO_PORT is validated as usual.
func applyPortFlag(port int) error {
	if port == 0 {
		return nil
	}
	return os.Setenv("AO_PORT", strconv.Itoa(port))
}

// serve reuses a daemon that already serves this data dir, or starts one. In
// the foreground it returns nil, nil once there is no daemon left to run or
// watch.
func (c *commandContext) serve(ctx context.Context, out, errOut io.Writer, opts serveOptions) (*serveResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if opts.foreground {
		return nil, c.serveForeground(ctx, out, errOut, cfg, opts)
	}
	if res, err := c.existingDaemon(ctx, errOut, cfg, opts); err != nil || res != nil {
		return res, err
	}
	env := serveEnv(os.Environ(), opts.port, telemetryOptedOut(cfg.DataDir), Version)
	noteAnalyticsOnce(errOut, cfg, env)
	exe, err := c.deps.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve ao executable: %w", err)
	}
	child, err := c.deps.StartDetached(processStartConfig{Path: exe, Args: []string{"daemon"}, Env: env})
	if err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	// Forward the child's one exit notification so both the readiness wait
	// and the loser cleanup can observe it.
	exited := make(chan error, 1)
	childDone := make(chan struct{})
	go func() {
		e := <-child.Exited
		exited <- e
		close(childDone)
	}()
	res, err := c.waitServeReady(ctx, cfg, child.PID, exited, opts.timeout, false, nil)
	if res != nil && res.PID != child.PID {
		// A concurrent serve won the start. Our child is waiting on the
		// data-dir lock and would take over the moment the winner stops,
		// so stop and reap it, unless it already exited.
		stopLosingChild(child, childDone)
	}
	if res != nil && opts.port != 0 && res.Port != opts.port && res.PID == child.PID {
		_, _ = fmt.Fprintf(errOut, "warning: port %d was not available; the daemon is serving on port %d\n", opts.port, res.Port)
	}
	return res, err
}

// stopLosingChild kills a started daemon that lost a concurrent start and
// waits briefly for it to exit, so it cannot become a second daemon later.
func stopLosingChild(child startedProcess, done <-chan struct{}) {
	select {
	case <-done:
		return // already gone
	default:
	}
	if child.Kill == nil {
		return
	}
	_ = child.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// existingDaemon returns the daemon that already serves this data dir, or
// nil when one must be started. A second daemon on the same data dir would
// block on its lock for minutes and then exit 75, so this check comes first.
func (c *commandContext) existingDaemon(ctx context.Context, errOut io.Writer, cfg config.Config, opts serveOptions) (*serveResult, error) {
	st, err := c.inspectDaemon(ctx)
	if err != nil {
		return nil, err
	}
	switch st.State {
	case stateReady:
		if err := checkDaemonDataDir(st, cfg.DataDir); err != nil {
			return nil, err
		}
		if opts.port != 0 && opts.port != st.Port {
			_, _ = fmt.Fprintf(errOut, "warning: a daemon is already serving on port %d; --port %d was ignored\n", st.Port, opts.port)
		}
		res := newServeResult(st, true)
		return &res, nil
	case stateNotReady:
		// Ours, alive and still booting: wait for it instead of starting another.
		return c.waitServeReady(ctx, cfg, 0, nil, opts.timeout, true, &st)
	case stateUnhealthy:
		return nil, fmt.Errorf("a daemon (pid %d) is running for %s but is not healthy: %s; check it with `ao status` or stop it with `ao stop`", st.PID, cfg.DataDir, st.Error)
	}
	return nil, nil
}

// checkDaemonDataDir refuses a daemon that the run file names but that serves
// another data dir, as when only AO_DATA_DIR was changed and the default run
// file still points at the main daemon.
func checkDaemonDataDir(st daemonStatus, dataDir string) error {
	if st.workingDirectory == "" {
		// Every daemon since v0.10.0 reports it; without it the data dir
		// cannot be verified, and starting a second daemon is no safer.
		return fmt.Errorf("the daemon in %s (pid %d, port %d) does not report its data dir, so serve cannot tell whether it serves %s; stop it with `ao stop` or update Orchestrator", st.RunFile, st.PID, st.Port, dataDir)
	}
	if sameDir(st.workingDirectory, dataDir) {
		return nil
	}
	return fmt.Errorf("the daemon in %s (pid %d, port %d) serves data dir %s, not %s; set AO_RUN_FILE and AO_PORT too to run a separate daemon", st.RunFile, st.PID, st.Port, st.workingDirectory, dataDir)
}

// sameDir compares filesystem identity, so symlinks and case-insensitive
// volumes match while distinct directories on a case-sensitive one do not.
func sameDir(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ai, bi)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// serveForegroundDaemon runs the daemon in this process; tests replace it.
var serveForegroundDaemon = runForegroundDaemon

// How long a foreground serve waits for the desktop app's replacement daemon
// after the one it watched stops, and how often it checks the daemon it watches.
const (
	serveReplacementWait = 20 * time.Second
	serveWatchInterval   = 5 * time.Second
)

// serveForeground runs the daemon in this process, or watches the one that
// already serves this data dir, for as long as one does. It exits 0 after a
// clean stop (`ao stop`), and with an error, so launchd or systemd starts it
// again, when the daemon it watches crashed or was the desktop app's own and
// went away with the app. The app replaces a daemon it did not start with its
// own on launch; watching that replacement keeps a service-managed install
// supervised.
func (c *commandContext) serveForeground(parent context.Context, out, errOut io.Writer, cfg config.Config, opts serveOptions) error {
	// SIGTERM (launchctl bootout, systemctl stop) or Ctrl-C ends serve at
	// once with exit 0; the in-process daemon also receives it and shuts down.
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := c.serveForegroundLoop(ctx, out, errOut, cfg, opts)
	if ctx.Err() != nil && parent.Err() == nil {
		return nil
	}
	return err
}

func (c *commandContext) serveForegroundLoop(ctx context.Context, out, errOut io.Writer, cfg config.Config, opts serveOptions) error {
	sawDaemon, sawOwner, stopped, err := c.settleStartup(ctx, opts.timeout)
	if err != nil || stopped {
		return err
	}
	res, err := c.existingDaemon(ctx, errOut, cfg, opts)
	var stoppedErr daemonStoppedError
	if errors.As(err, &stoppedErr) {
		// Gone while it booted: follow a replacement if one comes (an app
		// swap); otherwise the stop sticks, unless it was the app's own
		// daemon quitting with the app, when the service manager should
		// start one.
		if res, err = c.waitForReplacement(ctx, cfg); err != nil {
			return err
		}
		if res == nil {
			if stoppedErr.owner == "app" {
				return errors.New("the desktop app's daemon stopped with the app while it was starting; exiting so the service manager starts one")
			}
			return nil
		}
	} else if err != nil {
		return err
	}
	if res == nil && sawDaemon {
		// The daemon seen a moment ago went away cleanly, as under an
		// `ao stop`: follow a replacement if one comes, and do not start a
		// daemon in its place.
		if res, err = c.waitForReplacement(ctx, cfg); err != nil {
			return err
		}
		if res == nil {
			// Like watchDaemon: the app's own daemon quitting with the app is
			// a reason to run one, not a stop to honour.
			if sawOwner == "app" {
				return errors.New("the desktop app's daemon stopped with the app while this serve started; exiting so the service manager starts one")
			}
			return nil
		}
	}
	// Tell `ao service install` that this serve has decided what to run, so
	// an `ao stop` after install returns reaches a watcher, not a starting
	// serve that would take the stopped daemon's place.
	marker := serveAttachMarker(cfg.RunFilePath, os.Getpid())
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return fmt.Errorf("create run-file dir: %w", err)
	}
	if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return fmt.Errorf("write attach marker: %w", err)
	}
	defer func() { _ = os.Remove(marker) }()
	if res == nil {
		env := absoluteStatePaths(serveEnv(os.Environ(), opts.port, telemetryOptedOut(cfg.DataDir), Version), cfg)
		noteAnalyticsOnce(errOut, cfg, env)
		// daemon.Run installs its own SIGINT/SIGTERM handler only after the
		// data-dir lock and store setup; until then serveForeground's handler
		// alone sees a stop. A stop before the daemon has published its run
		// file ends the process at once; after that, the daemon's handler
		// shuts it down and the stop is not mistaken for an app swap.
		running := make(chan struct{})
		var mu sync.Mutex
		returned := false
		go func() {
			select {
			case <-ctx.Done():
				mu.Lock() // held through os.Exit, so it cannot race the return
				if !returned {
					if info, err := runfile.Read(cfg.RunFilePath); err != nil || info == nil || info.PID != os.Getpid() {
						_ = os.Remove(marker)
						os.Exit(0)
					}
				}
				mu.Unlock()
			case <-running:
			}
		}()
		err := serveForegroundDaemon(env)
		mu.Lock()
		returned = true // from here the CLI reports err itself
		mu.Unlock()
		close(running)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil // stopped by a signal: no replacement to wait for
		}
		// A clean exit is `ao stop`, or the app replacing this daemon.
		res, err = c.waitForReplacement(ctx, cfg)
		if err != nil || res == nil {
			return err
		}
	}
	if err := writeServeResult(out, *res, opts.json); err != nil {
		return err
	}
	return c.watchDaemon(ctx, cfg, res.PID, res.owner)
}

// serveAttachMarker is the file a foreground serve with this PID keeps next to
// the run file once it is running or watching a daemon.
func serveAttachMarker(runFile string, pid int) string {
	return filepath.Join(filepath.Dir(runFile), fmt.Sprintf("serve-foreground-%d.attached", pid))
}

// settleStartup waits out a daemon that is alive but not answering, as one
// that `ao stop` is shutting down while this serve starts. It reports whether
// it saw a live daemon at all and that daemon's run-file owner, and
// stopped=true when that daemon then stopped cleanly: the stop was meant to
// stick. The desktop app's own daemon stopping is not such a stop.
func (c *commandContext) settleStartup(ctx context.Context, timeout time.Duration) (sawDaemon bool, owner string, stopped bool, err error) {
	deadline := c.deps.Now().Add(timeout)
	settling := false
	for {
		st, err := c.inspectDaemon(ctx)
		if err != nil {
			return false, "", false, err
		}
		switch st.State {
		case stateUnhealthy:
			settling = true
			owner = st.owner
		case stateStopped:
			// An app-owned daemon that quit with the app is not a stop to
			// honour: let the caller start one.
			if settling && owner == "app" {
				return false, "", false, nil
			}
			return settling, owner, settling, nil
		case stateReady, stateNotReady:
			return true, st.owner, false, nil
		default:
			return false, "", false, nil
		}
		if !c.deps.Now().Before(deadline) {
			return true, owner, false, nil // existingDaemon reports the unhealthy daemon
		}
		if err := ctx.Err(); err != nil {
			return false, "", false, err
		}
		c.deps.Sleep(250 * time.Millisecond)
	}
}

// waitForReplacement waits briefly for another daemon to become ready for
// this data dir. It returns nil, nil when none does.
func (c *commandContext) waitForReplacement(ctx context.Context, cfg config.Config) (*serveResult, error) {
	deadline := c.deps.Now().Add(serveReplacementWait)
	var last daemonStatus
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		st, err := c.inspectDaemon(ctx)
		if err != nil {
			return nil, err
		}
		if st.State == stateReady {
			if err := checkDaemonDataDir(st, cfg.DataDir); err != nil {
				return nil, err
			}
			res := newServeResult(st, true)
			return &res, nil
		}
		last = st
		if !c.deps.Now().Before(deadline) {
			if last.State == stateStale {
				// A replacement started, wrote its run file and died.
				return nil, fmt.Errorf("the replacement daemon (pid %d) exited without shutting down; exiting so the service manager restarts it", last.PID)
			}
			return nil, nil
		}
		c.deps.Sleep(500 * time.Millisecond)
	}
}

// watchDaemon follows the daemon serving this data dir until none is left.
func (c *commandContext) watchDaemon(ctx context.Context, cfg config.Config, pid int, owner string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.deps.Sleep(serveWatchInterval)
		st, err := c.inspectDaemon(ctx)
		if err != nil {
			return err
		}
		switch st.State {
		case stateReady:
			if st.PID != pid {
				if err := checkDaemonDataDir(st, cfg.DataDir); err != nil {
					return err
				}
				pid, owner = st.PID, st.owner
			}
		case stateNotReady:
			// A replacement still booting (such as the app's): follow it now,
			// so its owner decides what its disappearance means.
			if st.PID != pid && st.workingDirectory != "" && checkDaemonDataDir(st, cfg.DataDir) == nil {
				pid, owner = st.PID, st.owner
			}
		case stateStale:
			// The run file outlived its daemon: it did not shut down cleanly.
			return fmt.Errorf("the daemon (pid %d) exited without shutting down; exiting so the service manager restarts it", pid)
		case stateStopped:
			next, err := c.waitForReplacement(ctx, cfg)
			if err != nil {
				return err
			}
			if next != nil {
				pid, owner = next.PID, next.owner
				continue
			}
			if owner == "app" {
				return fmt.Errorf("the desktop app's daemon (pid %d) stopped with the app; exiting so the service manager starts one", pid)
			}
			return nil
		}
	}
}

// waitServeReady polls the run file and /readyz until a daemon for this data
// dir is ready. wantPID, when set, is the daemon this command started; exited
// reports that child ending early. seen, when set, is the daemon already observed booting, so
// a stop right after that observation is reported as a stop.
func (c *commandContext) waitServeReady(ctx context.Context, cfg config.Config, wantPID int, exited <-chan error, timeout time.Duration, reused bool, seen *daemonStatus) (*serveResult, error) {
	deadline := c.deps.Now().Add(timeout)
	logPath := filepath.Join(cfg.DataDir, "daemon.log")
	var last daemonStatus
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case exitErr := <-exited:
			// The child may have lost the data-dir lock to a concurrent
			// start; that winner serves this data dir just as well.
			if st, err := c.inspectDaemon(ctx); err == nil && st.State == stateReady && checkDaemonDataDir(st, cfg.DataDir) == nil {
				res := newServeResult(st, true)
				return &res, nil
			}
			return nil, daemonExitedError(exitErr, cfg.DataDir, logPath)
		default:
		}
		st, err := c.inspectDaemon(ctx)
		if err != nil {
			return nil, err
		}
		last = st
		switch st.State {
		case stateNotReady, stateUnhealthy:
			if st.PID != 0 {
				seen = &st
			}
		case stateStopped:
			// A daemon seen booting whose run file is now gone was stopped
			// cleanly, as by `ao stop`: report that, not a timeout.
			if seen != nil {
				return nil, daemonStoppedError{pid: seen.PID, owner: seen.owner}
			}
		}
		// Another daemon that won a concurrent start is just as good: it
		// serves the same data dir.
		if st.State == stateReady {
			if err := checkDaemonDataDir(st, cfg.DataDir); err != nil {
				return nil, err
			}
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

// daemonStoppedError is a daemon that was seen starting and then stopped
// cleanly before it became ready.
type daemonStoppedError struct {
	pid   int
	owner string
}

func (e daemonStoppedError) Error() string {
	return fmt.Sprintf("the daemon (pid %d) was stopped while it was starting", e.pid)
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
		owner:   st.owner,
	}
}

// telemetryOptedOut reports whether the user turned off "Share usage
// analytics" in the desktop app, which leaves a marker in the data dir.
func telemetryOptedOut(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, telemetryadapter.OptOutFile))
	return err == nil
}

// serveEnv builds the daemon's environment from the caller's. It leaves the
// daemon unowned (headless), applies --port, and turns usage analytics on the way the
// desktop app does (frontend/src/main.ts telemetryOverrides), unless the user
// opted out in the app, set AO_TELEMETRY_REMOTE themselves, or set
// DO_NOT_TRACK. Any other explicit AO_TELEMETRY_* value wins.
func serveEnv(base []string, port int, optedOut bool, version string) []string {
	// The daemon passes its environment to every session it spawns, so the
	// calling shell's session and agent variables must not reach it: an
	// agent's `ao serve` would otherwise make every session look nested in
	// that agent, or claim to be the session the agent runs in.
	//
	// That includes AO_OWNER: an empty run-file owner is the headless
	// convention. The desktop app keeps such a daemon's replacement alive
	// after it quits, just as for "persistent"
	// (frontend/src/shared/daemon-takeover.ts), and the daemon does not tell
	// sessions they run under the desktop app
	// (session_manager.desktopOwnedDaemon), which would send macOS permission
	// prompts to the wrong app.
	env := map[string]string{}
	for k, v := range envMap(sessionenv.ScrubForDaemon(base)) {
		// On Windows Do_Not_Track is DO_NOT_TRACK: compare names as the OS
		// does, so an opt-out spelled in any case is honoured.
		env[sessionenv.NormalizeKey(k)] = v
	}
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
	if optedOut || doNotTrack(env["DO_NOT_TRACK"]) {
		// An opt-out beats an inherited AO_TELEMETRY_REMOTE.
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

// serveAnalyticsNotice is printed once per state dir, the first time `ao serve`
// starts a daemon that exports usage analytics. A headless or agent-driven
// user never sees the app's "Share usage analytics" switch.
const serveAnalyticsNotice = "Orchestrator shares anonymous usage analytics (never code, prompts or conversations). To turn them off, set DO_NOT_TRACK=1, or turn off Share usage analytics in the app's settings."

// noteAnalyticsOnce prints serveAnalyticsNotice to errOut when env exports
// analytics and the notice has not been shown for this state dir yet.
func noteAnalyticsOnce(errOut io.Writer, cfg config.Config, env []string) {
	if envMap(env)["AO_TELEMETRY_REMOTE"] != "posthog" {
		return
	}
	marker := filepath.Join(filepath.Dir(cfg.RunFilePath), "serve-analytics-notice-shown")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	_, _ = fmt.Fprintln(errOut, serveAnalyticsNotice)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err == nil {
		_ = os.WriteFile(marker, nil, 0o600)
	}
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
	if err := applyProcessEnv(env); err != nil {
		return err
	}
	return daemon.Run(Version)
}

// absoluteStatePaths rewrites AO_DATA_DIR and AO_RUN_FILE in env to the
// absolute paths cfg resolved. The in-process daemon chdirs into its data
// dir, after which a relative value would resolve somewhere else.
func absoluteStatePaths(env []string, cfg config.Config) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch sessionenv.NormalizeKey(k) {
		case "AO_DATA_DIR":
			kv = k + "=" + cfg.DataDir
		case "AO_RUN_FILE":
			kv = k + "=" + cfg.RunFilePath
		}
		out = append(out, kv)
	}
	return out
}

// applyProcessEnv makes this process's environment exactly env: it sets
// every entry and unsets variables env leaves out, such as an inherited
// AO_OWNER.
func applyProcessEnv(env []string) error {
	want := envMap(env)
	for k := range envMap(os.Environ()) {
		if _, ok := want[k]; !ok {
			if err := os.Unsetenv(k); err != nil {
				return err
			}
		}
	}
	for k, v := range want {
		if os.Getenv(k) == v {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}
