package session

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeCardSummaryStore struct {
	mu     sync.Mutex
	writes []string
}

func (f *fakeCardSummaryStore) UpdateSessionCardSummary(_ context.Context, _ domain.SessionID, summary string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, summary)
	return true, nil
}

func (f *fakeCardSummaryStore) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

type cardSummaryRig struct {
	c       *CardSummarizer
	store   *fakeCardSummaryStore
	now     time.Time
	timers  []func()
	waits   []time.Duration
	enabled bool
}

func newCardSummaryRig(rewrite CardSummaryRewriter) *cardSummaryRig {
	r := &cardSummaryRig{store: &fakeCardSummaryStore{}, now: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), enabled: true}
	r.c = NewCardSummarizer(context.Background(), r.store, func(context.Context) bool { return r.enabled }, rewrite, nil)
	r.c.now = func() time.Time { return r.now }
	r.c.after = func(d time.Duration, f func()) { r.waits = append(r.waits, d); r.timers = append(r.timers, f) }
	return r
}

func (r *cardSummaryRig) report(state domain.ReportState, note string) {
	r.c.ReportFiled(domain.ReportRecord{SessionID: "ao-1", State: state, Note: note})
	r.c.wg.Wait()
}

func modelLine(_ context.Context, _ domain.SessionID, note string) (string, error) {
	return `"card: ` + note + `"`, nil
}

func TestCardSummarizerRewritesEachReportAtMostOnceAMinute(t *testing.T) {
	t.Parallel()
	r := newCardSummaryRig(modelLine)

	r.report(domain.ReportCheckpoint, "first")
	r.now = r.now.Add(20 * time.Second)
	r.report(domain.ReportCheckpoint, "second")
	r.report(domain.ReportCheckpoint, "third")
	if want := []string{"card: first"}; !slices.Equal(r.store.got(), want) {
		t.Fatalf("writes before the interval = %q, want %q", r.store.got(), want)
	}
	if len(r.timers) != 1 || r.waits[0] != 40*time.Second {
		t.Fatalf("held notes set %d timers, waits %v; want one of 40s", len(r.timers), r.waits)
	}
	r.now = r.now.Add(40 * time.Second)
	r.timers[0]()
	r.c.wg.Wait()
	if want := []string{"card: first", "card: third"}; !slices.Equal(r.store.got(), want) {
		t.Fatalf("writes after the interval = %q, want the newest held note: %q", r.store.got(), want)
	}
}

func TestCardSummarizerNeverHoldsBackDoneNeedsInputOrStuck(t *testing.T) {
	t.Parallel()
	r := newCardSummaryRig(modelLine)
	r.report(domain.ReportCheckpoint, "working")
	r.report(domain.ReportCheckpoint, "held")
	r.report(domain.ReportNeedsInput, "asking")
	r.report(domain.ReportStuck, "stuck")
	r.report(domain.ReportDone, "finished")
	r.timers[0]()
	r.c.wg.Wait()
	want := []string{"card: working", "card: asking", "card: stuck", "card: finished"}
	if !slices.Equal(r.store.got(), want) {
		t.Fatalf("writes = %q, want %q (the held checkpoint is superseded)", r.store.got(), want)
	}
}

func TestCardSummarizerKeepsTheNewestLineWhenAnOlderRewriteIsSlow(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	r := newCardSummaryRig(func(ctx context.Context, id domain.SessionID, note string) (string, error) {
		if note == "slow" {
			<-release
		}
		return modelLine(ctx, id, note)
	})
	r.c.ReportFiled(domain.ReportRecord{SessionID: "ao-1", State: domain.ReportCheckpoint, Note: "slow"})
	r.c.ReportFiled(domain.ReportRecord{SessionID: "ao-1", State: domain.ReportDone, Note: "fast"})
	deadline := time.Now().Add(5 * time.Second)
	for len(r.store.got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	r.c.wg.Wait()
	if want := []string{"card: fast"}; !slices.Equal(r.store.got(), want) {
		t.Fatalf("writes = %q, want only the newer line %q", r.store.got(), want)
	}
}

func TestCardSummarizerFallsBackToTheCutNote(t *testing.T) {
	t.Parallel()
	r := newCardSummaryRig(func(context.Context, domain.SessionID, string) (string, error) {
		return "", errors.New("provider down")
	})
	r.report(domain.ReportDone, "  Added the 404 page;\n build passes ")
	if want := []string{"Added the 404 page; build passes"}; !slices.Equal(r.store.got(), want) {
		t.Fatalf("writes = %q, want %q", r.store.got(), want)
	}

	blank := newCardSummaryRig(func(context.Context, domain.SessionID, string) (string, error) { return " `` ", nil })
	blank.report(domain.ReportDone, "Shipped it")
	if want := []string{"Shipped it"}; !slices.Equal(blank.store.got(), want) {
		t.Fatalf("blank model reply writes = %q, want %q", blank.store.got(), want)
	}
}

func TestCardSummarizerSkipsRepeatsBlankNotesAndOff(t *testing.T) {
	t.Parallel()
	calls := 0
	r := newCardSummaryRig(func(ctx context.Context, id domain.SessionID, note string) (string, error) {
		calls++
		return modelLine(ctx, id, note)
	})
	r.report(domain.ReportDone, "finished")
	r.report(domain.ReportDone, "finished")
	r.report(domain.ReportCheckpoint, "   ")
	r.now = r.now.Add(CardSummaryInterval)
	r.c.ReportFiled(domain.ReportRecord{SessionID: "ao-1", Message: "informational update"})
	r.c.wg.Wait()
	if want := []string{"card: finished", "card: informational update"}; calls != 2 || !slices.Equal(r.store.got(), want) {
		t.Fatalf("calls = %d, writes = %q; want 2 and %q", calls, r.store.got(), want)
	}

	r.enabled = false
	r.report(domain.ReportDone, "while off")
	if calls != 2 || len(r.store.got()) != 2 {
		t.Fatalf("off: calls = %d, writes = %q; want nothing new", calls, r.store.got())
	}
}
