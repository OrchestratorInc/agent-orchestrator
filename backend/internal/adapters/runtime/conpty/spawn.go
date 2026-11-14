package conpty

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type HostOptions struct {
	StartOnAttach bool
	LazySurface   bool
	Cols, Rows    uint16
}

type hostSpawner func(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string, opts HostOptions) (addr string, pid int, err error)

const (
	startOnAttachArg = "--start=attach"
	lazySurfaceArg   = "--surface=lazy"
	sizeArgPrefix    = "--size="
)

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

func cleanupStartedHostFailure(pid int, cause error, kill func() error) (int, error) {
	if killErr := kill(); killErr != nil {
		return pid, errors.Join(cause, fmt.Errorf("kill started pty-host pid %d: %w", pid, killErr))
	}
	return 0, cause
}

func stripEnvAssignments(argv []string) (assignments, rest []string) {
	if len(argv) == 0 || filepath.Base(argv[0]) != "env" {
		return nil, argv
	}
	i := 1
	for i < len(argv) && strings.Contains(argv[i], "=") {
		i++
	}
	if i >= len(argv) {
		return nil, argv
	}
	return argv[1:i], argv[i:]
}

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

func hostStartGrid(opts HostOptions) (cols, rows uint16, deferred bool) {
	if opts.Cols > 0 && opts.Rows > 0 {
		return opts.Cols, opts.Rows, false
	}
	if opts.StartOnAttach {
		return 0, 0, true
	}
	return initialConPTYColumns, initialConPTYRows, false
}
