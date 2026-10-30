package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ListTerminalCleanupCandidates mirrors the SQLite scan: terminated sessions
// with no facts, stale facts, or a pending cleanup that is due.
func (f *fakeStore) ListTerminalCleanupCandidates(_ context.Context, now time.Time) ([]domain.SessionID, error) {
	f.cleanupMu.Lock()
	defer f.cleanupMu.Unlock()
	var ids []domain.SessionID
	for id, rec := range f.sessions {
		if !rec.IsTerminated {
			continue
		}
		facts, ok := f.cleanupFacts[id]
		if !ok || facts.SessionGeneration < rec.CleanupGeneration ||
			(facts.WorkspaceDisposition == domain.DispositionPending && !facts.NextAttemptAt.After(now)) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// batchCleanupFactsStore adds the SQLite store's one-query facts read, so
// Cleanup's batched path is exercised as well as its per-session fallback.
type batchCleanupFactsStore struct {
	*fakeStore
	batchReads int
}

func (b *batchCleanupFactsStore) ListSessionCleanupFactsForSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionCleanupRecord, error) {
	b.cleanupMu.Lock()
	defer b.cleanupMu.Unlock()
	b.batchReads++
	out := map[domain.SessionID]domain.SessionCleanupRecord{}
	for _, id := range ids {
		if facts, ok := b.cleanupFacts[id]; ok {
			out[id] = facts
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteTerminatedSession(_ context.Context, id domain.SessionID) (bool, error) {
	if f.deleteErr != nil {
		return false, f.deleteErr
	}
	rec, ok := f.sessions[id]
	if !ok || !rec.IsTerminated {
		return false, nil
	}
	delete(f.sessions, id)
	return true, nil
}

func cleanupFacts(t *testing.T, st *fakeStore, id domain.SessionID) (domain.SessionCleanupRecord, bool) {
	t.Helper()
	facts, ok, err := st.GetSessionCleanupFacts(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return facts, ok
}

func TestCleanup_RecordsReleaseAndSkipsItOnTheNextRun(t *testing.T) {
	for _, reclaim := range []ports.WorkspaceReclaim{ports.WorkspaceReclaimRemoved, ports.WorkspaceReclaimAlreadyAbsent} {
		t.Run(string(reclaim), func(t *testing.T) {
			m, st, _, ws := newManager()
			ws.destroyReclaim = reclaim
			seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})

			first, err := m.Cleanup(ctx, "mer")
			if err != nil || len(first.Cleaned)+len(first.AlreadyGone) != 1 {
				t.Fatalf("first run = %+v, %v; want mer-1 released", first, err)
			}
			if facts, ok := cleanupFacts(t, st, "mer-1"); !ok || facts.WorkspaceDisposition != domain.DispositionRemoved {
				t.Fatalf("facts = %+v (found %v), want removed", facts, ok)
			}

			second, err := m.Cleanup(ctx, "mer")
			if err != nil || len(second.Cleaned)+len(second.AlreadyGone)+len(second.Skipped) != 0 {
				t.Fatalf("second run = %+v, %v; want nothing left to do", second, err)
			}
			if ws.destroyed != 1 {
				t.Fatalf("destroyed = %d, want 1", ws.destroyed)
			}
		})
	}
}

// With a store that batches facts, a run reads every session's facts once
// instead of once per session.
func TestCleanup_BatchesSettledFactsRead(t *testing.T) {
	_, st, rt, ws := newManager()
	batch := &batchCleanupFactsStore{fakeStore: st}
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: batch, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	seedTerminal(st, "mer-2", domain.SessionMetadata{WorkspacePath: "/ws/mer-2"})
	st.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{
		"mer-1": {SessionID: "mer-1", WorkspaceDisposition: domain.DispositionRemoved},
	}

	res, err := m.Cleanup(ctx, "mer")
	if err != nil || len(res.Cleaned) != 1 || res.Cleaned[0] != "mer-2" {
		t.Fatalf("result = %+v, %v; want only mer-2 reclaimed", res, err)
	}
	if batch.batchReads != 1 || ws.destroyed != 1 {
		t.Fatalf("batch reads = %d, destroyed = %d; want 1 and 1", batch.batchReads, ws.destroyed)
	}
}

// An untracked failure stays report-only: recording it would surface an old
// preserved worktree as a cleanup error in the app.
func TestCleanup_DoesNotRecordUntrackedFailure(t *testing.T) {
	m, st, _, ws := newManager()
	ws.destroyErr = ports.ErrWorkspaceDirty
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})

	res, err := m.Cleanup(ctx, "mer")
	if err != nil || len(res.Skipped) != 1 || res.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("result = %+v, %v; want a dirty skip", res, err)
	}
	if facts, ok := cleanupFacts(t, st, "mer-1"); ok {
		t.Fatalf("facts = %+v, want none for an untracked failure", facts)
	}
	if _, err := m.Cleanup(ctx, "mer"); err != nil || ws.destroyed != 2 {
		t.Fatalf("a skipped workspace must be retried: destroyed=%d err=%v", ws.destroyed, err)
	}
}

