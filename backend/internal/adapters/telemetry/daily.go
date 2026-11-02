package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

const (
	cliUsageEventName    = "ao.cli.usage_errors"
	dailyCommandCountCap = 20
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
	Sent   bool           `json:"sent"`
}

// NewDailySummarySink loads any unsent day from path. An empty path keeps the
// summary in memory only.
func NewDailySummarySink(next ports.EventSink, path string) *DailySummarySink {
	sink := &DailySummarySink{next: next, path: path, state: dailyUsageState{Counts: map[string]int{}}}
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
	s.state.Counts[commandPath(ev)+"\x00"+errorKind(ev)]++
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

// Discard drops the unsent summary and removes its file. Opt-out and an
// install-id reset call this so command paths are not flushed later.
func (s *DailySummarySink) Discard() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = dailyUsageState{Counts: map[string]int{}}
	if s.path != "" {
		_ = os.Remove(s.path)
	}
}

func (s *DailySummarySink) flushCompleted(ctx context.Context, now time.Time) {
	s.mu.Lock()
	events := s.takeCompletedLocked(now)
	s.mu.Unlock()
	if s.next == nil {
		return
	}
	for _, ev := range events {
		s.next.Emit(ctx, ev)
	}
}

func (s *DailySummarySink) rollLocked(ctx context.Context, now time.Time) {
	events := s.takeCompletedLocked(now)
	if len(events) == 0 {
		s.ensureDayLocked(now)
		return
	}
	s.mu.Unlock()
	for _, ev := range events {
		s.next.Emit(ctx, ev)
	}
	s.mu.Lock()
	s.ensureDayLocked(now)
	s.persistLocked()
}

func (s *DailySummarySink) takeCompletedLocked(now time.Time) []ports.TelemetryEvent {
	day := utcDay(now)
	if s.state.Day == "" || s.state.Day >= day || s.state.Sent || len(s.state.Counts) == 0 {
		return nil
	}
	type pair struct {
		path, kind string
		count      int
	}
	pairs := make([]pair, 0, len(s.state.Counts))
	for key, count := range s.state.Counts {
		if count <= 0 {
			continue
		}
		path, kind, _ := strings.Cut(key, "\x00")
		pairs = append(pairs, pair{path, kind, count})
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count && pairs[i].path == pairs[j].path {
			return pairs[i].kind < pairs[j].kind
		}
		if pairs[i].count == pairs[j].count {
			return pairs[i].path < pairs[j].path
		}
		return pairs[i].count > pairs[j].count
	})
	if len(pairs) > dailyCommandCountCap {
		pairs = pairs[:dailyCommandCountCap]
	}
	summarized := s.state.Day
	events := make([]ports.TelemetryEvent, 0, len(pairs))
	for _, pair := range pairs {
		events = append(events, ports.TelemetryEvent{
			Name:       cliUsageEventName,
			Source:     "cli",
			OccurredAt: endOfUTCDay(summarized),
			Level:      ports.TelemetryLevelWarn,
			Payload: map[string]any{
				"component":    "cli",
				"operation":    "command_parse",
				"command_path": pair.path,
				"error_kind":   pair.kind,
				"day":          summarized,
				"count":        pair.count,
			},
		})
	}
	s.state.Sent = true
	s.persistLocked()
	return events
}

func (s *DailySummarySink) ensureDayLocked(now time.Time) {
	day := utcDay(now)
	if s.state.Day == day {
		if s.state.Counts == nil {
			s.state.Counts = map[string]int{}
		}
		return
	}
	s.state = dailyUsageState{Day: day, Counts: map[string]int{}}
}

func endOfUTCDay(day string) time.Time {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Now().UTC()
	}
	// Last millisecond of that UTC day. Midnight of the next day is bucketed
	// as the following day.
	return parsed.UTC().Add(24*time.Hour - time.Millisecond)
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
	path = telemetrymeta.NormalizeCommandPath(path)
	if !isAllowedCommandValue("command_path", path) {
		return "other"
	}
	if len(path) > 160 {
		path = path[:160]
	}
	return path
}
