package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionBranchStateRoundTripsAndStreamsOnChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "branch-state")
	session, err := s.CreateSession(ctx, sampleRecord("branch-state"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.BranchState != nil {
		t.Fatalf("new session branch state = %+v, want unobserved", session.BranchState)
	}

	baseSeq, _ := s.LatestSeq(ctx)
	want := domain.SessionBranchState{Commits: 2, RemoteBranch: "origin/feat/x", Unpushed: 1}
	if ok, err := s.SetSessionBranchState(ctx, session.ID, want); err != nil || !ok {
		t.Fatalf("set branch state: ok=%v err=%v", ok, err)
	}
	got, ok, err := s.GetSession(ctx, session.ID)
	if err != nil || !ok || got.BranchState == nil || *got.BranchState != want {
		t.Fatalf("branch state = %+v ok=%v err=%v, want %+v", got.BranchState, ok, err, want)
	}
	if !got.UpdatedAt.Equal(session.UpdatedAt) {
		t.Fatalf("updatedAt moved to %v, want %v: an observed git fact is not recency", got.UpdatedAt, session.UpdatedAt)
	}
	events, err := s.EventsAfter(ctx, baseSeq, 100)
	if err != nil {
		t.Fatalf("read CDC: %v", err)
	}
	if len(events) != 1 || string(events[0].Type) != "session_updated" {
		t.Fatalf("events = %+v, want one session_updated", events)
	}

	// Writing the same facts again is not a change.
	baseSeq, _ = s.LatestSeq(ctx)
	if _, err := s.SetSessionBranchState(ctx, session.ID, want); err != nil {
		t.Fatalf("rewrite branch state: %v", err)
	}
	if events, _ := s.EventsAfter(ctx, baseSeq, 100); len(events) != 0 {
		t.Fatalf("unchanged write emitted %+v", events)
	}
}
