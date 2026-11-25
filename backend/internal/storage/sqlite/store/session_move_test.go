package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// stoppedMovingSession is a session whose agent exited and whose move m-1 pushed it.
func stoppedMovingSession(t *testing.T, s *sqlite.Store) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, "mov")
	rec := sampleRecord("mov")
	rec.Activity.State = domain.ActivityExited
	r, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetSessionMoveStarted(ctx, r.ID, "m-1", time.Now()); err != nil || !ok {
		t.Fatalf("start move: ok=%v err=%v", ok, err)
	}
	return r
}

func TestSessionMovedToRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	r := stoppedMovingSession(t, s)
	if got, _, _ := s.GetSession(ctx, r.ID); got.MovedTo != nil {
		t.Fatalf("new session MovedTo = %+v, want nil", got.MovedTo)
	}
	move := domain.SessionMove{
		MoveID: "m-1", OrgID: "org-1", SessionID: "app-7", At: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
	}
	if ok, err := s.MarkSessionMoved(ctx, r.ID, move, move.At); err != nil || !ok {
		t.Fatalf("mark: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetSession(ctx, r.ID)
	if err != nil || got.MovedTo == nil || *got.MovedTo != move || got.MoveStarted != "" {
		t.Fatalf("GetSession MovedTo = %+v, MoveStarted %q, err %v; want %+v and no move in progress", got.MovedTo, got.MoveStarted, err, move)
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
	if ok, _ := s.MarkSessionMoved(ctx, "missing", move, move.At); ok {
		t.Fatal("unknown session reported as updated")
	}
	// Once marked moved, ending the move here is refused and changes nothing.
	if ok, err := s.EndSessionMoveStarted(ctx, r.ID, time.Now()); err != nil || ok {
		t.Fatalf("end after mark: ok=%v err=%v, want refused", ok, err)
	}
}

// A session resumed here after its move pushed it, or one moved by another
// move, or one whose agent runs, is never marked moved.
func TestMarkSessionMovedRefusesASessionThatStayedHere(t *testing.T) {
	ctx := context.Background()
	move := domain.SessionMove{MoveID: "m-1", OrgID: "org-1", SessionID: "app-7", At: time.Now().UTC()}
	for name, prepare := range map[string]func(t *testing.T, s *sqlite.Store, id domain.SessionID){
		"resumed here": func(t *testing.T, s *sqlite.Store, id domain.SessionID) {
			if ok, err := s.EndSessionMoveStarted(ctx, id, time.Now()); err != nil || !ok {
				t.Fatalf("end: ok=%v err=%v", ok, err)
			}
		},
		"another move": func(t *testing.T, s *sqlite.Store, id domain.SessionID) {
			if _, err := s.SetSessionMoveStarted(ctx, id, "m-2", time.Now()); err != nil {
				t.Fatal(err)
			}
		},
		"agent running": func(t *testing.T, s *sqlite.Store, id domain.SessionID) {
			rec, _, _ := s.GetSession(ctx, id)
			rec.Activity.State = domain.ActivityActive
			if err := s.UpdateSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			r := stoppedMovingSession(t, s)
			prepare(t, s, r.ID)
			if ok, err := s.MarkSessionMoved(ctx, r.ID, move, move.At); err != nil || ok {
				t.Fatalf("mark: ok=%v err=%v, want refused", ok, err)
			}
			if got, _, _ := s.GetSession(ctx, r.ID); got.MovedTo != nil {
				t.Fatalf("MovedTo = %+v, want nil", got.MovedTo)
			}
		})
	}
}

func TestSessionMoveStartedRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mov")
	r, err := s.CreateSession(ctx, sampleRecord("mov"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	if ok, err := s.SetSessionMoveStarted(ctx, r.ID, "m-1", now); err != nil || !ok {
		t.Fatalf("set: ok=%v err=%v", ok, err)
	}
	all, err := s.ListAllSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range all {
		if rec.ID == r.ID && rec.MoveStarted != "m-1" {
			t.Fatalf("ListAllSessions MoveStarted = %q, want m-1", rec.MoveStarted)
		}
	}
	if ok, err := s.SetSessionMoveStarted(ctx, r.ID, "", now); err != nil || !ok {
		t.Fatalf("clear: ok=%v err=%v", ok, err)
	}
	if got, _, _ := s.GetSession(ctx, r.ID); got.MoveStarted != "" {
		t.Fatalf("cleared MoveStarted = %q", got.MoveStarted)
	}
}
