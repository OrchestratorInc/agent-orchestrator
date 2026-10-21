package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Issue #6931: each retry types the whole batch into the orchestrator again,
// so a delivery that was written but never acknowledged must wait longer
// before the next try instead of repeating every few seconds.
func TestCoordinatorWaitsLongerAfterEachUnacknowledgedDelivery(t *testing.T) {
	now := time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI}}
	store.add(reportAt("r1", "worker", now, now, "note"))
	delivery := &coordinatorDelivery{submitErr: fmt.Errorf("send: %w", domain.ErrReportDeliveryUnacknowledged)}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, RetryDelay: 5 * time.Second})

	for attempt, wait := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute} {
		if _, err := c.RunDue(context.Background()); !errors.Is(err, domain.ErrReportDeliveryUnacknowledged) {
			t.Fatalf("attempt %d: RunDue error = %v", attempt+1, err)
		}
		r := store.reports[0]
		if r.DeliveryState != domain.ReportPending || !r.AvailableAt.Equal(now.Add(wait)) {
			t.Fatalf("attempt %d: state %s, next try at %v; want pending at %v", attempt+1, r.DeliveryState, r.AvailableAt, now.Add(wait))
		}
		if delay, err := c.RunDue(context.Background()); err != nil || delay != wait {
			t.Fatalf("attempt %d: RunDue before the wait = %v, %v; want %v, nil", attempt+1, delay, err, wait)
		}
		now = now.Add(wait)
	}
}

func TestCoordinatorRetriesOtherFailuresWithoutWaiting(t *testing.T) {
	// Nothing was typed (for example, a permission dialog is open), so the
	// next try should not be pushed back.
	now := time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI}}
	store.add(reportAt("r1", "worker", now, now, "note"))
	delivery := &coordinatorDelivery{submitErr: errors.New("session awaiting a decision")}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("RunDue succeeded; want the submit error")
	}
	if r := store.reports[0]; r.DeliveryState != domain.ReportPending || r.AvailableAt.After(now) {
		t.Fatalf("state %s, next try at %v; want pending now", r.DeliveryState, r.AvailableAt)
	}
}

func TestReportRetryWaitIsCapped(t *testing.T) {
	for attempts, want := range map[int64]time.Duration{
		0: 15 * time.Second, 1: 15 * time.Second, 2: 30 * time.Second,
		6: 8 * time.Minute, 7: 10 * time.Minute, 64: 10 * time.Minute, 1 << 40: 10 * time.Minute,
	} {
		if got := reportRetryWait(attempts); got != want {
			t.Errorf("reportRetryWait(%d) = %v, want %v", attempts, got, want)
		}
	}
}

// Claude Code turns a pasted path ending in .png into an image attachment and
// moves an [Image #N] placeholder in front of AO's envelope. Backticks keep
// the reference as plain text.
func TestRenderPutsOutputReferencesInBackticks(t *testing.T) {
	batch := PreparedBatch{Reports: []domain.ReportRecord{{
		ID: "r1", SessionID: "w", ProjectID: "p", State: domain.ReportDone, Note: "done", RepeatCount: 1,
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputArtifact, Reference: "/Users/me/.ao/data/artifacts/w/test8-fine.png"},
			{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/34"},
		},
	}}}
	got := batch.Render()
	for _, want := range []string{
		"\nartifact: `/Users/me/.ao/data/artifacts/w/test8-fine.png`",
		"\npr_created: `https://github.com/o/r/pull/34`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render() = %q, missing %q", got, want)
		}
	}
}

func TestQuoteImagePathsInNoteText(t *testing.T) {
	in := "Rendered /x/out.PNG\nsee /y/a.jpg and `/z/b.png` too.\nno image here: /w/data.json"
	want := "Rendered `/x/out.PNG`\nsee `/y/a.jpg` and `/z/b.png` too.\nno image here: /w/data.json"
	if got := quoteImagePaths(in); got != want {
		t.Fatalf("quoteImagePaths() =\n%q\nwant\n%q", got, want)
	}
}

func TestCoordinatorWaitCountsOnlyUnacknowledgedDeliveries(t *testing.T) {
	// Fast retries while a permission dialog was open must not make the first
	// real unacknowledged delivery wait the maximum.
	now := time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI}}
	store.add(reportAt("r1", "worker", now, now, "note"))
	delivery := &coordinatorDelivery{submitErr: errors.New("session awaiting a decision")}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	for range 10 {
		_, _ = c.RunDue(context.Background())
		now = now.Add(5 * time.Second)
	}
	delivery.submitErr = fmt.Errorf("send: %w", domain.ErrReportDeliveryUnacknowledged)
	_, _ = c.RunDue(context.Background())
	if r := store.reports[0]; !r.AvailableAt.Equal(now.Add(15 * time.Second)) {
		t.Fatalf("next try at %v, want %v (after %d claims)", r.AvailableAt, now.Add(15*time.Second), r.DeliveryAttempts)
	}
}
