package chat

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMemorySideStoreEnforcesOneSidePerMainAcrossConcurrentCreates(t *testing.T) {
	ctx := context.Background()
	store := newMemorySideStore()
	_, _ = store.ClaimSideLaunch(ctx, "launch-1", time.Now())
	const workers = 32
	ids := make(chan string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			side, _, err := store.CreateSideConversation(ctx, domain.SideConversation{
				ID: string(rune('a' + n)), SessionID: "session-1", MainConversationID: "main-1",
				AppRunID: "launch-1", CreateKey: string(rune('A' + n)), Generation: "g",
			})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- side.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("created multiple sides: %q and %q", first, id)
		}
	}
	if _, err := store.CloseSideConversation(ctx, first, time.Now()); err != nil {
		t.Fatal(err)
	}
	other, created, err := store.CreateSideConversation(ctx, domain.SideConversation{ID: "replacement", SessionID: "session-1", MainConversationID: "main-1", AppRunID: "launch-1"})
	if err != nil || !created || other.ID != "replacement" {
		t.Fatalf("close then create = %#v, %v, %v", other, created, err)
	}
}

func TestMemorySideStoreRecoversInFlightTurnWithoutDurableRows(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	before := newMemorySideStore()
	_, _ = before.ClaimSideLaunch(ctx, "launch-1", now)
	side := domain.SideConversation{ID: "side-1", SessionID: "session-1", MainConversationID: "main-1", AppRunID: "launch-1", ProviderHostID: "btw-side-1", ProviderForkID: "provider-1", Generation: "generation-1", State: "ready"}
	_, _, _ = before.CreateSideConversation(ctx, side)
	_, _, _ = before.ReserveSideTurn(ctx, domain.SideTurn{ID: "turn-1", SideID: side.ID, ClientMessageID: "client-1", Text: "question", CreatedAt: now}, "launch-1")
	_, _, _, _ = before.ClaimNextSideTurn(ctx, "launch-1", now)
	state := before.export("launch-1")
	after := newMemorySideStore()
	_, _ = after.ClaimSideLaunch(ctx, "launch-1", now)
	recovered, err := after.recover("launch-1", state, now)
	if err != nil || len(recovered) != 1 {
		t.Fatalf("recover = %#v, %v", recovered, err)
	}
	turns, _, err := after.SideTurns(ctx, side.ID, time.Time{}, 10)
	if err != nil || len(turns) != 1 || turns[0].State != "failed" {
		t.Fatalf("recovered turns = %#v, %v", turns, err)
	}
}

func TestMemorySideStoreKeepsInterruptedOpeningVisibleAfterRecovery(t *testing.T) {
	now := time.Now().UTC()
	store := newMemorySideStore()
	_, _ = store.ClaimSideLaunch(context.Background(), "launch-1", now)
	record := SideRecoveryRecord{
		Side:           domain.SideConversation{ID: "side-1", SessionID: "session-1", MainConversationID: "main-1", State: "opening"},
		ProviderHostID: "btw-side-1", Draft: "unfinished question",
	}
	recovered, err := store.recover("launch-1", []SideRecoveryRecord{record}, now)
	if err != nil || len(recovered) != 1 || recovered[0].State != "failed" {
		t.Fatalf("recover opening = %#v, %v", recovered, err)
	}
	draft, err := store.SideDraft(context.Background(), "side-1")
	if err != nil || draft != "unfinished question" {
		t.Fatalf("recovered draft = %q, %v", draft, err)
	}
}

func TestMemorySideStoreHasNoTurnLimitAndNewLaunchClearsSides(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := newMemorySideStore()
	_, _ = store.ClaimSideLaunch(ctx, "launch-1", now)
	side := domain.SideConversation{ID: "side-1", SessionID: "session-1", MainConversationID: "main-1", AppRunID: "launch-1", State: "ready"}
	_, _, _ = store.CreateSideConversation(ctx, side)
	for i := 0; i < 60; i++ {
		turn := domain.SideTurn{ID: string(rune(1000 + i)), SideID: side.ID, ClientMessageID: string(rune(2000 + i)), Text: "question", CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if _, created, err := store.ReserveSideTurn(ctx, turn, "launch-1"); err != nil || !created {
			t.Fatalf("turn %d: created=%v err=%v", i, created, err)
		}
	}
	turns, _, err := store.SideTurns(ctx, side.ID, time.Time{}, 100)
	if err != nil || len(turns) != 60 {
		t.Fatalf("turns=%d err=%v", len(turns), err)
	}
	retired, err := store.ClaimSideLaunch(ctx, "launch-2", now)
	if err != nil || len(retired) != 1 {
		t.Fatalf("new launch retired=%d err=%v", len(retired), err)
	}
	current, err := store.ListSideConversations(ctx, side.SessionID, "launch-2")
	if err != nil || len(current) != 0 {
		t.Fatalf("new launch sides=%d err=%v", len(current), err)
	}
}

func TestMemorySideStoreEmptyTurnsAreAnArray(t *testing.T) {
	store := newMemorySideStore()
	turns, more, err := store.SideTurns(context.Background(), "missing", time.Time{}, 50)
	if err != nil || more || turns == nil || len(turns) != 0 {
		t.Fatalf("empty turns = %#v, more=%v, err=%v", turns, more, err)
	}
}

func TestClosedSideRejectsLateProviderEvents(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := newMemorySideStore()
	_, _ = store.ClaimSideLaunch(ctx, "launch-1", now)
	_, _, _ = store.CreateSideConversation(ctx, domain.SideConversation{ID: "side-1", MainConversationID: "main-1", AppRunID: "launch-1", Generation: "generation-1"})
	_, _ = store.CloseSideConversation(ctx, "side-1", now)
	if err := store.UpsertSideMessage(ctx, domain.SideMessage{ID: "late", SideID: "side-1"}, "generation-1"); err != ErrSideClosed {
		t.Fatalf("late message err=%v", err)
	}
	if err := store.UpsertSideActivity("side-1", "generation-1", domain.SideActivity{ID: "late"}); err != ErrSideClosed {
		t.Fatalf("late activity err=%v", err)
	}
}
