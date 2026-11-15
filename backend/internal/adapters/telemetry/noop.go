package telemetry

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type NoopSink struct{}

func (NoopSink) Emit(context.Context, ports.TelemetryEvent) {}

func (NoopSink) Close(context.Context) error { return nil }
