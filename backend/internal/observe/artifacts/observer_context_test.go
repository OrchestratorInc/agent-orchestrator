package artifacts

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type contextRecordingSessions struct {
	rows        []domain.SessionRecord
	cancellable []bool
}

func (s *contextRecordingSessions) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	s.cancellable = append(s.cancellable, ctx.Done() != nil)
	return s.rows, nil
}

// contextRecordingSink records each reconcile's context and, if set, runs
// onReconcile first.
type contextRecordingSink struct {
	reconciled  []domain.SessionID
	cancellable []bool
	onReconcile func()
}

func (s *contextRecordingSink) ReconcileSessionOutputType(ctx context.Context, id domain.SessionID) error {
	if s.onReconcile != nil {
		s.onReconcile()
	}
	s.reconciled = append(s.reconciled, id)
	s.cancellable = append(s.cancellable, ctx.Done() != nil)
	return nil
}

type contextRecordingVersions struct{ cancellable []bool }

func (v *contextRecordingVersions) CaptureSession(ctx context.Context, _ domain.SessionRecord) error {
	v.cancellable = append(v.cancellable, ctx.Done() != nil)
	return nil
}

// A cancellable context makes the SQLite driver and database/sql start two
// watcher goroutines per query; the observer reads for every session each tick
// and must not pay that.
func TestPollReadsWithoutCancellableContext(t *testing.T) {
	sessions := &contextRecordingSessions{rows: []domain.SessionRecord{{ID: "s1"}, {ID: "s2"}}}
	sink := &contextRecordingSink{}
	versions := &contextRecordingVersions{}
	o := New(sessions, sink, versions, Config{})
	pollCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := o.Poll(pollCtx); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]bool{"listing": sessions.cancellable, "reconcile": sink.cancellable, "capture": versions.cancellable} {
		for _, cancellable := range got {
			if cancellable {
				t.Fatalf("%s ran with a cancellable context: %v", name, got)
			}
		}
	}
	if len(sink.reconciled) != 2 || len(versions.cancellable) != 2 {
		t.Fatalf("reconciled %v and captured %d sessions, want both sessions", sink.reconciled, len(versions.cancellable))
	}
}

// The reads ignore cancellation, so the poll itself stops between sessions.
func TestPollStopsBetweenSessionsOnceCancelled(t *testing.T) {
	sessions := &contextRecordingSessions{rows: []domain.SessionRecord{{ID: "s1"}, {ID: "s2"}, {ID: "s3"}}}
	pollCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &contextRecordingSink{onReconcile: cancel}
	o := New(sessions, sink, nil, Config{})
	if err := o.Poll(pollCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Poll error = %v, want context.Canceled", err)
	}
	if len(sink.reconciled) != 1 {
		t.Fatalf("reconciled %v after cancellation, want only the session in progress", sink.reconciled)
	}
}
