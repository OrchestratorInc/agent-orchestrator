//go:build !windows

// Package processalive probes whether an operating-system process id still
// maps to a live process.
package processalive

import (
	"bytes"
	"errors"
	"strconv"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Alive reports whether pid maps to a running process. EPERM counts as alive:
// the process exists even if the current user cannot signal it. Zombies are
// treated as not alive because the executable has already exited; only its
// parent has not reaped the process table entry yet.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return !isZombie(pid)
}

func isZombie(pid int) bool {
	out, err := aoprocess.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return bytes.HasPrefix(bytes.TrimSpace(out), []byte("Z"))
}