// A restore bumps the cleanup generation, so a release recorded for the old
// generation does not hide the restored session's new workspace.
func TestCleanup_RevisitsSessionRestoredAfterRelease(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	rec := st.sessions["mer-1"]
	rec.CleanupGeneration = 1
	st.sessions["mer-1"] = rec
	st.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{
		"mer-1": {SessionID: "mer-1", SessionGeneration: 0, WorkspaceDisposition: domain.DispositionRemoved},
	}

	res, err := m.Cleanup(ctx, "mer")
	if err != nil || len(res.Cleaned) != 1 || ws.destroyed != 1 {
		t.Fatalf("result = %+v, destroyed = %d, %v; want the new workspace reclaimed", res, ws.destroyed, err)
	}
	if facts, _ := cleanupFacts(t, st, "mer-1"); facts.SessionGeneration != 1 || facts.WorkspaceDisposition != domain.DispositionRemoved {
		t.Fatalf("facts = %+v, want removed for generation 1", facts)
	}
}

func TestCleanup_RecordsSessionWithoutWorkspaceAsNotApplicable(t *testing.T) {
	m, st, _, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{})

	if _, err := m.Cleanup(ctx, "mer"); err != nil {
		t.Fatal(err)
	}
	if facts, ok := cleanupFacts(t, st, "mer-1"); !ok || facts.WorkspaceDisposition != domain.DispositionNotApplicable {
		t.Fatalf("facts = %+v (found %v), want not_applicable", facts, ok)
	}
}

func TestKill_RecordsReleasedWorkspace(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")

	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if facts, ok := cleanupFacts(t, st, "mer-1"); !ok || facts.WorkspaceDisposition != domain.DispositionRemoved {
		t.Fatalf("facts = %+v (found %v), want removed", facts, ok)
	}
	res, err := m.Cleanup(ctx, "mer")
	if err != nil || len(res.Cleaned)+len(res.AlreadyGone)+len(res.Skipped) != 0 {
		t.Fatalf("cleanup after kill = %+v, %v; want nothing left to do", res, err)
	}
}

func TestKill_PreservedWorkspaceIsNotRecordedReleased(t *testing.T) {
	m, st, _, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	ws.destroyErr = ports.ErrWorkspaceDirty

	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if facts, ok := cleanupFacts(t, st, "mer-1"); ok {
		t.Fatalf("facts = %+v, want none for a preserved workspace", facts)
	}
}

func TestCleanupSkipReasonNamesLockedWorktree(t *testing.T) {
	err := fmt.Errorf("gitworktree: refusing to remove %q: %w", "/ws/mer-1", ports.ErrWorkspaceLocked)
	if got := cleanupSkipReason(err); got != "worktree is locked by git; run `git worktree unlock` on it, then retry cleanup" {
		t.Fatalf("reason = %q", got)
	}
}

func TestReconcileWorkspaceCleanup_FinishesAcceptedCleanup(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	st.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{
		"mer-1": {SessionID: "mer-1", WorkspaceDisposition: domain.DispositionPending, AttemptCount: 1},
	}

	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 1 {
		t.Fatalf("destroyed = %d, want the accepted cleanup finished", ws.destroyed)
	}
	if facts, _ := cleanupFacts(t, st, "mer-1"); facts.WorkspaceDisposition != domain.DispositionRemoved || facts.AttemptCount != 2 {
		t.Fatalf("facts = %+v, want removed after a second attempt", facts)
	}
}

// The sweep never removes a worktree that is still on disk unless the user
// already asked for it.
func TestReconcileWorkspaceCleanup_LeavesUnacceptedWorktreeOnDisk(t *testing.T) {
	m, st, _, ws := newManager()
	path := t.TempDir()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: path})

	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 0 {
		t.Fatalf("destroyed = %d, want 0", ws.destroyed)
	}
	if facts, ok := cleanupFacts(t, st, "mer-1"); ok {
		t.Fatalf("facts = %+v, want none", facts)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree touched: %v", err)
	}
}

