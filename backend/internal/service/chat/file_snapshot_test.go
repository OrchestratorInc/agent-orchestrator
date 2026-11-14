package chat_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestRollbackWithFilesRestoresTheWorktreeFromBeforeTheTurn(t *testing.T) {
	recorder := newHistoryRecorder()
	workspace := gitWorkspace(t)
	h := newGitHarness(t, recorder, workspace)
	ctx := context.Background()

	completeTurn(t, h, "first", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	writeWorkspaceFile(t, workspace, "a.txt", "first turn\n")
	second := completeTurn(t, h, "second", "provider-turn-2")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 4 })
	writeWorkspaceFile(t, workspace, "a.txt", "second turn\n")
	writeWorkspaceFile(t, workspace, "added.txt", "second turn\n")

	preview, err := h.svc.RollbackPreview(ctx, testSession, second)
	if err != nil {
		t.Fatalf("RollbackPreview: %v", err)
	}
	want := []filesnapshot.Change{
		{Path: "a.txt", Kind: filesnapshot.ChangeReverted},
		{Path: "added.txt", Kind: filesnapshot.ChangeRemoved},
	}
	if preview.Unavailable != "" || !reflect.DeepEqual(preview.Changes, want) {
		t.Fatalf("preview = %+v, want restorable %+v", preview, want)
	}

	discarded, err := h.svc.Rollback(ctx, testSession, second, preview.PlanID)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if discarded != 1 || !reflect.DeepEqual(recorder.rollbackTargets(), []string{"provider-turn-2"}) {
		t.Fatalf("discarded %d, provider rolled back %v; want 1 and provider-turn-2", discarded, recorder.rollbackTargets())
	}
	if got := readWorkspaceFile(t, workspace, "a.txt"); got != "first turn\n" {
		t.Fatalf("a.txt = %q, want the first turn's edit", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "added.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("added.txt survived the rollback: %v", err)
	}
	saved := filesnapshot.BeforeRollbackRef(testSession, second)
	if got := gitOutputIn(t, workspace, "show", saved+":added.txt"); got != "second turn" {
		t.Fatalf("added.txt under %s = %q, want the overwritten contents", saved, got)
	}
}

func TestRollbackWithFilesRefusesWhenTheFilesChangedSinceThePreview(t *testing.T) {
	recorder := newHistoryRecorder()
	workspace := gitWorkspace(t)
	h := newGitHarness(t, recorder, workspace)
	ctx := context.Background()

	turnID := completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	writeWorkspaceFile(t, workspace, "a.txt", "agent edit\n")
	preview, err := h.svc.RollbackPreview(ctx, testSession, turnID)
	if err != nil || len(preview.Changes) != 1 || preview.PlanID == "" {
		t.Fatalf("RollbackPreview = %+v, %v; want a.txt listed with a plan id", preview, err)
	}
	writeWorkspaceFile(t, workspace, "notes.txt", "written while the dialog was open\n")

	if _, err := h.svc.Rollback(ctx, testSession, turnID, preview.PlanID); !errors.Is(err, chatsvc.ErrFilesChanged) {
		t.Fatalf("Rollback = %v, want ErrFilesChanged", err)
	}
	if targets := recorder.rollbackTargets(); len(targets) != 0 {
		t.Fatalf("provider was asked to roll back %v after the files changed", targets)
	}
	if got := readWorkspaceFile(t, workspace, "notes.txt"); got != "written while the dialog was open\n" {
		t.Fatalf("notes.txt = %q, want it untouched", got)
	}
	if got := readWorkspaceFile(t, workspace, "a.txt"); got != "agent edit\n" {
		t.Fatalf("a.txt = %q, want it untouched", got)
	}
}

