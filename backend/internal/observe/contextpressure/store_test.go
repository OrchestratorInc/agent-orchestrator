package contextpressure

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestStoreReportsTheNewestReading(t *testing.T) {
	s := NewStore()
	early := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)

	if got := s.Get("s1"); got != nil {
		t.Fatalf("unreported session = %+v, want nil (unknown)", got)
	}

	s.Record("s1", 40, "claude-code-statusline", early)
	s.Record("s1", 75, "claude-code-statusline", late)
	got := s.Get("s1")
	if got == nil || got.ContextUsedPercent != 75 || !got.ObservedAt.Equal(late) {
		t.Fatalf("reading = %+v, want the later 75%%", got)
	}

	// A report that arrives late but describes an earlier moment must not
	// overwrite a newer one.
	s.Record("s1", 10, "claude-code-statusline", early)
	if got := s.Get("s1"); got.ContextUsedPercent != 75 {
		t.Fatalf("stale report overwrote the newer reading: %+v", got)
	}
}

func TestStoreClampsAndFillsObservedAt(t *testing.T) {
	s := NewStore()
	fixed := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }

	s.Record("over", 140, "x", fixed)
	if got := s.Get("over"); got.ContextUsedPercent != 100 {
		t.Errorf("over-reported percent = %d, want clamped 100", got.ContextUsedPercent)
	}
	s.Record("under", -5, "x", fixed)
	if got := s.Get("under"); got.ContextUsedPercent != 0 {
		t.Errorf("negative percent = %d, want clamped 0", got.ContextUsedPercent)
	}

	s.Record("no-time", 50, "x", time.Time{})
	if got := s.Get("no-time"); !got.ObservedAt.Equal(fixed) {
		t.Errorf("observedAt = %v, want the clock's now", got.ObservedAt)
	}

	s.Record("", 50, "x", fixed)
	if got := s.Get(""); got != nil {
		t.Error("an empty session id was recorded")
	}
}

func TestStorePrunesExpiredReadings(t *testing.T) {
	s := NewStore()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	old := now.Add(-2 * retention)
	for i := 0; i < pruneThreshold; i++ {
		s.Record(domain.SessionID(fmt.Sprintf("old-%d", i)), 50, "x", old)
	}
	if s.Get("old-0") == nil {
		t.Fatal("a reading was dropped before the threshold was crossed")
	}

	s.Record("fresh", 50, "x", now)
	if got := s.Get("old-0"); got != nil {
		t.Errorf("expired reading survived the sweep: %+v", got)
	}
	if got := s.Get("fresh"); got == nil {
		t.Error("the sweep dropped a current reading")
	}
}

func TestStoreIsSafeUnderConcurrentUse(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.Record("s1", 50, "x", time.Now()) }()
		go func() { defer wg.Done(); s.Get("s1") }()
	}
	wg.Wait()
}
