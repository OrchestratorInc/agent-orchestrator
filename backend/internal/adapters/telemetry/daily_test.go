package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type dailyRecorder struct {
	events []ports.TelemetryEvent
}

func (r *dailyRecorder) Emit(_ context.Context, ev ports.TelemetryEvent) {
	r.events = append(r.events, ev)
}
func (r *dailyRecorder) Close(context.Context) error { return nil }

func usageEvent(at time.Time, path string) ports.TelemetryEvent {
	return ports.TelemetryEvent{
		Name: cliUsageEventName, Source: "cli", OccurredAt: at, Level: ports.TelemetryLevelWarn,
		Payload: map[string]any{"command_path": path, "error": "secret prompt"},
	}
}

func TestDailySummaryOneEventPerInstallDay(t *testing.T) {
	rec := &dailyRecorder{}
	path := filepath.Join(t.TempDir(), "cli-usage.json")
	day := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	sink := NewDailySummarySink(rec, path)
	sink.Emit(context.Background(), usageEvent(day, "ao session"))
	sink.Emit(context.Background(), usageEvent(day, "ao session"))
	sink.Emit(context.Background(), usageEvent(day.Add(time.Hour), "ao send"))
	if len(rec.events) != 0 {
		t.Fatalf("same-day events = %d, want 0", len(rec.events))
	}

	restarted := NewDailySummarySink(rec, path)
	restarted.Emit(context.Background(), usageEvent(day.Add(2*time.Hour), "ao session"))
	if len(rec.events) != 0 {
		t.Fatalf("restart same day emitted %d events", len(rec.events))
	}

	next := day.Add(24 * time.Hour)
	restarted.Emit(context.Background(), usageEvent(next, "ao preview"))
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want one daily summary", len(rec.events))
	}
	got := rec.events[0]
	if got.Payload["count"] != 4 {
		t.Fatalf("count = %#v, want 4", got.Payload["count"])
	}
	if got.Payload["command_counts"] != "ao session=3,ao send=1" {
		t.Fatalf("command_counts = %#v", got.Payload["command_counts"])
	}
	if _, ok := got.Payload["error"]; ok {
		t.Fatalf("summary carried raw error payload: %#v", got.Payload)
	}
	if got.Payload["day"] != "2026-10-20" {
		t.Fatalf("day = %#v", got.Payload["day"])
	}
	if got.Payload["error_counts"] != "usage=4" {
		t.Fatalf("error_counts = %#v", got.Payload["error_counts"])
	}
	if !got.OccurredAt.Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("occurred = %s, want end of summarized day", got.OccurredAt)
	}

	restarted.Emit(context.Background(), usageEvent(next.Add(time.Hour), "ao preview"))
	if len(rec.events) != 1 {
		t.Fatalf("second same-day event after flush emitted again: %d", len(rec.events))
	}
}

func TestDailySummaryPassesOtherEventsThrough(t *testing.T) {
	rec := &dailyRecorder{}
	sink := NewDailySummarySink(rec, "")
	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", OccurredAt: time.Now().UTC()})
	if len(rec.events) != 1 || rec.events[0].Name != "ao.daemon.started" {
		t.Fatalf("events = %#v", rec.events)
	}
}
