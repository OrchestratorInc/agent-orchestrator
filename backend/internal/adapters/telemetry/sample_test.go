package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSamplingSinkKeepsFirstThenOneInOneHundred(t *testing.T) {
	rec := &recordingSink{}
	sink := NewSamplingSink(rec, map[string]float64{"ao.http.5xx": 0.01})
	ev := ports.TelemetryEvent{
		Name: "ao.http.5xx", Source: "http", OccurredAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		Payload: map[string]any{"operation": "http_request", "error_code": "PROJECTS_LIST_FAILED", "status": 500},
	}
	for i := 0; i < 2000; i++ {
		sink.Emit(context.Background(), ev)
	}
	other := ev
	other.Payload = map[string]any{"operation": "http_request", "error_code": "OTHER", "status": 500}
	sink.Emit(context.Background(), other)
	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.session.spawned"})
	if len(rec.events) != 22 {
		t.Fatalf("events = %d, want 20 sampled plus the other error plus the unsampled event", len(rec.events))
	}
	if rec.events[0].Payload["sample_rate"] != float64(1) {
		t.Fatalf("first sample_rate = %#v", rec.events[0].Payload["sample_rate"])
	}
	if rec.events[1].Payload["sample_rate"] != 0.01 {
		t.Fatalf("later sample_rate = %#v", rec.events[1].Payload["sample_rate"])
	}
	if _, changed := ev.Payload["sample_rate"]; changed {
		t.Fatal("sampling changed the caller payload")
	}
	nextDay := ev
	nextDay.OccurredAt = ev.OccurredAt.Add(24 * time.Hour)
	sink.Emit(context.Background(), nextDay)
	if rec.events[len(rec.events)-1].Payload["sample_rate"] != float64(1) {
		t.Fatal("next UTC day did not send the first occurrence")
	}
}
