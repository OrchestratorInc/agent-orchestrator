package telemetry

import (
	"context"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// NoopSink discards every event.
type NoopSink struct{}

// Emit discards the event.
func (NoopSink) Emit(context.Context, ports.TelemetryEvent) {}

// Close is a no-op.
func (NoopSink) Close(context.Context) error { return nil }
