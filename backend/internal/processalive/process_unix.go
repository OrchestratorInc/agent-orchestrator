//go:build !windows

package processalive

import (
	"bytes"
	"errors"
	"strconv"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

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

func Exited(pid int) bool {
	if pid <= 0 {
		return false
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
