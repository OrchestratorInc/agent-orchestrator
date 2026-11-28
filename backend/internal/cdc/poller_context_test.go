package cdc_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
)

type contextRecordingSource struct{ readCancellable []bool }

func (s *contextRecordingSource) EventsAfter(ctx context.Context, _ int64, _ int) ([]cdc.Event, error) {
	s.readCancellable = append(s.readCancellable, ctx.Done() != nil)
	return nil, nil
}

func (s *contextRecordingSource) LatestSeq(context.Context) (int64, error) { return 0, nil }

// A cancellable context makes the SQLite driver and database/sql start two
// watcher goroutines per query; the change-log poll must not pay that.
func TestPollReadsWithoutCancellableContext(t *testing.T) {
	src := &contextRecordingSource{}
	p := cdc.NewPoller(src, cdc.NewBroadcaster(), cdc.PollerConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(src.readCancellable) != 1 || src.readCancellable[0] {
		t.Fatalf("poll read with a cancellable context: %v", src.readCancellable)
	}
}

// fullBatchSource always returns a full batch, as a long backlog does.
type fullBatchSource struct{}

func (s *fullBatchSource) EventsAfter(_ context.Context, after int64, limit int) ([]cdc.Event, error) {
	evs := make([]cdc.Event, limit)
	for i := range evs {
		evs[i] = cdc.Event{Seq: after + int64(i) + 1}
	}
	return evs, nil
}

func (s *fullBatchSource) LatestSeq(context.Context) (int64, error) { return 0, nil }

// Reads ignore cancellation, so a backlog of full batches must not keep the
// loop running after its context is cancelled.
func TestStartStopsOnCancelWhileDrainingFullBatches(t *testing.T) {
	p := cdc.NewPoller(&fullBatchSource{}, cdc.NewBroadcaster(), cdc.PollerConfig{Batch: 2, Interval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the poller kept draining full batches after cancellation")
	}
}