func TestRollbackWithFilesPutsFilesBackWhenTheProviderRefuses(t *testing.T) {
	recorder := newHistoryRecorder()
	recorder.rollbackErr = refusedError{msg: "thread is busy"}
	workspace := gitWorkspace(t)
	h := newGitHarness(t, recorder, workspace)
	ctx := context.Background()

	turnID := completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	writeWorkspaceFile(t, workspace, "a.txt", "agent edit\n")
	writeWorkspaceFile(t, workspace, "added.txt", "agent file\n")
	preview, err := h.svc.RollbackPreview(ctx, testSession, turnID)
	if err != nil || len(preview.Changes) != 2 {
		t.Fatalf("RollbackPreview = %+v, %v; want both agent files listed", preview, err)
	}

	_, err = h.svc.Rollback(ctx, testSession, turnID, preview.PlanID)
	if !errors.Is(err, chatsvc.ErrProviderRefused) {
		t.Fatalf("Rollback = %v, want ErrProviderRefused", err)
	}
	if got := readWorkspaceFile(t, workspace, "a.txt"); got != "agent edit\n" {
		t.Fatalf("a.txt = %q after a refused rollback, want the agent's edit back", got)
	}
	if got := readWorkspaceFile(t, workspace, "added.txt"); got != "agent file\n" {
		t.Fatalf("added.txt = %q after a refused rollback, want it back", got)
	}
	if snapshot := h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true }); len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d after a refused rollback, want the turn kept", len(snapshot.Messages))
	}
}

func TestRollbackWithFilesRefusesWhenFilesCannotBeRestored(t *testing.T) {
	recorder := newHistoryRecorder()
	h := newHarnessWithConversation(t, recorder)
	ctx := context.Background()

	turnID := completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })

	preview, err := h.svc.RollbackPreview(ctx, testSession, turnID)
	if err != nil || preview.Unavailable == "" || len(preview.Changes) != 0 {
		t.Fatalf("RollbackPreview = %+v, %v; want unavailable with a reason", preview, err)
	}
	if _, err := h.svc.Rollback(ctx, testSession, turnID, "plan-from-another-preview"); !errors.Is(err, chatsvc.ErrFilesNotRestorable) {
		t.Fatalf("Rollback = %v, want ErrFilesNotRestorable", err)
	}
	if targets := recorder.rollbackTargets(); len(targets) != 0 {
		t.Fatalf("provider was asked to roll back %v after files were refused", targets)
	}
	if _, err := h.svc.Rollback(ctx, testSession, turnID, ""); err != nil {
		t.Fatalf("conversation-only Rollback: %v", err)
	}
}

func TestRollbackWithoutFilesLeavesTheWorktreeAlone(t *testing.T) {
	recorder := newHistoryRecorder()
	workspace := gitWorkspace(t)
	h := newGitHarness(t, recorder, workspace)

	turnID := completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	writeWorkspaceFile(t, workspace, "a.txt", "agent edit\n")

	if _, err := h.svc.Rollback(context.Background(), testSession, turnID, ""); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := readWorkspaceFile(t, workspace, "a.txt"); got != "agent edit\n" {
		t.Fatalf("a.txt = %q, want a conversation-only rollback to leave it", got)
	}
}

func TestTurnsAreNotSnapshottedForAnAgentThatCannotRollBack(t *testing.T) {
	workspace := gitWorkspace(t)
	h := newGitHarness(t, nil, workspace)

	turnID := completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })

	if refs := gitOutputIn(t, workspace, "for-each-ref", "refs/ao/"); refs != "" {
		t.Fatalf("snapshot refs = %q, want none", refs)
	}
	if _, err := h.svc.RollbackPreview(context.Background(), testSession, "not-a-turn"); !errors.Is(err, domain.ErrNoConversationTurn) {
		t.Fatalf("RollbackPreview(unknown turn) = %v, want ErrNoConversationTurn", err)
	}
	if _, err := h.svc.Rollback(context.Background(), testSession, turnID, "plan"); !errors.Is(err, chatsvc.ErrRollbackUnsupported) {
		t.Fatalf("Rollback = %v, want ErrRollbackUnsupported", err)
	}
}

func newGitHarness(t *testing.T, conv ports.ChatConversation, workspace string) *harness {
	t.Helper()
	return newGitHarnessFor(t, conv, workspace, domain.HarnessCodex)
}

