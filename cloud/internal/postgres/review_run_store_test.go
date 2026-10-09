package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/jackc/pgx/v5"
	"strings"
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
	reviewer := domain.ProjectReviewer{Harness: "codex", AgentConfig: domain.ProjectAgentConfig{Model: "review-model", Effort: "high"}}
	command := reviewTerminalOpenCommand("reviewer-terminal-id", "review-run-id", "review this pull request", reviewer)
	if command.TerminalID != "reviewer-terminal-id" {
		t.Fatalf("TerminalID = %q", command.TerminalID)
	}
	if command.Kind != "reviewer" || command.Review || command.ReviewRunID != "review-run-id" {
		t.Fatalf("review open command = %#v, want isolated reviewer", command)
	}
	if command.Reviewer == nil || *command.Reviewer != reviewer {
		t.Fatalf("reviewer settings = %+v, want %+v", command.Reviewer, reviewer)
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

func TestReviewCompletionNotifiesAndDeliversFindings(t *testing.T) {
	for _, inject := range []bool{false, true} {
		t.Run(fmt.Sprint(inject), func(t *testing.T) {
			store, _, fixture := openNotificationTestStore(t)
			ctx := context.Background()
			if _, err := store.UpdateSessionPreferences(ctx, domain.Principal{UserID: fixture.userID, Provider: "local"}, fixture.orgID, fixture.sessionID, SessionPreferencesUpdate{AutoInjectReview: &inject}); err != nil {
				t.Fatal(err)
			}
			pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 22, "https://github.test/owner/repo/pull/22", "feature", "main", "review-head", "Review", 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, pr.HeadSHA, "codex", "manual")
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.CompleteAndDeliverReviewRun(ctx, fixture.orgID, run.ID, fixture.sessionID, domain.SubmitReviewResult{Verdict: contract.AOReviewVerdictChangesRequested, Body: "Missing validation in handler.go"}, "123")
			if err != nil {
				t.Fatal(err)
			}
			err = store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
				var title string
				if err := tx.QueryRow(ctx, `SELECT title FROM ao_notifications WHERE org_id=$1 AND pull_request_id=$2 AND type='review_completed'`, fixture.orgID, pr.ID).Scan(&title); err != nil {
					return err
				}
				if title != "PR review completed" {
					t.Errorf("notification title = %q", title)
				}
				var payloads [][]byte
				rows, err := tx.Query(ctx, `SELECT payload FROM ao_commands WHERE org_id=$1 AND session_id=$2 AND kind='message.send'`, fixture.orgID, fixture.sessionID)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var payload []byte
					if err := rows.Scan(&payload); err != nil {
						return err
					}
					payloads = append(payloads, payload)
				}
				if inject {
					if len(payloads) != 1 || !strings.Contains(string(payloads[0]), "Missing validation in handler.go") {
						t.Errorf("feedback payloads = %s", payloads)
					}
				} else if len(payloads) != 0 {
					t.Errorf("auto inject disabled but queued %d messages", len(payloads))
				}
				return rows.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReviewOpenUsesSnapshotForManualAndAuto(t *testing.T) {
	for _, source := range []string{"manual", "auto"} {
		t.Run(source, func(t *testing.T) {
			store, _, fixture := openNotificationTestStore(t)
			ctx := context.Background()
			pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 23, "https://github.test/owner/repo/pull/23", "feature", "main", "sha", "Review", 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, pr.HeadSHA, "codex", source)
			if err != nil {
				t.Fatal(err)
			}
			terminalID, err := store.OpenReviewTerminal(ctx, fixture.orgID, fixture.sessionID, run.ID, "Review only this PR", "codex")
			if err != nil {
				t.Fatal(err)
			}
			err = store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
				var payload []byte
				if err := tx.QueryRow(ctx, `SELECT payload FROM ao_worker_requests WHERE org_id=$1 AND session_id=$2 AND kind='terminal.open' AND payload->>'terminalId'=$3`, fixture.orgID, fixture.sessionID, terminalID).Scan(&payload); err != nil {
					return err
				}
				var command worker.TerminalCommand
				if err := json.Unmarshal(payload, &command); err != nil {
					return err
				}
				if command.Kind != "reviewer" || command.ReviewRunID != run.ID || command.Review || command.Reviewer == nil || string(command.Data) != "Review only this PR" {
					t.Errorf("command=%+v", command)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAutomaticReviewCandidatesWaitForIdleAndSkipReviewedHeads(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 24, "https://github.test/owner/repo/pull/24", "feature", "main", "auto-sha", "Review", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		prs, err := store.AutomaticReviewCandidates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, candidate := range prs {
			if candidate.ID == pr.ID {
				found = true
			}
		}
		if found != want {
			t.Fatalf("candidate present=%v, want %v", found, want)
		}
	}
	check(false)
	err = store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET auto_review_enabled=true,activity_state='active' WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	check(false)
	err = store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_sessions SET activity_state='idle' WHERE org_id=$1 AND id=$2`, fixture.orgID, fixture.sessionID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	check(true)
	run, _, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, pr.HeadSHA, "codex", "manual")
	if err != nil {
		t.Fatal(err)
	}
	check(false)
	if _, err = store.CompleteAndDeliverReviewRun(ctx, fixture.orgID, run.ID, fixture.sessionID, domain.SubmitReviewResult{Verdict: contract.AOReviewVerdictApproved, Body: "Looks good"}, "456"); err != nil {
		t.Fatal(err)
	}
	check(false)
	prior, created, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, pr.HeadSHA, "codex", "auto")
	if err != nil || created || prior.ID != run.ID {
		t.Fatalf("automatic retry of reviewed head: run=%s created=%v err=%v", prior.ID, created, err)
	}
	err = store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_pull_requests SET head_sha='new-head' WHERE org_id=$1 AND id=$2`, fixture.orgID, pr.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	check(true)
}

func TestReviewerTerminalCannotReceiveWorkerFeedback(t *testing.T) {
	store, _, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}
	agent, err := store.EnsureWorkerAgentTerminal(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 24, "https://github.test/owner/repo/pull/24", "feature", "main", "sha", "Review", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.CreateReviewRun(ctx, fixture.orgID, pr.ID, fixture.sessionID, pr.HeadSHA, "codex", "manual")
	if err != nil {
		t.Fatal(err)
	}
	reviewerID, err := store.OpenReviewTerminal(ctx, fixture.orgID, fixture.sessionID, run.ID, "Review", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE ao_terminal_sessions SET state='open' WHERE org_id=$1 AND id=$2`, fixture.orgID, reviewerID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	found, err := store.EnsureWorkerAgentTerminal(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, time.Hour)
	if err != nil || found.ID != agent.ID {
		t.Fatalf("worker lookup selected reviewer: %+v, %v", found, err)
	}
	ticket, _, err := store.IssueTerminalTicket(ctx, principal, fixture.orgID, fixture.sessionID, "agent", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	attached, err := store.OpenTerminal(ctx, ticket, "agent", "", time.Hour)
	if err != nil || attached.ID != agent.ID {
		t.Fatalf("worker browser attached to reviewer: %+v, %v", attached, err)
	}
	if _, err := store.SendMessage(ctx, principal, fixture.orgID, fixture.sessionID, "review-feedback-target", "Address the review feedback", domain.ChatTurnSettings{}); err != nil {
		t.Fatal(err)
	}
	if err := store.withOrg(ctx, fixture.orgID, func(tx pgx.Tx) error {
		var target string
		err := tx.QueryRow(ctx, `SELECT payload->>'terminalId' FROM ao_worker_requests WHERE org_id=$1 AND session_id=$2 AND kind='terminal.input' ORDER BY created_at DESC LIMIT 1`, fixture.orgID, fixture.sessionID).Scan(&target)
		if err == nil && target != agent.ID {
			t.Errorf("feedback target=%s, worker=%s, reviewer=%s", target, agent.ID, reviewerID)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// An explicit reviewer attachment is still allowed; only implicit worker
	// lookups must exclude the review process.
	ticket, _, err = store.IssueTerminalTicket(ctx, principal, fixture.orgID, fixture.sessionID, "agent", reviewerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	attached, err = store.OpenTerminal(ctx, ticket, "agent", reviewerID, time.Hour)
	if err != nil || attached.ID != reviewerID {
		t.Fatalf("review panel attachment broke: %+v, %v", attached, err)
	}
}
