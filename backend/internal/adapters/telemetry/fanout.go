package telemetry

import (
	"context"
	"errors"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// FanoutSink emits each event to multiple sinks.
type FanoutSink struct {
	sinks []ports.EventSink
}

// NewFanoutSink builds a sink that forwards each event to every non-nil sink.
func NewFanoutSink(sinks ...ports.EventSink) *FanoutSink {
	filtered := make([]ports.EventSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	return &FanoutSink{sinks: filtered}
}

// Emit forwards the event to each configured sink.
func (s *FanoutSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	for _, sink := range s.sinks {
		sink.Emit(ctx, ev)
	}
}

// Close closes every configured sink and joins any returned errors.
func (s *FanoutSink) Close(ctx context.Context) error {
	var errs []error
	for _, sink := range s.sinks {
		if err := sink.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
