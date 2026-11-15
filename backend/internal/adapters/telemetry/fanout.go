package telemetry

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type FanoutSink struct {
	sinks []ports.EventSink
}

func NewFanoutSink(sinks ...ports.EventSink) *FanoutSink {
	filtered := make([]ports.EventSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	return &FanoutSink{sinks: filtered}
}

func (s *FanoutSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	for _, sink := range s.sinks {
		sink.Emit(ctx, ev)
	}
}

func (s *FanoutSink) Close(ctx context.Context) error {
	var errs []error
	for _, sink := range s.sinks {
		if err := sink.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
