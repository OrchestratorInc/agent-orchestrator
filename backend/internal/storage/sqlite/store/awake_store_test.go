package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAwakeIntentAndAgentActivityBoundary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	initial, err := s.LoadAwake(ctx)
	if err != nil || initial != "{}" {
		t.Fatalf("initial=%s err=%v", initial, err)
	}
	if err := s.SaveAwake(ctx, `{"version":1,"paused":true}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAwake(ctx, `broken`); err == nil {
		t.Fatal("invalid durable JSON accepted")
	}
	got, err := s.LoadAwake(ctx)
	if err != nil || got != `{"version":1,"paused":true}` {
		t.Fatalf("intent lost: %s %v", got, err)
	}
	active, err := s.HasAwakeAgentActivity(ctx)
	if err != nil || active {
		t.Fatalf("empty activity=%v %v", active, err)
	}
	seedProject(t, s, "awake")
	rec := sampleRecord("awake")
	rec.Activity.State = domain.ActivityActive
	if _, err := s.CreateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	active, err = s.HasAwakeAgentActivity(ctx)
	if err != nil || !active {
		t.Fatalf("active agent missing: %v %v", active, err)
	}
}
