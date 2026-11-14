package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type OnboardingSink struct {
	next     ports.EventSink
	identity *Identity
}

func NewOnboardingSink(next ports.EventSink, identity *Identity) *OnboardingSink {
	return &OnboardingSink{next: next, identity: identity}
}

func (s *OnboardingSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	s.next.Emit(ctx, ev)
	if ev.Name != "ao.cli.invoked" || ev.Payload["actor_type"] != "user" || s.identity == nil || s.identity.ExportSuppressed() {
		return
	}
	file, err := os.OpenFile(filepath.Join(s.identity.dataDir, "telemetry_cli_detected_emitted"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	_ = file.Close()
	s.next.Emit(ctx, ports.TelemetryEvent{Name: "ao.cli.first_used", Source: "cli", OccurredAt: time.Now().UTC(), Level: ports.TelemetryLevelInfo})
}

func (s *OnboardingSink) Close(ctx context.Context) error { return s.next.Close(ctx) }
