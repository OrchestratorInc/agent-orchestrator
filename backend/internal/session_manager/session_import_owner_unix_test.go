//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestFakeClaudeOwnerProcess(t *testing.T) {
	if os.Getenv("AO_TEST_FAKE_CLAUDE_OWNER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func startFakeClaude(t *testing.T) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink(self, bin); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=^TestFakeClaudeOwnerProcess$")
	cmd.Env = append(os.Environ(), "AO_TEST_FAKE_CLAUDE_OWNER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func waitForExecName(t *testing.T, pid int, name string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got, _, ok := processStart(context.Background(), pid); ok && got == name {
			if !processalive.Alive(pid) {
				t.Fatalf("process %d ran as %q but already exited", pid, name)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d never ran as %q", pid, name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

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

	owner := startFakeClaude(t)
	t.Cleanup(func() { _ = owner.Process.Kill(); _ = owner.Wait() })
	waitForExecName(t, owner.Process.Pid, "claude")
	claude := domain.SessionRecord{Harness: domain.HarnessClaudeCode}
	claude.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "7999f45f-eb7e-43b7-90da-be3d5fd2208d", ConfigDir: home}
	register := func(pid int, cwd string, startedAt ...time.Time) {
		t.Helper()
		entry := map[string]any{"pid": pid, "sessionId": claude.Metadata.ImportSource.NativeID, "cwd": cwd, "kind": "interactive"}
		if len(startedAt) > 0 {
			entry["startedAt"] = startedAt[0].UnixMilli()
		}
		body, _ := json.Marshal(entry)
		if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "sessions", strconv.Itoa(pid)+".json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	register(os.Getpid(), "/source")
	if importOwnedElsewhere(ctx, claude) {
		t.Fatal("a reused PID was treated as Claude")
	}
	register(owner.Process.Pid, "/source")
	if !importOwnedElsewhere(ctx, claude) {
		t.Fatal("a live Claude session was not detected")
	}
	register(owner.Process.Pid, "/source", time.Now().Add(-time.Hour))
	if importOwnedElsewhere(ctx, claude) {
		t.Fatal("a reused PID was treated as the registered Claude session")
	}
	register(owner.Process.Pid, "/source", time.Now())
	if !importOwnedElsewhere(ctx, claude) {
		t.Fatal("a live Claude session with its registration time was not detected")
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

func TestImportOwnedElsewhereOpenCodeNeverOwned(t *testing.T) {
	ctx := context.Background()
	oc := domain.SessionRecord{Harness: domain.HarnessOpenCode}
	oc.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "ses_e830c4a8affelmmC9j6HFbCZAr", ConfigDir: t.TempDir()}
	oc.Metadata.NativeTranscriptPath = oc.Metadata.ImportSource.NativeID
	if importOwnedElsewhere(ctx, oc) {
		t.Fatal("OpenCode import must report not-owned without positive evidence")
	}
	if adoptedOwnedElsewhere(ctx, oc) {
		t.Fatal("adopted OpenCode import must report not-owned")
	}
}

func TestAdoptedClaudeImportOpenElsewhereBlocksResume(t *testing.T) {
	home := t.TempDir()
	owner := startFakeClaude(t)
	t.Cleanup(func() { _ = owner.Process.Kill(); _ = owner.Wait() })
	waitForExecName(t, owner.Process.Pid, "claude")
	body, _ := json.Marshal(map[string]any{"pid": owner.Process.Pid, "sessionId": "thread-1", "cwd": "/source"})
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sessions", "1.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: home, Transferred: true, Prepared: true, Adopted: true}}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); !errors.Is(err, ErrSessionOpenElsewhere) || len(launcher.started) != 0 {
		t.Fatalf("resume beside a live Claude session: %v, launches %d", err, len(launcher.started))
	}
	_ = owner.Process.Kill()
	_ = owner.Wait()
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil || len(launcher.started) != 1 {
		t.Fatalf("resume after the other session closed: %v, launches %d", err, len(launcher.started))
	}
}

func TestAdoptedCodexImportOpenElsewhereSkipsLaunch(t *testing.T) {
	home := t.TempDir()
	lock := filepath.Join(home, "thread-writer-locks", "thread-1.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	holder, err := os.Create(lock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: home, Transferred: true, Prepared: true, Adopted: true}}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); !errors.Is(err, ErrSessionOpenElsewhere) || len(launcher.started) != 0 {
		t.Fatalf("resume beside a held Codex thread: %v, launches %d", err, len(launcher.started))
	}
}
