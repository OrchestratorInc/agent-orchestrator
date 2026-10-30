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
	if len(rec.events) < 1 {
		t.Fatal("want one event per command path")
	}
	if len(rec.events) != 2 {
		t.Fatalf("events = %d, want one per command path", len(rec.events))
	}
	byPath := map[string]ports.TelemetryEvent{}
	for _, ev := range rec.events {
		byPath[ev.Payload["command_path"].(string)] = ev
	}
	if byPath["ao session"].Payload["count"] != 3 || byPath["ao send"].Payload["count"] != 1 {
		t.Fatalf("counts = %#v", rec.events)
	}
	if byPath["ao session"].Payload["error_kind"] != "usage" {
		t.Fatalf("error_kind = %#v", byPath["ao session"].Payload["error_kind"])
	}
	if _, ok := byPath["ao session"].Payload["error"]; ok {
		t.Fatal("summary carried a raw error")
	}
	if !byPath["ao session"].OccurredAt.Equal(time.Date(2026, 10, 20, 23, 59, 59, int(time.Millisecond*999), time.UTC)) {
		t.Fatalf("occurred = %s", byPath["ao session"].OccurredAt)
	}

	restarted.Emit(context.Background(), usageEvent(next.Add(time.Hour), "ao preview"))
	if len(rec.events) != 2 {
		t.Fatalf("second same-day event after flush emitted again: %d", len(rec.events))
	}
}

func TestDailySummaryCollapsesUnknownCommandPaths(t *testing.T) {
	rec := &dailyRecorder{}
	day := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	sink := NewDailySummarySink(rec, "")
	sink.Emit(context.Background(), usageEvent(day, "ao secret-token"))
	sink.Emit(context.Background(), usageEvent(day, "AO  SESSION"))
	sink.Emit(context.Background(), usageEvent(day.Add(24*time.Hour), "ao preview"))
	byPath := map[string]int{}
	for _, ev := range rec.events {
		byPath[ev.Payload["command_path"].(string)] = ev.Payload["count"].(int)
	}
	if byPath["other"] != 1 || byPath["ao session"] != 1 {
		t.Fatalf("counts = %#v", byPath)
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
