// spawn.go - shared detached pty-host spawn support. Platform files provide
// defaultSpawnHost; tests can inject a hostSpawner through Options.
package conpty

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// HostOptions are the optional pty-host behaviours chosen at spawn.
type HostOptions struct {
	// StartOnAttach starts the process at its first client's grid (deferredPTY).
	StartOnAttach bool
	// LazySurface builds the rendered surface only when it is first read.
	LazySurface bool
	// Cols and Rows, when both set, start the process immediately at that grid
	// (taking precedence over StartOnAttach).
	Cols, Rows uint16
}

// hostSpawner starts a detached pty-host for the session and returns its
// loopback address ("127.0.0.1:PORT") and OS pid once it prints READY.
// Injectable for tests: replace this field on Options before calling New.
type hostSpawner func(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string, opts HostOptions) (addr string, pid int, err error)

// Optional leading pty-host arguments. Session ids never contain "=", so they
// cannot be mistaken for the positional session id; a host spawned without
// them, for example by an older daemon, behaves as before.
const (
	startOnAttachArg = "--start=attach"
	lazySurfaceArg   = "--surface=lazy"
	sizeArgPrefix    = "--size="
)

// ptyHostArgs builds the pty-host subcommand argv:
// pty-host [--start=attach] [--surface=lazy] <sessionID> <cwd> <shellCmd> <shellArgs...>
func ptyHostArgs(sessionID, cwd string, argv []string, opts HostOptions) []string {
	args := []string{"pty-host"}
	if opts.StartOnAttach {
		args = append(args, startOnAttachArg)
	}
	if opts.LazySurface {
		args = append(args, lazySurfaceArg)
	}
	if opts.Cols > 0 && opts.Rows > 0 {
		args = append(args, fmt.Sprintf("%s%dx%d", sizeArgPrefix, opts.Cols, opts.Rows))
	}
	args = append(args, sessionID, cwd)
	return append(args, argv...)
}

// splitHostOptionArgs consumes the optional leading option arguments from the
// pty-host argv (everything after the subcommand name).
func splitHostOptionArgs(args []string) (HostOptions, []string) {
	var opts HostOptions
	for len(args) > 0 {
		switch args[0] {
		case startOnAttachArg:
			opts.StartOnAttach = true
		case lazySurfaceArg:
			opts.LazySurface = true
		default:
			if !strings.HasPrefix(args[0], sizeArgPrefix) {
				return opts, args
			}
			var cols, rows uint16
			if _, err := fmt.Sscanf(strings.TrimPrefix(args[0], sizeArgPrefix), "%dx%d", &cols, &rows); err == nil && cols > 0 && rows > 0 {
				opts.Cols, opts.Rows = cols, rows
			}
		}
		args = args[1:]
	}
	return opts, args
}

// cleanupStartedHostFailure preserves evidence of a child that may still own
// the session. A successful kill proves the failed spawn left no runtime;
// otherwise the stable PID and joined cleanup error let Create expose a typed
// partial-effect result instead of incorrectly claiming no effect.
func cleanupStartedHostFailure(pid int, cause error, kill func() error) (int, error) {
	if killErr := kill(); killErr != nil {
		return pid, errors.Join(cause, fmt.Errorf("kill started pty-host pid %d: %w", pid, killErr))
	}
	return 0, cause
}

// stripEnvAssignments splits a launch argv that may begin with a Unix-style
// `env NAME=VALUE ...` prefix into the environment assignments ("NAME=VALUE"
// strings) and the real command argv that follows.
//
// Some agent adapters (e.g. opencode) prepend `env KEY=value` to their launch
// command to inject process env vars the CLI has no flag for. That is portable
// on macOS/Linux, where the tmux runtime runs the argv through a shell and the
// `env` coreutil applies the assignments. Windows has no `env` binary and the
// ConPTY pty-host execs argv[0] directly, so the spawner must apply the
// assignments to the child's environment itself — otherwise the launch fails
// with `env: executable file not found`. This mirrors launchBinary in the
// session manager, which already skips the same prefix to validate the real
// binary.
//
// If argv does not start with `env`, or the prefix consumes the whole argv with
// no command left, assignments is nil and rest is argv unchanged (so the
// caller's normal handling still applies).
func stripEnvAssignments(argv []string) (assignments, rest []string) {
	if len(argv) == 0 || filepath.Base(argv[0]) != "env" {
		return nil, argv
	}
	i := 1
	for i < len(argv) && strings.Contains(argv[i], "=") {
		i++
	}
	if i >= len(argv) {
		// Only assignments, no command to run: leave argv untouched so the
		// existing missing-binary path fires instead of silently dropping it.
		return nil, argv
	}
	return argv[1:i], argv[i:]
}

// interactiveTerminalEnv builds the environment inherited by the detached
// pty-host and, in turn, by the interactive agent process it owns.
//
// AO itself may run under an agent or CI process that sets NO_COLOR for
// captured logs. That ambient preference must not leak into an interactive
// terminal. Projects can still opt out of color explicitly through RuntimeConfig
// or an `env NO_COLOR=...` argv prefix. The native PTY and its xterm clients
// support 24-bit SGR color, so advertise that capability consistently with the
// legacy tmux runtime.
func interactiveTerminalEnv(base []string, configured map[string]string, assignments []string) []string {
	env := make([]string, 0, len(base)+len(configured)+len(assignments)+2)
	appendEntry := func(entry string, explicit bool) {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			env = append(env, entry)
			return
		}
		switch key {
		case "TERM", "COLORTERM":
			return
		case "NO_COLOR":
			if !explicit {
				return
			}
		}
		env = append(env, entry)
	}

	for _, entry := range base {
		appendEntry(entry, false)
	}
	for key, value := range configured {
		appendEntry(key+"="+value, true)
	}
	for _, entry := range assignments {
		appendEntry(entry, true)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	return env
}

// hostStartGrid decides how the pty-host starts its process: at an explicit
// grid when one was given (even with StartOnAttach, since the caller already
// knows the grid its viewer shows), deferred to the first client's grid for
// StartOnAttach alone, and otherwise immediately at the default grid.
func hostStartGrid(opts HostOptions) (cols, rows uint16, deferred bool) {
	if opts.Cols > 0 && opts.Rows > 0 {
		return opts.Cols, opts.Rows, false
	}
	if opts.StartOnAttach {
		return 0, 0, true
	}
	return initialConPTYColumns, initialConPTYRows, false
}
