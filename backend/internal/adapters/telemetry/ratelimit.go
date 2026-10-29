package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A per-minute cap alone doesn't bound the daily total: a loop that paces
// itself just under the minute ceiling (e.g. one call every 3-4 seconds)
// would sit under it forever and still rack up a large daily count. Two caps
// close that: a burst ceiling for real signal in a short window, and a hard
// daily ceiling that bounds worst case regardless of pacing. Both bound the
// billed (remote) sink only; local storage keeps every occurrence.
const (
	// eventsPerNamePerMinute caps a short burst per event name. 5 is enough
	// for genuine failure reporting (several real 5xx/panics while something
	// is actually broken) without leaving room for a tight retry loop.
	eventsPerNamePerMinute = 5
	// eventsPerNamePerDay is the hard ceiling per event name regardless of
	// how a runaway is paced. At anonymous PostHog rates this bounds a single
	// looping event name to a few dollars a month at worst, not hundreds.
	eventsPerNamePerDay = 200
	// eventsPerNamePerDayAggregated applies to event names an upstream
	// AggregatingSink has already folded into at most one rollup event per
	// flush window (see aggregate.go) before they ever reach this limiter.
	// The true occurrence count for the window is compressed into that one
	// event's `count` field, so per-occurrence cost is already gone; this
	// tier exists as a structural backstop (in case the aggregator itself
	// misbehaves), not as the real limiting mechanism, so it can be much
	// higher. 1500/day comfortably covers a name flushing every minute for a
	// full day (1440) with headroom, at a cost of a few cents a month even
	// in the worst case.
	eventsPerNamePerDayAggregated = 1500
)

// RateLimitedSink wraps a sink and drops events past a per-event-name rate
// ceiling. Intended to wrap only the remote (billed) sink; local storage
// should see every event unfiltered.
type RateLimitedSink struct {
	next ports.EventSink

	// aggregated marks event names that get the generous daily tier because
	// an upstream AggregatingSink already compresses their occurrence count
	// into one rollup per flush window.
	aggregated map[string]struct{}
	// skipMinute marks names that are already one summary per key, so a
	// burst of those summaries must not hit the per-minute cap. The daily
	// cap still applies.
	skipMinute map[string]struct{}

	mu      sync.Mutex
	minutes map[string]*rateWindow
	days    map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

// NewRateLimitedSink wraps next with the per-event-name rate ceiling.
// aggregatedNames identifies event names that are pre-aggregated upstream
// (see NewAggregatingSink) and should get the generous daily tier instead of
// the standard one; pass nil if next has no aggregation in front of it.
func NewRateLimitedSink(next ports.EventSink, aggregatedNames []string) *RateLimitedSink {
	aggregated := make(map[string]struct{}, len(aggregatedNames))
	for _, n := range aggregatedNames {
		aggregated[n] = struct{}{}
	}
	return &RateLimitedSink{
		next:       next,
		aggregated: aggregated,
		skipMinute: map[string]struct{}{},
		minutes:    make(map[string]*rateWindow),
		days:       make(map[string]*rateWindow),
	}
}

// SkipMinuteWindow exempts names whose events are already summaries. The
// daily ceiling still applies.
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

// Emit forwards ev to the wrapped sink unless its event name has exceeded
// either ceiling, in which case it is silently dropped.
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

// Close closes the wrapped sink.
func (s *RateLimitedSink) Close(ctx context.Context) error {
	return s.next.Close(ctx)
}
