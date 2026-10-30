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

// processRunsPi reports whether the live process pid is a Pi writer: its own
// executable, or the node/bun interpreter whose argv runs the pi CLI or pi-acp.
// It reads the command line with the same kind of bounded ps probe processStart
// uses, then matches with identifiesPi.
func processRunsPi(ctx context.Context, pid int) bool {
	if pid <= 0 {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := process.CommandContext(probeCtx, "ps", "-o", "comm=", "-o", "args=", "-p", strconv.Itoa(pid))
	// The C locale keeps ps from translating or reformatting the output.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return identifiesPi(string(out))
}

// processStart reads a live process's executable name and start time.
func processStart(ctx context.Context, pid int) (string, time.Time, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := process.CommandContext(probeCtx, "ps", "-o", "lstart=", "-o", "comm=", "-p", strconv.Itoa(pid))
	// Claude records the start in this format; the C locale keeps ps from translating it.
	cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
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
