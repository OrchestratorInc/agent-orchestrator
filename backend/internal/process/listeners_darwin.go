package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ListenersWithEnv returns the TCP listeners of processes whose environment
// holds name=value, leaving out processes running skipExecutable.
func ListenersWithEnv(ctx context.Context, name, value, skipExecutable string) ([]Listener, error) {
	out, err := CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && len(out) == 0) {
		return nil, err
	}
	want := []byte(name + "=" + value)
	matches := map[int]bool{}
	return uniqueByPort(parseLsofListeners(string(out), func(pid int) bool {
		match, seen := matches[pid]
		if !seen {
			exe, environ := processEnviron(pid)
			match = hasEnv(environ, want) && !sameExecutable(exe, skipExecutable)
			matches[pid] = match
		}
		return match
	})), nil
}

func parseLsofListeners(out string, include func(pid int) bool) []Listener {
	var listeners []Listener
	pid, command := 0, ""
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
			command = ""
		case 'c':
			command = line[1:]
		case 'n':
			cut := strings.LastIndexByte(line, ':')
			port, err := strconv.Atoi(line[cut+1:])
			if cut < 1 || err != nil || !reachableFromLoopback(line[1:cut]) || !include(pid) {
				continue
			}
			listeners = append(listeners, Listener{Port: port, PID: pid, Command: command})
		}
	}
	return listeners
}

// processEnviron reads kern.procargs2: a native-endian int32 argc, the
// executable path, NUL padding, argc NUL-terminated arguments, then the
// NUL-terminated environment.
func processEnviron(pid int) (string, []byte) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return "", nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return "", nil
	}
	exe := string(rest[:end])
	rest = bytes.TrimLeft(rest[end:], "\x00")
	for range argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return exe, nil
		}
		rest = rest[end+1:]
	}
	return exe, rest
}
