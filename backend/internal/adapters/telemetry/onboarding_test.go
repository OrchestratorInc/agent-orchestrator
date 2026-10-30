package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCLIOnboardingMilestoneIsDurableAndHonorsOptOut(t *testing.T) {
	dir := t.TempDir()
	identity, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := &syncRecordingSink{}
	event := ports.TelemetryEvent{Name: "ao.cli.invoked", Payload: map[string]any{"actor_type": "user"}}
	sink := NewOnboardingSink(rec, identity)
	sink.Emit(context.Background(), event)
	sink.Emit(context.Background(), event)
	restarted := NewOnboardingSink(rec, identity)
	restarted.Emit(context.Background(), event)
	var milestones int
	for _, got := range rec.snapshot() {
		if got.Name == "ao.onboarding.cli_detected" {
			milestones++
		}
	}
	if milestones != 1 {
		t.Fatalf("milestones = %d, want one after restart", milestones)
	}
	if err := os.WriteFile(filepath.Join(dir, OptOutFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	other := NewOnboardingSink(rec, identity)
	other.Emit(context.Background(), event)
	milestones = 0
	for _, got := range rec.snapshot() {
		if got.Name == "ao.onboarding.cli_detected" {
			milestones++
		}
	}
	if milestones != 1 {
		t.Fatal("opted out CLI command generated an onboarding milestone")
	}
}

func TestSecondSessionMilestoneIsDurable(t *testing.T) {
	dir := t.TempDir()
	identity, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := &syncRecordingSink{}
	event := ports.TelemetryEvent{Name: "ao.onboarding.second_session_spawned"}
	NewOnboardingSink(rec, identity).Emit(context.Background(), event)
	NewOnboardingSink(rec, identity).Emit(context.Background(), event)
	if got := len(rec.snapshot()); got != 1 {
		t.Fatalf("milestones = %d, want one after restart", got)
	}
}
