package ports_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type budgetTestKey struct{}

// A spawn whose budget runs out reads ctx.Err() to tell a timeout
// (SPAWN_TIMEOUT, 503) from a cancellation; so do the contexts it derives.
func TestSpawnRequestContextStopsWithDeadlineExceeded(t *testing.T) {
	ctx, timedOut, release := ports.NewSpawnRequestContext(context.Background(), 20*time.Millisecond)
	defer release()
	derived := map[string]context.Context{"request": ctx}
	var cancelTimeout, cancelCancel context.CancelFunc
	derived["WithTimeout"], cancelTimeout = context.WithTimeout(ctx, time.Hour)
	defer cancelTimeout()
	derived["WithCancel"], cancelCancel = context.WithCancel(ctx)
	defer cancelCancel()
	derived["WithValue"] = context.WithValue(ctx, budgetTestKey{}, "v")
	for name, c := range derived {
		select {
		case <-c.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("%s context never stopped", name)
		}
		if err := c.Err(); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s context Err() = %v, want context.DeadlineExceeded", name, err)
		}
	}
	if !timedOut() {
		t.Fatal("timedOut() = false after the budget ran out")
	}
}

func TestSpawnRequestContextExtendsFromNow(t *testing.T) {
	ctx, timedOut, release := ports.NewSpawnRequestContext(context.Background(), 30*time.Millisecond)
	defer release()
	ctx, cancel := ports.ExtendSpawnRequestBudget(ctx, 200*time.Millisecond)
	defer cancel()
	time.Sleep(80 * time.Millisecond)
	if err := ctx.Err(); err != nil || timedOut() {
		t.Fatalf("extended context Err() = %v timedOut = %v, want live", err, timedOut())
	}
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("extended context Err() = %v, want context.DeadlineExceeded", ctx.Err())
	}
}

// The client going away is a cancellation, not a timeout.
func TestSpawnRequestContextFollowsTheRequestsCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, timedOut, release := ports.NewSpawnRequestContext(parent, time.Hour)
	defer release()
	derived, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	cancelParent()
	select {
	case <-derived.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("derived context never stopped after the request was cancelled")
	}
	if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(derived.Err(), context.Canceled) || timedOut() {
		t.Fatalf("Err() = %v derived = %v timedOut = %v, want context.Canceled and no timeout", ctx.Err(), derived.Err(), timedOut())
	}
}
