package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type syncRecordingSink struct {
	mu     sync.Mutex
	events []ports.TelemetryEvent
	closed bool
}

func (s *syncRecordingSink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *syncRecordingSink) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *syncRecordingSink) snapshot() []ports.TelemetryEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.TelemetryEvent, len(s.events))
	copy(out, s.events)
	return out
}

func TestAggregatingSinkPassesThroughUnaggregatedNamesImmediately(t *testing.T) {
	rec := &syncRecordingSink{}
	s := NewAggregatingSink(rec, []string{"ao.http.5xx"}, time.Hour)
	defer s.Close(context.Background())

	s.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.cli.invoked"})

	events := rec.snapshot()
	if len(events) != 1 || events[0].Name != "ao.cli.invoked" {
		t.Fatalf("events = %#v, want one immediate ao.cli.invoked passthrough", events)
	}
}

func TestAggregatingSinkFoldsBurstIntoOneRollupOnFlush(t *testing.T) {
	rec := &syncRecordingSink{}
	s := NewAggregatingSink(rec, []string{"ao.http.5xx"}, time.Hour)

	for i := 0; i < 812; i++ {
		s.Emit(context.Background(), ports.TelemetryEvent{
			Name:    "ao.http.5xx",
			Payload: map[string]any{"path": "/api/v1/whatever"},
		})
	}
	if got := len(rec.snapshot()); got != 0 {
		t.Fatalf("events before flush = %d, want 0 (buffered, not yet flushed)", got)
	}

	s.flush(context.Background())

	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("events after flush = %d, want 1 rollup", len(events))
	}
	if events[0].Name != "ao.http.5xx" {
		t.Fatalf("rollup name = %q, want ao.http.5xx", events[0].Name)
	}
	if count, _ := events[0].Payload["count"].(int); count != 812 {
		t.Fatalf("rollup count = %#v, want 812", events[0].Payload["count"])
	}
	if _, ok := events[0].Payload["path"]; ok {
		t.Fatalf("rollup exported per-error path: %#v", events[0].Payload)
	}
	windowStart, ok := events[0].Payload["window_start"].(string)
	if !ok || windowStart == "" {
		t.Fatalf("rollup payload window_start = %#v, want non-empty RFC3339 string", events[0].Payload["window_start"])
	}
	if _, err := time.Parse(time.RFC3339Nano, windowStart); err != nil {
		t.Fatalf("window_start not RFC3339: %v", err)
	}
	windowEnd, ok := events[0].Payload["window_end"].(string)
	if !ok || windowEnd == "" {
		t.Fatalf("rollup payload window_end = %#v, want non-empty RFC3339 string", events[0].Payload["window_end"])
	}
	if _, err := time.Parse(time.RFC3339Nano, windowEnd); err != nil {
		t.Fatalf("window_end not RFC3339: %v", err)
	}
}

func TestHTTP5xxFanoutKeepsLocalDetailsAndExportsCountOnly(t *testing.T) {
	local, remote := &syncRecordingSink{}, &syncRecordingSink{}
	rollup := NewAggregatingSink(remote, []string{"ao.http.5xx"}, time.Hour)
	fanout := NewFanoutSink(local, rollup)
	for _, code := range []string{"FIRST", "SECOND"} {
		fanout.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.http.5xx", RequestID: code,
			Payload: map[string]any{"error_code": code, "path": "/private/path"}})
	}
	rollup.flush(context.Background())
	localEvents, remoteEvents := local.snapshot(), remote.snapshot()
	if len(localEvents) != 2 || localEvents[0].Payload["error_code"] != "FIRST" || localEvents[1].Payload["error_code"] != "SECOND" {
		t.Fatalf("local detail stream = %#v", localEvents)
	}
	if len(remoteEvents) != 1 || remoteEvents[0].Payload["count"] != 2 || remoteEvents[0].RequestID != "" {
		t.Fatalf("remote rollup = %#v", remoteEvents)
	}
	if _, ok := remoteEvents[0].Payload["error_code"]; ok {
		t.Fatalf("remote rollup retained error code: %#v", remoteEvents[0])
	}
}

func TestAggregatingSinkTracksEventNamesIndependently(t *testing.T) {
	rec := &syncRecordingSink{}
	s := NewAggregatingSink(rec, []string{"ao.http.5xx", "ao.daemon.panic"}, time.Hour)

	for i := 0; i < 3; i++ {
		s.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.http.5xx"})
	}
	s.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.panic"})
	s.flush(context.Background())

	events := rec.snapshot()
	counts := map[string]int{}
	for _, ev := range events {
		count, _ := ev.Payload["count"].(int)
		counts[ev.Name] = count
	}
	if counts["ao.http.5xx"] != 3 {
		t.Fatalf("ao.http.5xx rollup count = %d, want 3", counts["ao.http.5xx"])
	}
	if counts["ao.daemon.panic"] != 1 {
		t.Fatalf("ao.daemon.panic rollup count = %d, want 1", counts["ao.daemon.panic"])
	}
}

func TestAggregatingSinkClosesFlushesBufferedEvents(t *testing.T) {
	rec := &syncRecordingSink{}
	s := NewAggregatingSink(rec, []string{"ao.http.5xx"}, time.Hour)

	s.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.http.5xx"})
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	events := rec.snapshot()
	if len(events) != 1 || events[0].Payload["count"] != 1 {
		t.Fatalf("events after Close = %#v, want one rollup with count 1", events)
	}
	if !rec.closed {
		t.Fatal("wrapped sink was not closed")
	}
}

func TestAggregatingSinkClosedTwiceDoesNotFlushAgain(t *testing.T) {
	rec := &syncRecordingSink{}
	s := NewAggregatingSink(rec, []string{"ao.http.5xx"}, time.Hour)

	s.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.http.5xx"})
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	if got := len(rec.snapshot()); got != 1 {
		t.Fatalf("events after double Close = %d, want 1 (no duplicate rollup)", got)
	}
}
