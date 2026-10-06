package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// deliveryStore adds the AO-review delivery reads to the reducer fake.
type deliveryStore struct {
	*fakeStore
	runs      []domain.ReviewRun
	findings  map[string][]domain.ReviewFinding
	delivered []string
	retired   []string
}

func (d *deliveryStore) ListUndeliveredReviewRunsForPR(_ context.Context, id domain.SessionID, prURL string) ([]domain.ReviewRun, error) {
	var out []domain.ReviewRun
	for _, r := range d.runs {
		if r.SessionID == id && r.PRURL == prURL && r.Status == domain.ReviewRunComplete && r.DeliveredAt == nil && r.DeliverySkippedReason == "" && r.AutoInjectReview {
			out = append(out, r)
		}
	}
	return out, nil
}

func (d *deliveryStore) ListUndeliveredReviewRuns(ctx context.Context) ([]domain.ReviewRun, error) {
	var out []domain.ReviewRun
	for _, r := range d.runs {
		if r.Status == domain.ReviewRunComplete && r.DeliveredAt == nil && r.DeliverySkippedReason == "" && r.AutoInjectReview {
			out = append(out, r)
		}
	}
	return out, nil
}

func (d *deliveryStore) ListReviewRunsBySession(_ context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	return append([]domain.ReviewRun(nil), d.runs...), nil
}

func (d *deliveryStore) ListReviewFindingsByRun(_ context.Context, runID string) ([]domain.ReviewFinding, error) {
	return d.findings[runID], nil
}

func (d *deliveryStore) RetireReviewRunDelivery(_ context.Context, id, reason string) error {
	for i := range d.runs {
		if d.runs[i].ID == id && d.runs[i].DeliveredAt == nil && d.runs[i].DeliverySkippedReason == "" {
			d.runs[i].DeliverySkippedReason = reason
			d.retired = append(d.retired, id+"="+reason)
		}
	}
	return nil
}

func (d *deliveryStore) MarkReviewRunDelivered(_ context.Context, id string, at time.Time) (bool, error) {
	for i := range d.runs {
		if d.runs[i].ID == id && d.runs[i].DeliveredAt == nil {
			d.runs[i].DeliveredAt = &at
			d.runs[i].Status = domain.ReviewRunDelivered
			d.delivered = append(d.delivered, id)
			return true, nil
		}
	}
	return false, nil
}

const deliveryPR = "https://github.com/o/r/pull/5"

func newDeliveryFixture(head string) (*Manager, *deliveryStore, *fakeMessenger) {
	st := &deliveryStore{fakeStore: newFakeStore(), findings: map[string][]domain.ReviewFinding{}}
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: deliveryPR, SessionID: "mer-1", Number: 5, Title: "feat: shout", HeadSHA: head}}
	msg := &fakeMessenger{}
	return New(st, msg), st, msg
}

func completedRun(id, sha string, verdict domain.ReviewVerdict, created time.Time) domain.ReviewRun {
	return domain.ReviewRun{ID: id, SessionID: "mer-1", PRURL: deliveryPR, Harness: domain.ReviewerClaudeCode, TargetSHA: sha, Status: domain.ReviewRunComplete, Verdict: verdict, Body: "summary", AutoInjectReview: true, CreatedAt: created}
}

