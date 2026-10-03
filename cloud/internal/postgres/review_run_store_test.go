package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// reviewRunRowStub makes the nullable terminal column explicit. PostgreSQL
// returns NULL until OpenReviewTerminal records the terminal ID.
type reviewRunRowStub struct {
	terminalID *string
}

func (s reviewRunRowStub) Scan(dest ...any) error {
	values := []string{
		"run-id", "org-id", "pr-id", "session-id", "target-sha", "codex",
		"manual", "running", "", "", "", "", // trigger source through last_error
	}
	for i, value := range values {
		if i == 11 { // review_terminal_id is nullable.
			continue
		}
		*dest[i].(*string) = value
	}
	*dest[11].(**string) = s.terminalID
	*dest[13].(*time.Time) = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	*dest[14].(**time.Time) = nil
	*dest[15].(**time.Time) = nil
	return nil
}

func TestScanReviewRunAllowsMissingTerminal(t *testing.T) {
	run, err := scanReviewRun(reviewRunRowStub{})
	if err != nil {
		t.Fatalf("scanReviewRun: %v", err)
	}
	if run.ReviewTerminalID != "" {
		t.Fatalf("ReviewTerminalID = %q, want empty for a newly created review", run.ReviewTerminalID)
	}
}

func TestScanReviewRunReadsTerminal(t *testing.T) {
	const terminalID = "terminal-id"
	run, err := scanReviewRun(reviewRunRowStub{terminalID: ptr(terminalID)})
	if err != nil {
		t.Fatalf("scanReviewRun: %v", err)
	}
	if run.ReviewTerminalID != terminalID {
		t.Fatalf("ReviewTerminalID = %q, want %q", run.ReviewTerminalID, terminalID)
	}
}

func TestTerminalTicketPurposeBindsReviewerTerminal(t *testing.T) {
	if got, want := terminalTicketPurpose("agent", "reviewer-terminal-id"), "terminal:agent:reviewer-terminal-id"; got != want {
		t.Fatalf("terminalTicketPurpose = %q, want %q", got, want)
	}
	if got, want := terminalTicketPurpose("agent", ""), "terminal:agent"; got != want {
		t.Fatalf("generic terminalTicketPurpose = %q, want %q", got, want)
	}
}

func TestReviewTerminalOpenCommandCarriesInitialPrompt(t *testing.T) {
	command := reviewTerminalOpenCommand("reviewer-terminal-id", "review this pull request", "codex")
	if command.TerminalID != "reviewer-terminal-id" {
		t.Fatalf("TerminalID = %q", command.TerminalID)
	}
	if command.Kind != "agent" || !command.Review {
		t.Fatalf("review open command = %#v, want review agent", command)
	}
	if command.Harness != "codex" {
		t.Fatalf("Harness = %q, want codex", command.Harness)
	}
	if got, want := string(command.Data), "review this pull request"; got != want {
		t.Fatalf("initial review prompt = %q, want %q", got, want)
	}
}

func TestAutomaticReviewConflictReturnsRunningManualRun(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID,
		"github", "owner/repo", "author", 17, "https://github.test/owner/repo/pull/17",
		"feature", "main", "review-sha", "Review conflict", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	completedAuto, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "review-sha", "codex", "auto")
	if err != nil || !created {
		t.Fatalf("create auto review: created=%v err=%v", created, err)
	}
	if _, err := store.CompleteAndDeliverReviewRun(ctx, fixture.orgID, completedAuto.ID, fixture.sessionID,
		domain.SubmitReviewResult{Verdict: contract.AOReviewVerdictApproved, Body: "Approved"}, "123"); err != nil {
		t.Fatalf("complete auto review: %v", err)
	}
	priorAuto, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "review-sha", "codex", "auto")
	if err != nil || created || priorAuto.ID != completedAuto.ID {
		t.Fatalf("repeat automatic trigger: run=%s created=%v err=%v; want completed auto %s", priorAuto.ID, created, err, completedAuto.ID)
	}
	runningManual, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "review-sha", "codex", "manual")
	if err != nil || !created {
		t.Fatalf("create manual review: run=%+v created=%v err=%v", runningManual, created, err)
	}
	got, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "review-sha", "codex", "auto")
	if err != nil || created || got.ID != runningManual.ID {
		t.Fatalf("conflicting auto review: run=%s created=%v err=%v; want running manual %s", got.ID, created, err, runningManual.ID)
	}
}

