package wakeguard

import (
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestBudgetFailureCooldownBlocksRetryThenAllowsIt(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000, 0)}
	b := NewWithLimits(clock.now, time.Minute, 10*time.Minute, 10)
	id := domain.SessionID("orch-1")
	if err := b.Admit(id); err != nil {
		t.Fatalf("first admit: %v", err)
	}
	cause := errors.New("harness missing")
	b.Record(id, cause)

	clock.t = clock.t.Add(30 * time.Second)
	err := b.Admit(id)
	if !errors.Is(err, ErrRecentFailure) || !errors.Is(err, cause) {
		t.Fatalf("admit inside cooldown = %v, want ErrRecentFailure wrapping the cause", err)
	}

	clock.t = clock.t.Add(31 * time.Second)
	if err := b.Admit(id); err != nil {
		t.Fatalf("admit after cooldown: %v", err)
	}
}

func TestBudgetCapsRestartsPerWindow(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000, 0)}
	b := NewWithLimits(clock.now, time.Minute, 10*time.Minute, 2)
	id := domain.SessionID("orch-1")
	for i := range 2 {
		if err := b.Admit(id); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
		b.Record(id, nil)
		clock.t = clock.t.Add(time.Minute)
	}
	if err := b.Admit(id); !errors.Is(err, ErrTooManyRestarts) {
		t.Fatalf("third admit = %v, want ErrTooManyRestarts", err)
	}
	clock.t = clock.t.Add(10 * time.Minute)
	if err := b.Admit(id); err != nil {
		t.Fatalf("admit after the window moved on: %v", err)
	}
}

func TestBudgetIsPerSession(t *testing.T) {
	b := NewWithLimits(nil, time.Minute, time.Hour, 1)
	if err := b.Admit("a"); err != nil {
		t.Fatal(err)
	}
	if err := b.Admit("b"); err != nil {
		t.Fatalf("other session shares the budget: %v", err)
	}
}
