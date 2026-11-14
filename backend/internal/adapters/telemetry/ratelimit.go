package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	eventsPerNamePerMinute        = 5
	eventsPerNamePerDay           = 200
	eventsPerNamePerDayAggregated = 1500
	eventsPerNamePerDayUsage      = 100000
)

type RateLimitedSink struct {
	next ports.EventSink

	aggregated map[string]struct{}
	skipMinute map[string]struct{}
	usageDay   map[string]struct{}

	mu      sync.Mutex
	minutes map[string]*rateWindow
	days    map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

func NewRateLimitedSink(next ports.EventSink, aggregatedNames []string) *RateLimitedSink {
	aggregated := make(map[string]struct{}, len(aggregatedNames))
	for _, n := range aggregatedNames {
		aggregated[n] = struct{}{}
	}
	return &RateLimitedSink{
		next:       next,
		aggregated: aggregated,
		skipMinute: map[string]struct{}{},
		usageDay:   map[string]struct{}{},
		minutes:    make(map[string]*rateWindow),
		days:       make(map[string]*rateWindow),
	}
}

func (s *RateLimitedSink) UseUsageDailyWindow(names ...string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		s.usageDay[name] = struct{}{}
	}
}

func (s *RateLimitedSink) SkipMinuteWindow(names ...string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		s.skipMinute[name] = struct{}{}
	}
}

func (s *RateLimitedSink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	if !s.reserve(ev.Name, time.Now()) {
		return
	}
	s.next.Emit(ctx, ev)
}

func (s *RateLimitedSink) reserve(name string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, skip := s.skipMinute[name]; !skip {
		if !reserveWindow(s.minutes, name, now, time.Minute, eventsPerNamePerMinute) {
			return false
		}
	}
	dayLimit := eventsPerNamePerDay
	if _, ok := s.aggregated[name]; ok {
		dayLimit = eventsPerNamePerDayAggregated
	}
	if _, ok := s.usageDay[name]; ok {
		dayLimit = eventsPerNamePerDayUsage
	}
	return reserveWindow(s.days, name, now, 24*time.Hour, dayLimit)
}

func reserveWindow(windows map[string]*rateWindow, name string, now time.Time, size time.Duration, limit int) bool {
	w, ok := windows[name]
	if !ok || now.Sub(w.start) >= size {
		w = &rateWindow{start: now}
		windows[name] = w
	}
	if w.count >= limit {
		return false
	}
	w.count++
	return true
}

func (s *RateLimitedSink) Close(ctx context.Context) error {
	return s.next.Close(ctx)
}