func TestAutomaticReviewConflictWithOnlyRunningManualRun(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID,
		"github", "owner/repo", "author", 19, "https://github.test/owner/repo/pull/19",
		"feature", "main", "manual-only-sha", "Manual review conflict", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	runningManual, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "manual-only-sha", "codex", "manual")
	if err != nil || !created {
		t.Fatalf("create manual review: run=%+v created=%v err=%v", runningManual, created, err)
	}
	got, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "manual-only-sha", "codex", "auto")
	if err != nil || created || got.ID != runningManual.ID {
		t.Fatalf("conflicting auto review: run=%s created=%v err=%v; want running manual %s", got.ID, created, err, runningManual.ID)
	}
}

func TestReviewPublicationClaimIsDurableAndRejectsChangedVerdict(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID,
		"github", "owner/repo", "author", 18, "https://github.test/owner/repo/pull/18",
		"feature", "main", "publication-sha", "Review publication", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, "publication-sha", "codex", "manual")
	if err != nil || !created {
		t.Fatalf("create review: created=%v err=%v", created, err)
	}
	terminalID, err := store.OpenReviewTerminal(ctx, fixture.orgID, fixture.sessionID, run.ID, "Review", "codex")
	if err != nil {
		t.Fatalf("open reviewer terminal: %v", err)
	}
	result := domain.SubmitReviewResult{Verdict: contract.AOReviewVerdictApproved, Body: "Looks good"}
	claimed, err := store.BeginReviewPublication(ctx, fixture.orgID, run.ID, fixture.sessionID, result)
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}
	claimed, err = store.BeginReviewPublication(ctx, fixture.orgID, run.ID, fixture.sessionID, result)
	if err != nil || claimed {
		t.Fatalf("retry claim: claimed=%v err=%v", claimed, err)
	}
	if _, err := store.FailReviewRun(ctx, fixture.orgID, run.ID, fixture.sessionID, "reviewer terminal exited"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terminal failure after publication claim = %v, want the run preserved", err)
	}
	if err := store.MarkTerminalExited(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, terminalID, fixture.epoch, 1, false); err != nil {
		t.Fatalf("record reviewer terminal exit: %v", err)
	}
	retained, err := store.ReviewRunPullRequest(ctx, fixture.orgID, run.ID)
	if err != nil || retained.Status != contract.AOReviewRunRunning {
		t.Fatalf("run after reviewer exit: status=%s err=%v", retained.Status, err)
	}
	changed := domain.SubmitReviewResult{Verdict: contract.AOReviewVerdictChangesRequested, Body: "Different"}
	if _, err := store.BeginReviewPublication(ctx, fixture.orgID, run.ID, fixture.sessionID, changed); !errors.Is(err, ErrInvalid) {
		t.Fatalf("changed verdict error = %v, want ErrInvalid", err)
	}
	if err := store.MarkReviewPublicationUncertain(ctx, fixture.orgID, run.ID, fixture.sessionID, "response lost"); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.BeginReviewPublication(ctx, fixture.orgID, run.ID, fixture.sessionID, result)
	if err != nil || claimed {
		t.Fatalf("uncertain retry claim: claimed=%v err=%v", claimed, err)
	}
	delivered, err := store.CompleteAndDeliverReviewRun(ctx, fixture.orgID, run.ID, fixture.sessionID, result, "81")
	if err != nil || delivered.ProviderReviewID != "81" {
		t.Fatalf("deliver: run=%+v err=%v", delivered, err)
	}
}

func ptr(value string) *string { return &value }
