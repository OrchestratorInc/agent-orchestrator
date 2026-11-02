//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAutohandResumedConversationUsesCurrentHeartbeatForOwnership(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "autohand")
	if err := os.Symlink(self, bin); err != nil {
		t.Fatal(err)
	}
	// Only CI runs this synthetic test-binary child; no provider CLI is used.
	cmd := exec.Command(bin, "-test.run=^TestFakeClaudeOwnerProcess$")
	cmd.Env = append(os.Environ(), "AO_TEST_FAKE_CLAUDE_OWNER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForExecName(t, cmd.Process.Pid, "autohand")
	root, source, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	rec := domain.SessionRecord{Harness: domain.HarnessAutohand, Metadata: domain.SessionMetadata{WorkspacePath: workspace, ImportSource: &domain.SessionImportSource{NativeID: "resumed-conversation", ConfigDir: root}}}
	dir := filepath.Join(root, "active-agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(updated time.Time) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"sessionId": "resumed-conversation", "pid": cmd.Process.Pid, "workspaceRoot": source, "startedAt": time.Now().Add(-24 * time.Hour), "updatedAt": updated})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "resumed.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(time.Now())
	if !autohandImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("missed the current process resuming yesterday's conversation")
	}
	write(time.Now().Add(-time.Minute))
	if autohandImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("accepted a stale heartbeat")
	}
	_, started, known := processStart(context.Background(), cmd.Process.Pid)
	if !known {
		t.Fatal("synthetic process start time is unknown")
	}
	write(started.Add(-time.Second))
	if autohandImportOwnedElsewhere(context.Background(), rec) {
		t.Fatal("accepted a heartbeat predating this pid's current process")
	}
}
