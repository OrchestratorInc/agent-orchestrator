package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	cliUsageEventName     = "ao.cli.usage_errors"
	dailyCommandCountCap  = 20
	dailyCommandCountSize = 1024
)

// DailySummarySink turns ao.cli.usage_errors into one remote event per UTC day.
// Other events pass through. The local fanout is upstream, so diagnostics stay
// complete. Counts persist across restarts. A day is sent once, after it ends,
// when a later event arrives or FlushCompleted runs. Opt-out must wrap this
// sink so a suppressed day is not stored and flushed after opt-in.
type DailySummarySink struct {
	next ports.EventSink
	path string

	mu    sync.Mutex
	state dailyUsageState
}

type dailyUsageState struct {
	Day    string         `json:"day"`
	Counts map[string]int `json:"counts"`
	Kinds  map[string]int `json:"kinds"`
	Sent   bool           `json:"sent"`
}

// NewDailySummarySink loads any unsent day from path. An empty path keeps the
// summary in memory only.
func NewDailySummarySink(next ports.EventSink, path string) *DailySummarySink {
	sink := &DailySummarySink{next: next, path: path, state: dailyUsageState{Counts: map[string]int{}, Kinds: map[string]int{}}}
	if path == "" {
		return sink
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return sink
	}
	var stored dailyUsageState
	if json.Unmarshal(raw, &stored) != nil || stored.Day == "" {
		return sink
	}
	if stored.Counts == nil {
		stored.Counts = map[string]int{}
	}
	if stored.Kinds == nil {
		stored.Kinds = map[string]int{}
	}
	sink.state = stored
	return sink
}

// Emit records a CLI usage error or forwards every other event. Crossing into
// a new UTC day flushes the previous day's summary first.
func (s *DailySummarySink) Emit(ctx context.Context, ev ports.TelemetryEvent) {
	if s == nil || s.next == nil {
		return
	}
	if ev.Name != cliUsageEventName {
		s.flushCompleted(ctx, ev.OccurredAt)
		s.next.Emit(ctx, ev)
		return
	}
	s.mu.Lock()
	s.rollLocked(ctx, ev.OccurredAt)
	s.state.Counts[commandPath(ev)]++
	s.state.Kinds[errorKind(ev)]++
	s.persistLocked()
	s.mu.Unlock()
}

// FlushCompleted sends a finished, unsent day. The in-progress day stays put.
func (s *DailySummarySink) FlushCompleted(ctx context.Context, now time.Time) {
	if s == nil {
		return
	}
	s.flushCompleted(ctx, now)
}

// Close flushes a completed day and closes the next sink.
func (s *DailySummarySink) Close(ctx context.Context) error {
	if s == nil || s.next == nil {
		return nil
	}
	s.FlushCompleted(ctx, time.Now().UTC())
	return s.next.Close(ctx)
}

func (s *DailySummarySink) flushCompleted(ctx context.Context, now time.Time) {
	s.mu.Lock()
	ev, ok := s.takeCompletedLocked(now)
	s.mu.Unlock()
	if !ok || s.next == nil {
		return
	}
	s.next.Emit(ctx, ev)
}

func (s *DailySummarySink) rollLocked(ctx context.Context, now time.Time) {
	ev, ok := s.takeCompletedLocked(now)
	if !ok {
		s.ensureDayLocked(now)
		return
	}
	s.mu.Unlock()
	s.next.Emit(ctx, ev)
	s.mu.Lock()
	s.ensureDayLocked(now)
	s.persistLocked()
}

func (s *DailySummarySink) takeCompletedLocked(now time.Time) (ports.TelemetryEvent, bool) {
	day := utcDay(now)
	if s.state.Day == "" || s.state.Day >= day || s.state.Sent || len(s.state.Counts) == 0 {
		return ports.TelemetryEvent{}, false
	}
	total := 0
	for _, count := range s.state.Counts {
		if count > 0 {
			total += count
		}
	}
	if total == 0 {
		return ports.TelemetryEvent{}, false
	}
	summarized := s.state.Day
	ev := ports.TelemetryEvent{
		Name:       cliUsageEventName,
		Source:     "cli",
		OccurredAt: endOfUTCDay(summarized),
		Level:      ports.TelemetryLevelWarn,
		Payload: map[string]any{
			"component":      "cli",
			"operation":      "command_parse",
			"error_kind":     "usage",
			"day":            summarized,
			"count":          total,
			"command_counts": formatCommandCounts(s.state.Counts),
			"error_counts":   formatCommandCounts(s.state.Kinds),
		},
	}
	s.state.Sent = true
	s.persistLocked()
	return ev, true
}

func (s *DailySummarySink) ensureDayLocked(now time.Time) {
	day := utcDay(now)
	if s.state.Day == day {
		if s.state.Counts == nil {
			s.state.Counts = map[string]int{}
		}
		if s.state.Kinds == nil {
			s.state.Kinds = map[string]int{}
		}
		return
	}
	s.state = dailyUsageState{Day: day, Counts: map[string]int{}, Kinds: map[string]int{}}
}

func endOfUTCDay(day string) time.Time {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Now().UTC()
	}
	return parsed.UTC().Add(24 * time.Hour)
}

func errorKind(ev ports.TelemetryEvent) string {
	kind, _ := ev.Payload["error_kind"].(string)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "usage"
	}
	if len(kind) > 80 {
		kind = kind[:80]
	}
	return kind
}

func (s *DailySummarySink) persistLocked() {
	if s.path == "" {
		return
	}
	raw, err := json.Marshal(s.state)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o700)
	_ = os.WriteFile(s.path, raw, 0o600)
}

func utcDay(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC().Format("2006-01-02")
}

func commandPath(ev ports.TelemetryEvent) string {
	path, _ := ev.Payload["command_path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		path = "unknown"
	}
	if len(path) > 160 {
		path = path[:160]
	}
	return path
}

func formatCommandCounts(counts map[string]int) string {
	type pair struct {
		path  string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for path, count := range counts {
		if count > 0 && path != "" {
			pairs = append(pairs, pair{path, count})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].path < pairs[j].path
		}
		return pairs[i].count > pairs[j].count
	})
	if len(pairs) > dailyCommandCountCap {
		pairs = pairs[:dailyCommandCountCap]
	}
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair.path+"="+strconv.Itoa(pair.count))
	}
	joined := strings.Join(parts, ",")
	if len(joined) > dailyCommandCountSize {
		joined = joined[:dailyCommandCountSize]
	}
	return joined
}