func TestDeliverReviewRunsSendsFindingsOnceWithResolveCommand(t *testing.T) {
	m, st, msg := newDeliveryFixture("sha-1")
	st.runs = []domain.ReviewRun{completedRun("run-1", "sha-1", domain.VerdictChangesRequested, time.Now())}
	st.findings["run-1"] = []domain.ReviewFinding{
		{ID: "f-open", RunID: "run-1", Path: "greet.js", Line: 12, Body: "add tests", Status: domain.ReviewFindingOpen},
		{ID: "f-done", RunID: "run-1", Body: "already handled", Status: domain.ReviewFindingResolved},
	}

	for i := 0; i < 2; i++ {
		if err := m.DeliverReviewRuns(context.Background(), "mer-1", deliveryPR); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}
	if len(msg.msgs) != 1 || len(st.delivered) != 1 {
		t.Fatalf("messages=%d delivered=%v, want exactly one delivery", len(msg.msgs), st.delivered)
	}
	got := msg.msgs[0]
	for _, want := range []string{"requested changes on PR #5", "greet.js:12", "add tests", "Finding ID: f-open", "ao review resolve <finding-id> --note", "Do not reply on GitHub"} {
		if !strings.Contains(got, want) {
			t.Fatalf("delivery missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "already handled") {
		t.Fatalf("a resolved finding was delivered as open work:\n%s", got)
	}
}

func TestDeliverReviewRunsWaitsForAWorkerThatCannotTakeMessages(t *testing.T) {
	m, st, msg := newDeliveryFixture("sha-1")
	st.runs = []domain.ReviewRun{completedRun("run-1", "sha-1", domain.VerdictApproved, time.Now())}
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityWaitingInput
	st.sessions["mer-1"] = rec

	if err := m.DeliverPendingReviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 || len(st.delivered) != 0 || len(st.retired) != 0 {
		t.Fatalf("a worker waiting for input must be retried, not delivered to or retired: msgs=%v retired=%v", msg.msgs, st.retired)
	}

	st.sessions["mer-1"] = working("mer-1")
	if err := m.DeliverPendingReviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "It is not a GitHub approval") {
		t.Fatalf("sweep must deliver the approval with its boundary once the worker is free: %v", msg.msgs)
	}
}

func TestDeliverReviewRunsSkipsStaleReplacedAndReassignedPasses(t *testing.T) {
	now := time.Now()
	m, st, msg := newDeliveryFixture("sha-2")
	st.runs = []domain.ReviewRun{
		completedRun("run-old-head", "sha-1", domain.VerdictChangesRequested, now),
		completedRun("run-replaced", "sha-2", domain.VerdictChangesRequested, now.Add(time.Second)),
		completedRun("run-current", "sha-2", domain.VerdictApproved, now.Add(2*time.Second)),
	}
	if err := m.DeliverReviewRuns(context.Background(), "mer-1", deliveryPR); err != nil {
		t.Fatal(err)
	}
	if len(st.delivered) != 1 || st.delivered[0] != "run-current" || len(msg.msgs) != 1 {
		t.Fatalf("delivered=%v, want only the current head's newest pass", st.delivered)
	}
	// Undeliverable passes are retired, not re-examined by every sweep.
	if strings.Join(st.retired, ",") != "run-old-head=head_moved,run-replaced=replaced_by_newer_pass" {
		t.Fatalf("retired = %v", st.retired)
	}
	if pending, _ := st.ListUndeliveredReviewRuns(context.Background()); len(pending) != 0 {
		t.Fatalf("the next sweep still sees %d pass(es)", len(pending))
	}

	// A PR another session now owns is never delivered to the old owner.
	m2, st2, msg2 := newDeliveryFixture("sha-1")
	st2.prs["mer-1"][0].SessionID = "mer-2"
	st2.runs = []domain.ReviewRun{completedRun("run-1", "sha-1", domain.VerdictApproved, now)}
	if err := m2.DeliverReviewRuns(context.Background(), "mer-1", deliveryPR); err != nil {
		t.Fatal(err)
	}
	if len(msg2.msgs) != 0 {
		t.Fatalf("delivered a pass for a reassigned PR: %v", msg2.msgs)
	}
	if len(st2.retired) != 1 || st2.retired[0] != "run-1=pr_owned_by_other_session" {
		t.Fatalf("a reassigned PR's pass must be retired: %v", st2.retired)
	}
}

// The worker's own reply on a review thread is never nudged back to it as
// review work (#6300, #5574); a thread's opening comment still is.
func TestPRObservationDoesNotNudgeTheWorkersOwnReply(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: deliveryPR, SessionID: "mer-1", HeadSHA: "sha-1"}}
	st.comments[deliveryPR] = []domain.PullRequestComment{
		{ID: "c-root", ThreadID: "t1", Author: "reviewer", File: "a.go", Line: 1, Body: "please fix", AutoInjectReview: true},
		{ID: "c-reply", ThreadID: "t1", Author: "AgentWrapper", File: "a.go", Line: 1, Body: "left as is on purpose", AutoInjectReview: true, OwnReply: true},
	}
	if err := m.ApplyPRObservation(context.Background(), "mer-1", ports.PRObservation{Fetched: true, URL: deliveryPR}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(msg.msgs, "\n---\n")
	if !strings.Contains(joined, "please fix") {
		t.Fatalf("the reviewer's comment must still be delivered:\n%s", joined)
	}
	if strings.Contains(joined, "left as is on purpose") {
		t.Fatalf("the worker's own reply was delivered back to it:\n%s", joined)
	}
}

// A daemon that dies after sending but before stamping the run delivered must
// not resend after restart: the persisted sendOnce entry covers that window.
func TestDeliverReviewRunsDoesNotResendAfterRestartBeforeStamp(t *testing.T) {
	m, st, msg := newDeliveryFixture("sha-1")
	st.runs = []domain.ReviewRun{completedRun("run-1", "sha-1", domain.VerdictApproved, time.Now())}
	if err := m.DeliverReviewRuns(context.Background(), "mer-1", deliveryPR); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.signatures[deliveryPR], "run-1") {
		t.Fatalf("delivery dedup entry was not persisted with the PR: %q", st.signatures[deliveryPR])
	}
	// Simulate the lost stamp, then a fresh process over the same store.
	st.runs[0].DeliveredAt = nil
	st.runs[0].Status = domain.ReviewRunComplete
	restarted := New(st, msg)
	if err := restarted.DeliverReviewRuns(context.Background(), "mer-1", deliveryPR); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("resent after restart: %d messages", len(msg.msgs))
	}
	if st.runs[0].DeliveredAt == nil {
		t.Fatal("the restarted daemon must still stamp the already-sent pass delivered")
	}
}

func TestDeliverReviewRunsRetiresPassesForTerminatedWorkersAndClosedPRs(t *testing.T) {
	m, st, msg := newDeliveryFixture("sha-1")
	st.runs = []domain.ReviewRun{completedRun("run-1", "sha-1", domain.VerdictApproved, time.Now())}
	st.prs["mer-1"][0].Merged = true
	if err := m.DeliverPendingReviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 || len(st.retired) != 1 || st.retired[0] != "run-1=pr_closed" {
		t.Fatalf("msgs=%v retired=%v", msg.msgs, st.retired)
	}

	m2, st2, msg2 := newDeliveryFixture("sha-1")
	st2.runs = []domain.ReviewRun{completedRun("run-2", "sha-1", domain.VerdictApproved, time.Now())}
	rec := working("mer-1")
	rec.IsTerminated = true
	st2.sessions["mer-1"] = rec
	if err := m2.DeliverPendingReviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(msg2.msgs) != 0 || len(st2.retired) != 1 || st2.retired[0] != "run-2=session_terminated" {
		t.Fatalf("msgs=%v retired=%v", msg2.msgs, st2.retired)
	}
}
