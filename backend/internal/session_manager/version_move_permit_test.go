package sessionmanager

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// wakeCountingRefresher starts a provider in the background on each switch,
// as Chat does, and counts how many are starting at once.
type wakeCountingRefresher struct {
	*recordingLauncher
	starting, peak atomic.Int32
	waited         atomic.Int32
}

func (r *wakeCountingRefresher) SwitchToInstalledRelease(context.Context, domain.SessionID, time.Duration) (bool, error) {
	now := r.starting.Add(1)
	for {
		old := r.peak.Load()
		if now <= old || r.peak.CompareAndSwap(old, now) {
			break
		}
	}
	return true, nil
}

func (r *wakeCountingRefresher) WaitForWake(context.Context, domain.SessionID) error {
	time.Sleep(30 * time.Millisecond)
	r.starting.Add(-1)
	r.waited.Add(1)
	return nil
}

func TestVersionMovesHoldTheirPermitUntilTheProviderStarted(t *testing.T) {
	refresher := &wakeCountingRefresher{recordingLauncher: &recordingLauncher{live: true}}
	mgr, store, _ := newChatManager(refresher)
	seedChatResumeSession(store, domain.ActivityIdle)
	base := store.sessions["mer-1"]
	ids := make([]domain.SessionID, 7)
	for i := range ids {
		rec := base
		rec.ID = domain.SessionID(fmt.Sprintf("mer-%d", i+1))
		store.sessions[rec.ID] = rec
		ids[i] = rec.ID
	}
	mgr.store = &staleBinaryFakeStore{fakeStore: store, candidates: ids}
	// A turn-end move holds one permit: sweeps share the same four.
	release, err := mgr.versionMoves.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if err := mgr.RefreshStaleChats(context.Background(), time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := refresher.waited.Load(); got != int32(len(ids)) {
		t.Fatalf("waited for %d provider starts, want %d", got, len(ids))
	}
	if peak := refresher.peak.Load(); peak > maxConcurrentVersionMoves-1 || peak < 2 {
		t.Fatalf("providers starting at once = %d, want 2..%d", peak, maxConcurrentVersionMoves-1)
	}
}

func TestVersionMovesWaitWhileEveryPermitIsHeld(t *testing.T) {
	mgr, _, _ := newChatManager(&recordingLauncher{})
	for range maxConcurrentVersionMoves {
		release, err := mgr.versionMoves.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var moved atomic.Bool
	err := mgr.forEachVersionMove(ctx, []domain.SessionID{"mer-1"}, func(context.Context, domain.SessionID) error {
		moved.Store(true)
		return nil
	})
	if err == nil || moved.Load() {
		t.Fatalf("move ran with every permit held: moved=%v err=%v", moved.Load(), err)
	}
}
