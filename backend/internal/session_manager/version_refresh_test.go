package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type staleBinaryFakeStore struct {
	*fakeStore
	marked     []string
	idleBefore time.Time
	candidates []domain.SessionID
}

func (s *staleBinaryFakeStore) MarkHarnessSessionsStale(_ context.Context, harness domain.AgentHarness, version string, _ time.Time) (int64, error) {
	s.marked = append(s.marked, string(harness)+"@"+version)
	return 1, nil
}

func (s *staleBinaryFakeStore) ListVersionRefreshChatCandidates(_ context.Context, idleBefore time.Time) ([]domain.SessionID, error) {
	s.idleBefore = idleBefore
	return s.candidates, nil
}

func (s *staleBinaryFakeStore) CountStaleBinarySessions(context.Context) (map[domain.AgentHarness]int64, error) {
	return nil, nil
}

type recordingRefresher struct {
	*recordingLauncher
	calls []domain.SessionID
	wake  []bool
}

func (r *recordingRefresher) SwitchToInstalledRelease(_ context.Context, id domain.SessionID, _ time.Duration, wake bool) (bool, error) {
	r.calls = append(r.calls, id)
	r.wake = append(r.wake, wake)
	return true, nil
}

func TestMarkHarnessUpdatedMarksThroughTheStore(t *testing.T) {
	mgr, store, _ := newChatManager(&recordingLauncher{})
	stale := &staleBinaryFakeStore{fakeStore: store}
	mgr.store = stale
	if err := mgr.MarkHarnessUpdated(context.Background(), domain.HarnessCodex, "2.0.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(stale.marked) != 1 || stale.marked[0] != "codex@2.0.0" {
		t.Fatalf("marked = %v", stale.marked)
	}
}

func TestRefreshStaleChatsSkipsSessionsInAnotherOperation(t *testing.T) {
	refresher := &recordingRefresher{recordingLauncher: &recordingLauncher{live: true}}
	mgr, store, _ := newChatManager(refresher)
	seedChatResumeSession(store, domain.ActivityIdle)
	busy := store.sessions["mer-1"]
	busy.ID = "busy"
	store.sessions[busy.ID] = busy
	stale := &staleBinaryFakeStore{fakeStore: store, candidates: []domain.SessionID{"mer-1", "busy"}}
	mgr.store = stale
	ctx := context.Background()
	if err := mgr.beginAgentOperation(ctx, "busy", agentOperationResume); err != nil {
		t.Fatal(err)
	}
	defer mgr.endAgentOperation("busy", agentOperationResume)

	before := time.Now().UTC()
	if err := mgr.RefreshStaleChats(ctx, 10*time.Minute, true); err != nil {
		t.Fatal(err)
	}
	if len(refresher.calls) != 1 || refresher.calls[0] != "mer-1" || !refresher.wake[0] {
		t.Fatalf("switched %v (wake %v), want only mer-1 with wake", refresher.calls, refresher.wake)
	}
	if want := before.Add(-10 * time.Minute); stale.idleBefore.Before(want.Add(-time.Second)) || stale.idleBefore.After(want.Add(time.Second)) {
		t.Fatalf("idle cutoff = %s, want about %s", stale.idleBefore, want)
	}
}
