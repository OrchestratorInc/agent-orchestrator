//go:build !windows

package systemexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRunInstallCancellationKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- (Adapter{}).RunInstall(ctx, ports.InstallCommand{
			Argv: []string{"sh", "-c", "sleep 30 & echo $!; wait"},
		}, &output, &output)
	}()

	childPID := waitForChildPID(t, &output)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	cancel()
	if err := <-done; err == nil {
		t.Fatal("RunInstall error = nil, want cancellation")
	}
	assertProcessExited(t, childPID, "installer descendant")
}

func TestRunCancellationKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- (Adapter{}).Run(ctx, []string{"sh", "-c", "sleep 30 & echo $!; wait"}, &output, &output)
	}()

	childPID := waitForChildPID(t, &output)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	cancel()
	if err := <-done; err == nil {
		t.Fatal("Run error = nil, want cancellation")
	}
	assertProcessExited(t, childPID, "command descendant")
}

func waitForChildPID(t *testing.T, output *lockedBuffer) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value := strings.TrimSpace(output.String())
		if value != "" {
			pid, _ := strconv.Atoi(strings.Fields(value)[0])
			if pid != 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child PID was not reported")
	return 0
}

func assertProcessExited(t *testing.T, pid int, label string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s %d survived cancellation", label, pid)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

var _ io.Writer = (*lockedBuffer)(nil)

// An isolated installer runs in its own directory and sees only the
// environment it was given plus proxy, temp and locale settings. A variable
// it was given, such as SHELL, wins over the user's.
func TestRunInstallIsolatedKeepsUserVariablesOut(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", "/Users/me/.kimi-code")
	t.Setenv("HTTPS_PROXY", "http://proxy:8080")
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")
	dir := t.TempDir()
	var output bytes.Buffer
	err := (Adapter{}).RunInstall(context.Background(), ports.InstallCommand{
		Argv:     []string{"/bin/sh", "-c", `printf '%s|%s|%s|%s|%s' "${KIMI_CODE_HOME-unset}" "$HTTPS_PROXY" "$GIVEN" "$SHELL" "$(pwd -P)"`},
		Env:      []string{"PATH=/usr/bin:/bin", "GIVEN=yes", "SHELL=/bin/sh"},
		Dir:      dir,
		Isolated: true,
	}, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, _ := filepath.EvalSymlinks(dir)
	if want := "unset|http://proxy:8080|yes|/bin/sh|" + wantDir; output.String() != want {
		t.Fatalf("installer saw %q, want %q", output.String(), want)
	}
}