// A worktree already gone from disk still goes through teardown, which prunes
// git's stale registration before the release is recorded.
func TestReconcileWorkspaceCleanup_PrunesAndRecordsWorkspaceAlreadyGone(t *testing.T) {
	m, st, _, ws := newManager()
	ws.destroyReclaim = ports.WorkspaceReclaimAlreadyAbsent
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "gone")})
	seedTerminal(st, "mer-2", domain.SessionMetadata{})

	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 1 {
		t.Fatalf("destroyed = %d, want one teardown for the missing worktree", ws.destroyed)
	}
	for id, want := range map[domain.SessionID]domain.WorkspaceDisposition{
		"mer-1": domain.DispositionRemoved,
		"mer-2": domain.DispositionNotApplicable,
	} {
		if facts, ok := cleanupFacts(t, st, id); !ok || facts.WorkspaceDisposition != want {
			t.Fatalf("%s facts = %+v (found %v), want %s", id, facts, ok, want)
		}
	}
	if ids, _ := st.ListTerminalCleanupCandidates(ctx, time.Now()); len(ids) != 0 {
		t.Fatalf("candidates after reconcile = %v, want none", ids)
	}
}

// A registration git still refuses to drop (a locked one) is not recorded, so
// `ao session cleanup` keeps reporting it.
func TestReconcileWorkspaceCleanup_LeavesRefusedRegistrationForCleanup(t *testing.T) {
	m, st, _, ws := newManager()
	ws.destroyErr = ports.ErrWorkspaceLocked
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "gone")})

	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if facts, ok := cleanupFacts(t, st, "mer-1"); ok {
		t.Fatalf("facts = %+v, want none while git refuses the registration", facts)
	}
	res, err := m.Cleanup(ctx, "mer")
	if err != nil || len(res.Skipped) != 1 || res.Skipped[0].Reason != cleanupSkipReason(ports.ErrWorkspaceLocked) {
		t.Fatalf("cleanup = %+v, %v; want the locked registration reported", res, err)
	}
}

func TestReconcileWorkspaceCleanup_LeavesRestoreMarkersAndBusySessions(t *testing.T) {
	m, st, _, _ := newManager()
	gone := filepath.Join(t.TempDir(), "gone")
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: gone})
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName}}
	seedTerminal(st, "mer-2", domain.SessionMetadata{WorkspacePath: gone})
	if err := m.beginAgentOperation(ctx, "mer-2", agentOperationRestore); err != nil {
		t.Fatal(err)
	}
	defer m.endAgentOperation("mer-2", agentOperationRestore)

	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.SessionID{"mer-1", "mer-2"} {
		if facts, ok := cleanupFacts(t, st, id); ok {
			t.Fatalf("%s facts = %+v, want untouched", id, facts)
		}
	}
}

func TestDeleteSession(t *testing.T) {
	released := domain.SessionCleanupRecord{WorkspaceDisposition: domain.DispositionRemoved}
	for _, tc := range []struct {
		name    string
		rec     domain.SessionRecord
		facts   *domain.SessionCleanupRecord
		want    error
		deleted bool
	}{
		{name: "missing", want: ErrNotFound},
		{name: "live", rec: mkLive("mer-1"), want: ErrSessionNotTerminated},
		{name: "workspace not cleaned", rec: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true,
			Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1"}}, want: ErrWorkspaceNotReleased},
		{name: "released for an older generation", rec: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true, CleanupGeneration: 1,
			Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1"}}, facts: &released, want: ErrWorkspaceNotReleased},
		{name: "released", rec: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true,
			Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1"}}, facts: &released, deleted: true},
		{name: "never had a workspace", rec: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true}, deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			if tc.rec.ID != "" {
				st.sessions[tc.rec.ID] = tc.rec
			}
			if tc.facts != nil {
				facts := *tc.facts
				facts.SessionID = "mer-1"
				st.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{"mer-1": facts}
			}
			err := m.DeleteSession(ctx, "mer-1")
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("DeleteSession = %v, want %v", err, tc.want)
			}
			if _, exists := st.sessions["mer-1"]; tc.rec.ID != "" && exists == tc.deleted {
				t.Fatalf("session exists = %v, want deleted = %v", exists, tc.deleted)
			}
		})
	}
}

func TestDeleteSession_RefusesWhileAnotherOperationRuns(t *testing.T) {
	m, st, _, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{})
	if err := m.beginAgentOperation(ctx, "mer-1", agentOperationRestore); err != nil {
		t.Fatal(err)
	}
	defer m.endAgentOperation("mer-1", agentOperationRestore)
	if err := m.DeleteSession(ctx, "mer-1"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("DeleteSession = %v, want ErrOperationInProgress", err)
	}
	if _, ok := st.sessions["mer-1"]; !ok {
		t.Fatal("session deleted while a restore held it")
	}
}

func TestReconcileWorkspaceCleanup_LeavesPathSharedWithLiveSuccessor(t *testing.T) {
	m, st, _, _ := newManager()
	shared := filepath.Join(t.TempDir(), "gone")
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: shared})
	live := mkLive("mer-2")
	live.Metadata.WorkspacePath = shared
	st.sessions["mer-2"] = live

	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if facts, ok := cleanupFacts(t, st, "mer-1"); ok {
		t.Fatalf("facts = %+v, want untouched while a live session shares the path", facts)
	}
}
