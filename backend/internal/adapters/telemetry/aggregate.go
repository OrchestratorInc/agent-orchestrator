package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type AggregatingSink struct {
	next       ports.EventSink
	names      map[string]struct{}
	flushEvery time.Duration

	mu      sync.Mutex
	buckets map[string]*aggBucket
	closed  bool
	done    chan struct{}
}

type aggBucket struct {
	windowStart time.Time
	count       int
	sample      ports.TelemetryEvent
}

func NewAggregatingSink(next ports.EventSink, names []string, flushEvery time.Duration) *AggregatingSink {
	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}
	s := &AggregatingSink{
		next:       next,
		names:      nameSet,
		flushEvery: flushEvery,
		buckets:    make(map[string]*aggBucket),
		done:       make(chan struct{}),
	}
	go s.flushLoop()
	return s
}

func (s *AggregatingSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	if _, ok := s.names[ev.Name]; !ok {
		s.next.Emit(ctx, ev)
		return
	}

	s.mu.Lock()
	b, ok := s.buckets[ev.Name]
	if !ok {
		b = &aggBucket{windowStart: time.Now()}
		s.buckets[ev.Name] = b
	}
	b.count++
	b.sample = ev
	s.mu.Unlock()
}

func (s *AggregatingSink) flushLoop() {
	ticker := time.NewTicker(s.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.flush(context.Background())
		case <-s.done:
			return
		}
	}
}

func (s *AggregatingSink) flush(ctx context.Context) {
	s.mu.Lock()
	buckets := s.buckets
	s.buckets = make(map[string]*aggBucket)
	s.mu.Unlock()

	now := time.Now().UTC()
	for name, b := range buckets {
		payload := make(map[string]any, len(b.sample.Payload)+3)
		if name != "ao.http.5xx" {
			for k, v := range b.sample.Payload {
				payload[k] = v
			}
		}
		payload["count"] = b.count
		payload["window_start"] = b.windowStart.UTC().Format(time.RFC3339Nano)
		payload["window_end"] = now.Format(time.RFC3339Nano)

		rollup := ports.TelemetryEvent{
			Name:       name,
			Source:     b.sample.Source,
			OccurredAt: now,
			Level:      b.sample.Level,
			Payload:    payload,
		}
		if name != "ao.http.5xx" {
			rollup.ProjectID = b.sample.ProjectID
			rollup.SessionID = b.sample.SessionID
			rollup.RequestID = b.sample.RequestID
		}
		s.next.Emit(ctx, rollup)
	}
}

func (s *AggregatingSink) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.next.Close(ctx)
	}
	s.closed = true
	s.mu.Unlock()

	close(s.done)
	s.flush(ctx)
	return s.next.Close(ctx)
}
