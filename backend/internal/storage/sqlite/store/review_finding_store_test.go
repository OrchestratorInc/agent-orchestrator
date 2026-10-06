package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type findingFixture struct {
	s       *store.Store
	session domain.SessionID
	review  domain.Review
	now     time.Time
}

func newFindingFixture(t *testing.T) findingFixture {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	review := domain.Review{ID: "review-1", SessionID: rec.ID, ProjectID: rec.ProjectID, Harness: domain.ReviewerClaudeCode, PRURL: "https://example/pr/1", CreatedAt: now, UpdatedAt: now}
	if err := s.UpsertReview(ctx, review); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	return findingFixture{s: s, session: rec.ID, review: review, now: now}
}

func (f findingFixture) run(t *testing.T, id, sha string, harness domain.ReviewerHarness, offset time.Duration) domain.ReviewRun {
	t.Helper()
	run := domain.ReviewRun{
		ID: id, ReviewID: f.review.ID, SessionID: f.session, Harness: harness, PRURL: f.review.PRURL,
		TargetSHA: sha, Status: domain.ReviewRunRunning, CreatedAt: f.now.Add(offset), AutoInjectReview: true,
	}
	if err := f.s.InsertReviewRun(context.Background(), run); err != nil {
		t.Fatalf("insert run %s: %v", id, err)
	}
	return run
}

func complete(t *testing.T, s *store.Store, run domain.ReviewRun, verdict domain.ReviewVerdict, findings ...domain.ReviewFindingInput) {
	t.Helper()
	run.Verdict = verdict
	run.Body = "summary"
	ok, err := s.CompleteReviewRun(context.Background(), run, findings, run.CreatedAt.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("complete %s: ok=%v err=%v", run.ID, ok, err)
	}
}

func findingStatuses(t *testing.T, s *store.Store, runID string) []domain.ReviewFindingStatus {
	t.Helper()
	findings, err := s.ListReviewFindingsByRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("list findings: %v", err)
	}
	out := make([]domain.ReviewFindingStatus, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Status)
	}
	return out
}

func TestCompleteReviewRunFilesFindingsOnceAndOnlyForARunningRun(t *testing.T) {
	f := newFindingFixture(t)
	ctx := context.Background()
	run := f.run(t, "run-1", "sha-1", domain.ReviewerClaudeCode, 0)
	complete(t, f.s, run, domain.VerdictChangesRequested,
		domain.ReviewFindingInput{Path: "a.go", Line: 3, Body: "nil check"},
		domain.ReviewFindingInput{Body: "design: split this"},
	)

	// A retried completion finds the run no longer running and files nothing.
	run.Verdict = domain.VerdictChangesRequested
	ok, err := f.s.CompleteReviewRun(ctx, run, []domain.ReviewFindingInput{{Body: "again"}}, f.now)
	if err != nil || ok {
		t.Fatalf("second completion ok=%v err=%v, want false", ok, err)
	}
	findings, err := f.s.ListReviewFindingsByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Ordinal != 1 || findings[0].Path != "a.go" || findings[0].Line != 3 || findings[1].Body != "design: split this" {
		t.Fatalf("findings = %+v", findings)
	}
	stored, _, err := f.s.GetReviewRun(ctx, run.ID)
	if err != nil || stored.Status != domain.ReviewRunComplete || stored.Verdict != domain.VerdictChangesRequested {
		t.Fatalf("run = %+v err=%v", stored, err)
	}
}

func TestCompleteReviewRunSupersedesEarlierOpenFindingsButNotAParallelReviewer(t *testing.T) {
	f := newFindingFixture(t)
	ctx := context.Background()
	old := f.run(t, "run-old", "sha-1", domain.ReviewerClaudeCode, 0)
	complete(t, f.s, old, domain.VerdictChangesRequested, domain.ReviewFindingInput{Body: "old open"}, domain.ReviewFindingInput{Body: "old resolved"})
	oldFindings, _ := f.s.ListReviewFindingsByRun(ctx, old.ID)
	if ok, err := f.s.ResolveReviewFinding(ctx, oldFindings[1].ID, "fixed", f.session, f.now); err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	parallel := f.run(t, "run-codex", "sha-2", domain.ReviewerCodex, time.Second)
	complete(t, f.s, parallel, domain.VerdictChangesRequested, domain.ReviewFindingInput{Body: "codex view"})
	newer := f.run(t, "run-new", "sha-2", domain.ReviewerClaudeCode, 2*time.Second)
	complete(t, f.s, newer, domain.VerdictApproved)

	if got := findingStatuses(t, f.s, old.ID); got[0] != domain.ReviewFindingSuperseded || got[1] != domain.ReviewFindingResolved {
		t.Fatalf("older head findings = %v, want [superseded resolved]: resolution history is immutable", got)
	}
	if got := findingStatuses(t, f.s, parallel.ID); got[0] != domain.ReviewFindingOpen {
		t.Fatalf("a different reviewer's pass on the same head kept %v, want open", got)
	}
	// The first completed pass on the newer head, whichever reviewer ran it,
	// replaced the older head's open findings.
	superseded, _ := f.s.ListReviewFindingsByRun(ctx, old.ID)
	if superseded[0].SupersededByRunID != parallel.ID {
		t.Fatalf("superseded by %q, want %q", superseded[0].SupersededByRunID, parallel.ID)
	}
	if ok, err := f.s.ResolveReviewFinding(ctx, superseded[0].ID, "late", f.session, f.now); err != nil || ok {
		t.Fatalf("resolving a superseded finding ok=%v err=%v, want false", ok, err)
	}
}

