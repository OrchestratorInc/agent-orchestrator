package telemetry

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type OptOutSink struct {
	next       ports.EventSink
	suppressed func() bool
}

func NewOptOutSink(next ports.EventSink, suppressed func() bool) ports.EventSink {
	if next == nil || suppressed == nil {
		return next
	}
	return &OptOutSink{next: next, suppressed: suppressed}
}

type usageDiscarder interface {
	Discard()
}

func (s *OptOutSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	if s.suppressed() {
		if discarder, ok := s.next.(usageDiscarder); ok {
			discarder.Discard()
		}
		return
	}
	s.next.Emit(ctx, ev)
}

func (s *OptOutSink) Close(ctx context.Context) error {
	if s.suppressed() {
		if discarder, ok := s.next.(usageDiscarder); ok {
			discarder.Discard()
		}
	}
	return s.next.Close(ctx)
}
