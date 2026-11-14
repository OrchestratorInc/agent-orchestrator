package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionMovedToRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mov")
	r, err := s.CreateSession(ctx, sampleRecord("mov"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.GetSession(ctx, r.ID); got.MovedTo != nil {
		t.Fatalf("new session MovedTo = %+v, want nil", got.MovedTo)
	}
	move := domain.SessionMove{
		MoveID: "m-1", OrgID: "org-1", SessionID: "app-7", At: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
	}
	if ok, err := s.SetSessionMovedTo(ctx, r.ID, move, move.At); err != nil || !ok {
		t.Fatalf("set: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetSession(ctx, r.ID)
	if err != nil || got.MovedTo == nil || *got.MovedTo != move {
		t.Fatalf("GetSession MovedTo = %+v, err %v; want %+v", got.MovedTo, err, move)
	}
	all, err := s.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range all {
		if rec.ID == r.ID && (rec.MovedTo == nil || rec.MovedTo.SessionID != "app-7") {
			t.Fatalf("ListAllSessions MovedTo = %+v", rec.MovedTo)
		}
	}
	if ok, _ := s.SetSessionMovedTo(ctx, "missing", move, move.At); ok {
		t.Fatal("unknown session reported as updated")
	}
}
