package cdc_test

import (
	"context"
	"testing"

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
