package reaper

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

type bootRecordingReviewers struct {
	panicNext bool
	boots     []bool
}

func (r *bootRecordingReviewers) ListObservableReviewers(context.Context) ([]ports.ReviewerTarget, error) {
	if r.panicNext {
		r.panicNext = false
		panic("listing exploded")
	}
	return nil, nil
}

func (r *bootRecordingReviewers) ProbeReviewer(context.Context, ports.ReviewerTarget) (bool, error) {
	return false, nil
}

func (r *bootRecordingReviewers) ApplyReviewerObservations(_ context.Context, pass ports.ReviewerObservationPass) error {
	r.boots = append(r.boots, pass.Boot)
	return nil
}

// The run loop recovers a panicking tick and keeps going, so a boot pass cut
// short by a panic must still be retried as the boot pass next time.
func TestReviewerBootPassRetriesAfterPanic(t *testing.T) {
	reviewers := &bootRecordingReviewers{panicNext: true}
	r := New(&fakeLCM{}, &contextRecordingSessions{}, fakeRuntime{}, Config{Logger: quietLogger()})
	r.BindReviewers(reviewers)
	func() {
		defer func() { _ = recover() }()
		_ = r.observeReviewers(context.Background(), time.Now())
	}()
	if err := r.observeReviewers(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(reviewers.boots) != 1 || !reviewers.boots[0] {
		t.Fatalf("pass after the panic boots = %v, want one boot pass", reviewers.boots)
	}
}
