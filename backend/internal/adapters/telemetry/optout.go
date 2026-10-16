package telemetry

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// OptOutSink drops events while remote export is suppressed. It sits in
// front of the aggregator so opted-out activity never enters a rollup bucket
// that a later opt-in would flush to the remote.
type OptOutSink struct {
	next       ports.EventSink
	suppressed func() bool
}

// NewOptOutSink wraps next, dropping events while suppressed reports true (opted
// out, or an identity that could not be persisted).
func NewOptOutSink(next ports.EventSink, suppressed func() bool) ports.EventSink {
	if next == nil || suppressed == nil {
		return next
	}
	return &OptOutSink{next: next, suppressed: suppressed}
}

// Emit forwards the event unless telemetry is currently opted out.
func (s *OptOutSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	if s.suppressed() {
		return
	}
	s.next.Emit(ctx, ev)
}

// Close delegates so the wrapped sink still flushes on shutdown.
func (s *OptOutSink) Close(ctx context.Context) error { return s.next.Close(ctx) }
