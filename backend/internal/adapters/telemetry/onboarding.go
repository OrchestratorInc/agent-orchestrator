package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// OnboardingSink records the first observed human CLI command once per install.
type OnboardingSink struct {
	next     ports.EventSink
	identity *Identity
}

// NewOnboardingSink wraps a sink with a durable CLI milestone reservation.
func NewOnboardingSink(next ports.EventSink, identity *Identity) *OnboardingSink {
	return &OnboardingSink{next: next, identity: identity}
}

// Emit forwards an event and records the first eligible CLI command.
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
	s.next.Emit(ctx, ports.TelemetryEvent{Name: "ao.onboarding.cli_detected", Source: "cli", OccurredAt: time.Now().UTC(), Level: ports.TelemetryLevelInfo})
}

// Close closes the wrapped sink.
func (s *OnboardingSink) Close(ctx context.Context) error { return s.next.Close(ctx) }
