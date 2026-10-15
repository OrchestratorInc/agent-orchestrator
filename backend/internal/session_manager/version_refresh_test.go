package sessionmanager

import (
	"context"
	"fmt"
	"sync/atomic"
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
}

func (r *recordingRefresher) SwitchToInstalledRelease(_ context.Context, id domain.SessionID, _ time.Duration) (bool, error) {
	r.calls = append(r.calls, id)
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
	if err := mgr.RefreshStaleChats(ctx, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(refresher.calls) != 1 || refresher.calls[0] != "mer-1" {
		t.Fatalf("switched %v, want only mer-1", refresher.calls)
	}
	if want := before.Add(-10 * time.Minute); stale.idleBefore.Before(want.Add(-time.Second)) || stale.idleBefore.After(want.Add(time.Second)) {
		t.Fatalf("idle cutoff = %s, want about %s", stale.idleBefore, want)
	}
}

func TestForEachVersionMoveRunsAtMostFourAtOnce(t *testing.T) {
	mgr, _, _ := newChatManager(&recordingLauncher{})
	ids := make([]domain.SessionID, 10)
	for i := range ids {
		ids[i] = domain.SessionID(fmt.Sprintf("s%d", i))
	}
	var running, peak atomic.Int32
	err := mgr.forEachVersionMove(context.Background(), ids, func(context.Context, domain.SessionID) error {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return nil
	})
	if err != nil || peak.Load() > maxConcurrentVersionMoves || peak.Load() < 2 {
		t.Fatalf("peak = %d, err = %v; want 2..%d", peak.Load(), err, maxConcurrentVersionMoves)
	}
}

func TestRetryVersionMovesForgetsSessionsThatAreBack(t *testing.T) {
	mgr, store, _ := newChatManager(&recordingLauncher{})
	seedChatResumeSession(store, domain.ActivityIdle)
	mgr.versionRetries.note("mer-1")
	if err := mgr.RetryVersionMoves(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending := mgr.versionRetries.pending(); len(pending) != 0 {
		t.Fatalf("pending = %v; a running session needs no retry", pending)
	}
}

// launchFactsRecorder records which launch a live reconnect carries the
// running release and stale mark from.
type launchFactsRecorder struct {
	*fakeStore
	releases [][2]string
	stale    [][2]string
}

func (r *launchFactsRecorder) CarryHarnessRelease(_ context.Context, _ domain.SessionID, oldLaunch, newLaunch string) error {
	r.releases = append(r.releases, [2]string{oldLaunch, newLaunch})
	return nil
}

func (r *launchFactsRecorder) CarryStaleBinary(_ context.Context, _ domain.SessionID, oldLaunch, newLaunch string) error {
	r.stale = append(r.stale, [2]string{oldLaunch, newLaunch})
	return nil
}

func TestLiveChatReconnectCarriesFromTheGenerationBeforeTheClaim(t *testing.T) {
	launcher := &recordingLauncher{liveReconnect: true}
	m, st, _ := newChatManager(launcher)
	recorder := &launchFactsRecorder{fakeStore: st}
	m.store = recorder
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: chatTestProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Unix(100, 0).UTC()},
		Metadata: domain.SessionMetadata{Branch: "ao/mer-1/root", WorkspacePath: "/ws/mer-1",
			ProviderConversationID: "thread-1", ControllerGeneration: "gen-0"}}
	st.sessions[rec.ID] = rec
	// The chat service writes the new generation to the session before the
	// controller is reported ready.
	launcher.beforeStart = func(cfg ChatStart) {
		claimed := st.sessions[cfg.SessionID]
		claimed.Metadata.ControllerGeneration = "gen-1"
		st.sessions[cfg.SessionID] = claimed
	}
	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"gen-0", "gen-1"}}
	if fmt.Sprint(recorder.releases) != fmt.Sprint(want) || fmt.Sprint(recorder.stale) != fmt.Sprint(want) {
		t.Fatalf("carried release %v and stale mark %v, want both %v", recorder.releases, recorder.stale, want)
	}
}
