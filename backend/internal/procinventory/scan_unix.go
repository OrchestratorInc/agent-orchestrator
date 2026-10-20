//go:build !windows

package procinventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// defaultProbe is the canonical liveness probe (signal 0, zombies are dead).
func defaultProbe(pid int) bool {
	return processalive.Alive(pid)
}

// signalGroup delivers sig to pid's process group. Callers must have verified
// in the same snapshot that pgid == pid and that the leader's kernel start
// time matches the recorded one: a group kill on a recycled PID can take down
// an unrelated process family (issue #3475).
func signalGroup(pid int, sig Signal) error {
	err := syscall.Kill(-pid, sig.syscall())
	if errors.Is(err, syscall.ESRCH) {
		return ErrProcessGone
	}
	return err
}

// syscall maps the platform-neutral signal onto the OS signal.
func (s Signal) syscall() syscall.Signal {
	if s == SigKill {
		return syscall.SIGKILL
	}
	return syscall.SIGTERM
}

// SystemScan snapshots the full process table with parent, process group,
// resident memory, and kernel start time. -ww is required: agent command
// lines are long and ps truncates them otherwise. LC_ALL=C pins the lstart
// column to the fixed five-token C-locale format ("Mon Sep 14 00:13:51
// 2026") — under a user locale it localizes ("понедельник, 14 сентября ...")
// and the fixed-token parse would mis-slice every row.
func SystemScan(ctx context.Context) ([]Entry, error) {
	cmd := aoprocess.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,ppid=,pgid=,rss=,lstart=,args=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("process scan: %w", err)
	}
	return ParseTable(string(out))
}
