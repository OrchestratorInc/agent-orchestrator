package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSamplingSinkRemoteBoundary(t *testing.T) {
	base := ports.TelemetryEvent{
		Name: "ao.http.5xx", Source: "http", RequestID: "same-request",
		OccurredAt: time.Unix(42, 0), Payload: map[string]any{"error_code": "internal"},
	}
	for _, tc := range []struct {
		name string
		hash uint64
		want int
	}{
		{"below threshold", 0, 1},
		{"above threshold", ^uint64(0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingSink{}
			sink := NewSamplingSink(rec, map[string]float64{"ao.http.5xx": 0.01})
			sink.hash = func(ports.TelemetryEvent) uint64 { return tc.hash }
			sink.Emit(context.Background(), base)
			sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.session.spawned"})
			if len(rec.events) != tc.want+1 {
				t.Fatalf("events = %d, want %d", len(rec.events), tc.want+1)
			}
			if tc.want == 1 {
				if rec.events[0].Payload["sample_rate"] != 0.01 || rec.events[0].Payload["error_code"] != "internal" {
					t.Fatalf("sample payload = %#v", rec.events[0].Payload)
				}
				if _, changed := base.Payload["sample_rate"]; changed {
					t.Fatal("sampling changed the local event payload")
				}
			}
		})
	}
	first := eventSampleHash(base)
	second := eventSampleHash(base)
	if first != second {
		t.Fatal("same occurrence changed sampling decision")
	}
	rec := &recordingSink{}
	rates := map[string]float64{"ao.http.5xx": 2, "ao.cli.usage_errors": -1}
	sink := NewSamplingSink(rec, rates)
	rates["ao.http.5xx"] = 0
	sink.hash = func(ports.TelemetryEvent) uint64 { return ^uint64(0) }
	sink.Emit(context.Background(), base)
	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.cli.usage_errors"})
	if len(rec.events) != 1 || rec.events[0].Payload["sample_rate"] != float64(1) {
		t.Fatalf("bounded and copied rates failed: %#v", rec.events)
	}
}
