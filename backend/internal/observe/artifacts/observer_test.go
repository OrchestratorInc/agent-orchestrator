package artifacts

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeSessions struct {
	rows []domain.SessionRecord
	err  error
}

func (f fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.rows, f.err
}

type fakeSink struct {
	reconciled []domain.SessionID
	err        error
}

func (f *fakeSink) ReconcileSessionOutputType(_ context.Context, id domain.SessionID) error {
	f.reconciled = append(f.reconciled, id)
	return f.err
}

func TestPoll_SkipsTerminatedButRescansArtifactSessionsForRemovals(t *testing.T) {
	sessions := fakeSessions{rows: []domain.SessionRecord{
		{ID: "live"},
		{ID: "pr-only", OutputType: domain.SessionOutputPR}, // still polled: may later gain artifact output too.
		{ID: "terminated", IsTerminated: true},
		{ID: "already-artifact", OutputType: domain.SessionOutputArtifact}, // rescanned: its last file may have been deleted.
		{ID: "already-pr-and-artifact", OutputType: domain.SessionOutputPRAndArtifact},
	}}
	sink := &fakeSink{}
	o := New(sessions, sink, Config{})

	if err := o.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []domain.SessionID{"live", "pr-only", "already-artifact", "already-pr-and-artifact"}
	if len(sink.reconciled) != len(want) {
		t.Fatalf("reconciled = %v, want %v", sink.reconciled, want)
	}
	for i := range want {
		if sink.reconciled[i] != want[i] {
			t.Fatalf("reconciled = %v, want %v", sink.reconciled, want)
		}
	}
}

func TestPoll_ReconcileFailurePerSessionDoesNotStopTheRest(t *testing.T) {
	sessions := fakeSessions{rows: []domain.SessionRecord{{ID: "a"}, {ID: "b"}}}
	sink := &fakeSink{err: errors.New("boom")}
	o := New(sessions, sink, Config{})

	if err := o.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.reconciled) != 2 {
		t.Fatalf("reconciled = %v, want both sessions attempted", sink.reconciled)
	}
}

func TestPoll_PropagatesListSessionsError(t *testing.T) {
	sessions := fakeSessions{err: errors.New("db down")}
	o := New(sessions, &fakeSink{}, Config{})

	if err := o.Poll(context.Background()); err == nil {
		t.Fatal("want error from ListAllSessions to propagate")
	}
}
