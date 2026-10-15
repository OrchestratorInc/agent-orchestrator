//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestImportOwnedElsewhereUsesProviderOwnership(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	codex := domain.SessionRecord{Harness: domain.HarnessCodex}
	codex.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "01a14159-3977-7392-a3ae-2686f1c89850", ConfigDir: home}
	lock := filepath.Join(home, "thread-writer-locks", codex.Metadata.ImportSource.NativeID+".lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if importOwnedElsewhere(ctx, codex) {
		t.Fatal("an unheld Codex writer lock is ownership")
	}
	holder, err := os.Open(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if !importOwnedElsewhere(ctx, codex) {
		t.Fatal("a held Codex writer lock was not detected")
	}
	_ = holder.Close()
	if importOwnedElsewhere(ctx, codex) {
		t.Fatal("a released Codex writer lock is still ownership")
	}

	// Claude Code registers live sessions under sessions/<pid>.json.
	claudeBin := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink("/bin/sleep", claudeBin); err != nil {
		t.Fatal(err)
	}
	owner := exec.Command(claudeBin, "30")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Process.Kill(); _ = owner.Wait() })
	claude := domain.SessionRecord{Harness: domain.HarnessClaudeCode}
	claude.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "7999f45f-eb7e-43b7-90da-be3d5fd2208d", ConfigDir: home}
	register := func(pid int, cwd string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": claude.Metadata.ImportSource.NativeID, "cwd": cwd, "kind": "interactive"})
		if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "sessions", strconv.Itoa(pid)+".json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	register(os.Getpid(), "/source") // alive, but not a Claude process
	if importOwnedElsewhere(ctx, claude) {
		t.Fatal("a reused PID was treated as Claude")
	}
	register(owner.Process.Pid, "/source")
	if !importOwnedElsewhere(ctx, claude) {
		t.Fatal("a live Claude session was not detected")
	}
	claude.Metadata.WorkspacePath = "/ao/worktrees/mer-1"
	register(owner.Process.Pid, "/ao/worktrees/mer-1/sub")
	if importOwnedElsewhere(ctx, claude) {
		t.Fatal("AO's own earlier launch was treated as another owner")
	}
	register(owner.Process.Pid, "/source")
	_ = owner.Process.Kill()
	_ = owner.Wait()
	if importOwnedElsewhere(ctx, claude) {
		t.Fatalf("a crashed session's registry entry (pid %d) is still ownership", owner.Process.Pid)
	}
}
