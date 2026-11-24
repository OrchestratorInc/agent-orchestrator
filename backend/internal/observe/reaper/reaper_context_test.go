package reaper

import (
	"context"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type contextRecordingSessions struct{ cancellable []bool }

func (s *contextRecordingSessions) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	s.cancellable = append(s.cancellable, ctx.Done() != nil)
	return nil, nil
}

type contextRecordingReviewers struct{ cancellable []bool }

func (r *contextRecordingReviewers) ListObservableReviewers(ctx context.Context) ([]ports.ReviewerTarget, error) {
	r.cancellable = append(r.cancellable, ctx.Done() != nil)
	return nil, nil
}

func (r *contextRecordingReviewers) ProbeReviewer(context.Context, ports.ReviewerTarget) (bool, error) {
	return false, nil
}

func (r *contextRecordingReviewers) ApplyReviewerObservations(context.Context, ports.ReviewerObservationPass) error {
	return nil
}

// A cancellable context makes the SQLite driver and database/sql start two
// watcher goroutines per query; the reaper's listings run every tick and must
// not pay that.
func TestTickListsWithoutCancellableContext(t *testing.T) {
	sessions := &contextRecordingSessions{}
	reviewers := &contextRecordingReviewers{}
	r := New(&fakeLCM{}, sessions, fakeRuntime{}, Config{Logger: quietLogger()})
	r.BindReviewers(reviewers)
	tickCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Tick(tickCtx); err != nil {
		t.Fatal(err)
	}
	if len(sessions.cancellable) != 1 || sessions.cancellable[0] {
		t.Fatalf("sessions listed with a cancellable context: %v", sessions.cancellable)
	}
	if len(reviewers.cancellable) != 1 || reviewers.cancellable[0] {
		t.Fatalf("reviewers listed with a cancellable context: %v", reviewers.cancellable)
	}
}
