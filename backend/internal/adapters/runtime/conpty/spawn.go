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

// hostSpawner starts a detached pty-host for the session and returns its
// loopback address ("127.0.0.1:PORT") and OS pid once it prints READY. With
// startOnAttach the host starts the process at its first client's grid.
// Injectable for tests: replace this field on Options before calling New.
type hostSpawner func(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string, startOnAttach bool) (addr string, pid int, err error)

// startOnAttachArg is the optional leading pty-host argument that defers the
// process start (see deferredPTY). Session ids never contain "=", so it cannot
// be mistaken for the positional session id; a host spawned without it, for
// example by an older daemon, starts immediately as before.
const startOnAttachArg = "--start=attach"

// ptyHostArgs builds the pty-host subcommand argv:
// pty-host [--start=attach] <sessionID> <cwd> <shellCmd> <shellArgs...>
func ptyHostArgs(sessionID, cwd string, argv []string, startOnAttach bool) []string {
	args := []string{"pty-host"}
	if startOnAttach {
		args = append(args, startOnAttachArg)
	}
	args = append(args, sessionID, cwd)
	return append(args, argv...)
}

// splitStartOnAttachArg consumes the optional leading --start=attach argument
// from the pty-host argv (everything after the subcommand name).
func splitStartOnAttachArg(args []string) (bool, []string) {
	if len(args) > 0 && args[0] == startOnAttachArg {
		return true, args[1:]
	}
	return false, args
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