func TestReviewFindingResolutionEmitsReviewRunUpdated(t *testing.T) {
	f := newFindingFixture(t)
	ctx := context.Background()
	run := f.run(t, "run-1", "sha-1", domain.ReviewerClaudeCode, 0)
	complete(t, f.s, run, domain.VerdictChangesRequested, domain.ReviewFindingInput{Body: "fix"})
	before, err := f.s.EventsAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	findings, _ := f.s.ListReviewFindingsByRun(ctx, run.ID)
	if ok, err := f.s.ResolveReviewFinding(ctx, findings[0].ID, "done", "", f.now); err != nil || !ok {
		t.Fatalf("resolve ok=%v err=%v", ok, err)
	}
	after, err := f.s.EventsAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("resolution emitted %d events, want 1", len(after)-len(before))
	}
	ev := after[len(after)-1]
	if ev.Type != cdc.EventReviewRunUpdated || ev.SessionID != string(f.session) {
		t.Fatalf("event = %+v", ev)
	}
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != run.ID || payload["findingId"] != findings[0].ID || payload["findingStatus"] != "resolved" {
		t.Fatalf("payload = %#v", payload)
	}
	got, _, _ := f.s.GetReviewFinding(ctx, findings[0].ID)
	if got.ResolvedBySessionID != "" || got.ResolutionNote != "done" || got.ResolvedAt == nil {
		t.Fatalf("a person's resolution = %+v", got)
	}
}

func TestUndeliveredReviewRunsAndDeliveryStamp(t *testing.T) {
	f := newFindingFixture(t)
	ctx := context.Background()
	run := f.run(t, "run-1", "sha-1", domain.ReviewerClaudeCode, 0)
	complete(t, f.s, run, domain.VerdictApproved)
	optedOut := f.run(t, "run-2", "sha-2", domain.ReviewerCodex, time.Second)
	optedOut.AutoInjectReview = false
	complete(t, f.s, optedOut, domain.VerdictApproved)

	pending, err := f.s.ListUndeliveredReviewRunsForPR(ctx, f.session, f.review.PRURL)
	if err != nil || len(pending) != 1 || pending[0].ID != run.ID {
		t.Fatalf("pending = %+v err=%v, want only the auto-inject run", pending, err)
	}
	all, err := f.s.ListUndeliveredReviewRuns(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("all pending = %+v err=%v", all, err)
	}
	if ok, err := f.s.MarkReviewRunDelivered(ctx, run.ID, f.now); err != nil || !ok {
		t.Fatalf("mark delivered ok=%v err=%v", ok, err)
	}
	if ok, err := f.s.MarkReviewRunDelivered(ctx, run.ID, f.now); err != nil || ok {
		t.Fatalf("second stamp ok=%v err=%v, want false", ok, err)
	}
	if pending, _ := f.s.ListUndeliveredReviewRunsForPR(ctx, f.session, f.review.PRURL); len(pending) != 0 {
		t.Fatalf("delivered run still pending: %+v", pending)
	}
	if err := f.s.SetReviewRunProviderPost(ctx, optedOut.ID, "", "github: 403"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := f.s.GetReviewRun(ctx, optedOut.ID)
	if got.ProviderPostError != "github: 403" {
		t.Fatalf("provider post error = %q", got.ProviderPostError)
	}
}

// A pass retired from delivery (stale head, replaced, PR gone) must leave both
// delivery queues for good, so the sweep stops re-examining it.
func TestRetiredReviewRunLeavesTheDeliveryQueues(t *testing.T) {
	f := newFindingFixture(t)
	ctx := context.Background()
	run := f.run(t, "run-1", "sha-1", domain.ReviewerClaudeCode, 0)
	complete(t, f.s, run, domain.VerdictApproved)
	if err := f.s.RetireReviewRunDelivery(ctx, run.ID, "head_moved"); err != nil {
		t.Fatal(err)
	}
	if pending, err := f.s.ListUndeliveredReviewRuns(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("sweep queue = %+v err=%v, want empty", pending, err)
	}
	if pending, err := f.s.ListUndeliveredReviewRunsForPR(ctx, f.session, f.review.PRURL); err != nil || len(pending) != 0 {
		t.Fatalf("PR queue = %+v err=%v, want empty", pending, err)
	}
	if ok, err := f.s.MarkReviewRunDelivered(ctx, run.ID, f.now); err != nil || !ok {
		// Retirement only drops the pass from the queue; it is still a
		// complete pass and its history stays readable.
		t.Fatalf("mark delivered after retire ok=%v err=%v", ok, err)
	}
	got, _, _ := f.s.GetReviewRun(ctx, run.ID)
	if got.DeliverySkippedReason != "head_moved" {
		t.Fatalf("reason = %q", got.DeliverySkippedReason)
	}
	if err := f.s.RetireReviewRunDelivery(ctx, run.ID, "replaced_by_newer_pass"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := f.s.GetReviewRun(ctx, run.ID); got.DeliverySkippedReason != "head_moved" {
		t.Fatalf("a second retirement overwrote the first reason: %q", got.DeliverySkippedReason)
	}
}
