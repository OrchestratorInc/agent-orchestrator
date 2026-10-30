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

// NewOnboardingSink wraps a sink with durable onboarding milestone reservations.
func NewOnboardingSink(next ports.EventSink, identity *Identity) *OnboardingSink {
	return &OnboardingSink{next: next, identity: identity}
}

// Emit forwards an event and records eligible milestones once per install.
func (s *OnboardingSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	if ev.Name == "ao.onboarding.second_session_spawned" {
		if s.identity == nil || s.identity.ExportSuppressed() || !s.claim("telemetry_second_session_emitted") {
			return
		}
	}
	s.next.Emit(ctx, ev)
	if ev.Name != "ao.cli.invoked" || ev.Payload["actor_type"] != "user" || s.identity == nil || s.identity.ExportSuppressed() {
		return
	}
	if !s.claim("telemetry_cli_detected_emitted") {
		return
	}
	s.next.Emit(ctx, ports.TelemetryEvent{Name: "ao.onboarding.cli_detected", Source: "cli", OccurredAt: time.Now().UTC(), Level: ports.TelemetryLevelInfo})
}

func (s *OnboardingSink) claim(name string) bool {
	file, err := os.OpenFile(filepath.Join(s.identity.dataDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

// Close closes the wrapped sink.
func (s *OnboardingSink) Close(ctx context.Context) error { return s.next.Close(ctx) }
