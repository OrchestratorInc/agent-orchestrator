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

func TestRetryVersionMovesWaitsForSharedPermit(t *testing.T) {
	launcher := &recordingLauncher{startErr: fmt.Errorf("fixture startup failed")}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityExited)
	mgr.versionRetries.note("mer-1")
	var releases []func()
	for range maxConcurrentVersionMoves {
		release, err := mgr.versionMoves.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := mgr.RetryVersionMoves(ctx); err == nil {
		t.Fatal("retry did not report unavailable permit")
	}
	if len(launcher.started) != 0 {
		t.Fatal("retry started a provider while all four refresh permits were held")
	}
}

func TestRetryVersionMoveHoldsPermitThroughStartup(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	launcher := &recordingLauncher{beforeStart: func(ChatStart) { close(entered); <-finish }}
	mgr, store, _ := newChatManager(launcher)
	seedChatResumeSession(store, domain.ActivityExited)
	mgr.versionRetries.note("mer-1")
	done := make(chan error, 1)
	go func() { done <- mgr.RetryVersionMoves(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("retry did not start")
	}
	var releases []func()
	for range maxConcurrentVersionMoves - 1 {
		release, err := mgr.versionMoves.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	release, err := mgr.versionMoves.acquire(ctx)
	if err == nil {
		release()
		t.Error("slow retry released its permit before provider startup finished")
	}
	for _, release := range releases {
		release()
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