func newGitHarnessFor(t *testing.T, conv ports.ChatConversation, workspace string, agent domain.AgentHarness) *harness {
	t.Helper()
	h := newHarnessInWorkspace(t, conv, func(st *sqlite.Store) chatsvc.Store { return st }, agent, workspace)
	record, found, err := h.st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("read seeded session: found=%v, err=%v", found, err)
	}
	record.Metadata.WorkspacePath = workspace
	if err := h.st.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("record workspace: %v", err)
	}
	return h
}

func gitWorkspace(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	gitOutputIn(t, workspace, "init", "-q", "-b", "main")
	gitOutputIn(t, workspace, "config", "user.email", "test@example.com")
	gitOutputIn(t, workspace, "config", "user.name", "Test")
	writeWorkspaceFile(t, workspace, "a.txt", "base\n")
	gitOutputIn(t, workspace, "add", ".")
	gitOutputIn(t, workspace, "commit", "-q", "-m", "init")
	return workspace
}

func writeWorkspaceFile(t *testing.T, workspace, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readWorkspaceFile(t *testing.T, workspace, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(workspace, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func gitOutputIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestForksSnapshotTurnsForAnAgentThatCannotRollBack(t *testing.T) {
	chatsvc.EnableForkSnapshots(true)
	t.Cleanup(func() { chatsvc.EnableForkSnapshots(false) })
	workspace := gitWorkspace(t)
	h := newGitHarnessFor(t, nil, workspace, domain.HarnessClaudeCode)

	completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })

	if refs := gitOutputIn(t, workspace, "for-each-ref", "refs/ao/snapshots/"); refs == "" {
		t.Fatal("no snapshot was saved for the turn")
	}
}

func TestForksKeepArtifactsAtEachTurnStart(t *testing.T) {
	chatsvc.EnableForkSnapshots(true)
	var mu sync.Mutex
	var kept []domain.SessionID
	var keptFor [][]string
	chatsvc.SetTurnArtifactSnapshot(func(_ context.Context, id domain.SessionID, turnIDs []string) {
		mu.Lock()
		defer mu.Unlock()
		kept = append(kept, id)
		keptFor = append(keptFor, turnIDs)
	})
	t.Cleanup(func() {
		chatsvc.EnableForkSnapshots(false)
		chatsvc.SetTurnArtifactSnapshot(nil)
	})
	workspace := gitWorkspace(t)
	h := newGitHarnessFor(t, nil, workspace, domain.HarnessClaudeCode)
	completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	mu.Lock()
	defer mu.Unlock()
	if len(kept) == 0 || kept[0] != testSession {
		t.Fatalf("artifacts kept for %v", kept)
	}
	if len(keptFor[0]) == 0 || keptFor[0][0] == "" {
		t.Fatalf("artifacts kept under turns %v, want the turn being started", keptFor[0])
	}
}

func TestForksDontSnapshotAnAgentThatOnlyForksItsLatestTurn(t *testing.T) {
	chatsvc.EnableForkSnapshots(true)
	var mu sync.Mutex
	artifactSnapshots := 0
	chatsvc.SetTurnArtifactSnapshot(func(context.Context, domain.SessionID, []string) {
		mu.Lock()
		defer mu.Unlock()
		artifactSnapshots++
	})
	t.Cleanup(func() {
		chatsvc.EnableForkSnapshots(false)
		chatsvc.SetTurnArtifactSnapshot(nil)
	})
	workspace := gitWorkspace(t)
	h := newGitHarnessFor(t, nil, workspace, domain.HarnessCopilot)
	completeTurn(t, h, "only", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })

	if refs := gitOutputIn(t, workspace, "for-each-ref", "refs/ao/"); refs != "" {
		t.Fatalf("snapshot refs = %q, want none", refs)
	}
	mu.Lock()
	defer mu.Unlock()
	if artifactSnapshots != 0 {
		t.Fatalf("artifacts were kept %d times, want none", artifactSnapshots)
	}
}
