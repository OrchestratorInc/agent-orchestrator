//go:build !windows

package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// providerLockHeld probes a provider writer lock without waiting for it.
func providerLockHeld(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// processStart reads a live process's executable name and start time.
func processStart(ctx context.Context, pid int) (string, time.Time, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := process.CommandContext(probeCtx, "ps", "-o", "lstart=", "-o", "comm=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.Output()
	fields := strings.Fields(string(out))
	if err != nil || len(fields) < 6 {
		return "", time.Time{}, false
	}
	started, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(fields[:5], " "), time.UTC)
	if err != nil {
		return "", time.Time{}, false
	}
	return filepath.Base(strings.Join(fields[5:], " ")), started, true
}
