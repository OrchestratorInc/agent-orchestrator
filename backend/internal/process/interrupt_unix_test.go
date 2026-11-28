//go:build !windows

package process_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// A canceled child gets to run its exit handlers, e.g. removing a lock file.
func TestInterruptOnCancelLetsChildCleanUp(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "profile.lock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The trap is installed before the lock appears, so readiness implies the
	// child can already clean up.
	cmd := aoprocess.CommandContext(ctx, "/bin/sh", "-c", `trap 'rm -f "$1"; exit 0' INT; touch "$1"; while :; do sleep 0.05; done`, "sh", lock)
	aoprocess.InterruptOnCancel(cmd, 5*time.Second)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(lock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never took its lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	_ = cmd.Wait()
	reaped = true
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("interrupted child left its lock: %v", err)
	}
}
