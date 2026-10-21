//go:build !windows

package process_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func startGroup(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Reap the leader as it exits, as the chat hosts do, so a zombie does not
	// keep the group alive.
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

// TestTerminateGroupLetsTheLeaderStopWhatItStarted pins why chat agents are
// asked to exit before they are killed: Claude Code runs each command in a
// process group of its own and stops it on SIGTERM. The group signal never
// reaches that command, so only the agent's own cleanup can stop it, and a
// bare SIGKILL would leave it running.
func TestTerminateGroupLetsTheLeaderStopWhatItStarted(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "tool.pid")
	// set -m gives the background "tool" a process group of its own.
	cmd := startGroup(t, `set -m; sleep 60 & echo $! > `+pidFile+`; tool=$!; set +m
trap 'kill $tool; exit 0' TERM
while :; do sleep 0.05; done`)
	var tool int
	for range 100 {
		data, _ := os.ReadFile(pidFile)
		if tool, _ = strconv.Atoi(strings.TrimSpace(string(data))); tool > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tool == 0 {
		t.Fatal("tool did not start")
	}
	t.Cleanup(func() { _ = syscall.Kill(tool, syscall.SIGKILL) })

	if err := aoprocess.TerminateGroup(cmd.Process.Pid, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if !alive(tool) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the leader's own command outlived the stop")
}

// TestTerminateGroupKillsWhatIgnoresSIGTERM pins the fallback: a group that
// does not exit within the grace is killed.
func TestTerminateGroupKillsWhatIgnoresSIGTERM(t *testing.T) {
	cmd := startGroup(t, `trap '' TERM; sleep 60`)
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if err := aoprocess.TerminateGroup(cmd.Process.Pid, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Fatal("killed before the grace ran out")
	}
	for range 100 {
		if err := syscall.Kill(-cmd.Process.Pid, 0); err != nil && !strings.Contains(err.Error(), "not permitted") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("group still alive after SIGKILL")
}
