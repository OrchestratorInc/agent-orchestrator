package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// noticeStore adds the durable "review done" queue to the lifecycle fake.
type noticeStore struct {
	*fakeStore
	runs []domain.ReviewRun
}

func (s *noticeStore) ListPendingReviewRunNotices(_ context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	var out []domain.ReviewRun
	for _, run := range s.runs {
		if run.SessionID == id && run.WorkerNotice == domain.ReviewWorkerNoticePending {
			out = append(out, run)
		}
	}
	return out, nil
}

func (s *noticeStore) ListSessionsWithPendingReviewRunNotices(context.Context) ([]domain.SessionID, error) {
	seen := map[domain.SessionID]bool{}
	var out []domain.SessionID
	for _, run := range s.runs {
		if run.WorkerNotice == domain.ReviewWorkerNoticePending && !seen[run.SessionID] {
			seen[run.SessionID] = true
			out = append(out, run.SessionID)
		}
	}
	return out, nil
}

func (s *noticeStore) MarkReviewRunNoticeSent(_ context.Context, id string) (bool, error) {
	for i := range s.runs {
		if s.runs[i].ID == id && s.runs[i].WorkerNotice == domain.ReviewWorkerNoticePending {
			s.runs[i].WorkerNotice = domain.ReviewWorkerNoticeSent
			return true, nil
		}
	}
	return false, nil
}

func (s *noticeStore) RecordReviewRunNoticeFailure(_ context.Context, id, sendErr string, at time.Time) (bool, error) {
	for i := range s.runs {
		if s.runs[i].ID == id && s.runs[i].WorkerNotice == domain.ReviewWorkerNoticePending {
			s.runs[i].WorkerNoticeAttempts++
			s.runs[i].WorkerNoticeError = sendErr
			s.runs[i].WorkerNoticeAttemptedAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (s *noticeStore) ListReviewRunsBySession(_ context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	var out []domain.ReviewRun
	for _, run := range s.runs {
		if run.SessionID == id {
			out = append(out, run)
		}
	}
	return out, nil
}

func (s *noticeStore) run(id string) domain.ReviewRun {
	for _, run := range s.runs {
		if run.ID == id {
			return run
		}
	}
	return domain.ReviewRun{}
}

func (s *noticeStore) notice(id string) domain.ReviewWorkerNotice {
	for _, run := range s.runs {
		if run.ID == id {
			return run.WorkerNotice
		}
	}
	return ""
}

const donePR = "https://github.com/o/r/pull/7"

func newNoticeManager(t *testing.T) (*Manager, *noticeStore, *fakeMessenger) {
	t.Helper()
	st := &noticeStore{fakeStore: newFakeStore()}
	msg := &fakeMessenger{}
	m := New(st, msg)
	st.sessions["w-1"] = working("w-1")
	return m, st, msg
}

func doneRun(id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, githubReviewID, body string) domain.ReviewRun {
	return domain.ReviewRun{
		ID: id, SessionID: "w-1", Harness: "codex", ReviewerModel: "gpt-5.5",
		PRURL: donePR, TargetSHA: "abc123", Status: status, Verdict: verdict,
		GithubReviewID: githubReviewID, Body: body, AutoInjectReview: true,
		WorkerNotice: domain.ReviewWorkerNoticePending,
	}
}

func observedReview(githubReviewID string) domain.PullRequestReview {
	return domain.PullRequestReview{ID: "PRR_node", Author: "me", URL: donePR + "#pullrequestreview-" + githubReviewID, AutoInjectReview: true}
}

func assertContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("message missing %q:\n%s", want, got)
		}
	}
}

// The reported case: a reviewer approves with only a summary (no inline
// comments), so nothing was forwarded. The worker gets one done message.
func TestReviewDoneNotice_ApprovedWithoutInlineComments(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunComplete, domain.VerdictApproved, "555", "Looks good. Optional: rename x.")}
	st.reviews[donePR] = []domain.PullRequestReview{observedReview("555")}

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one done message, got %d: %v", len(msg.msgs), msg.msgs)
	}
	assertContains(t, msg.msgs[0],
		"AO review done: approved",
		"PR: "+donePR,
		"Reviewed commit: abc123",
		"Reviewer: codex (model gpt-5.5)",
		"Run: run-1",
		"Review: "+donePR+"#pullrequestreview-555",
		"Inline comments: 0",
		"Looks good. Optional: rename x.",
		"not a GitHub approval",
	)
	if got := st.notice("run-1"); got != domain.ReviewWorkerNoticeSent {
		t.Fatalf("notice = %q, want sent", got)
	}

	// Another delivery pass, and a restarted daemon over the same store, must
	// not repeat it.
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	restarted := New(st, msg)
	restarted.SweepReviewDoneNotices(ctx)
	if len(msg.msgs) != 1 {
		t.Fatalf("done message repeated: %v", msg.msgs)
	}
}

// The reviewer submits before the SCM observer sees its review: the notice
// waits, then goes out from the PR observation that records the review.
func TestReviewDoneNotice_WaitsForObservedReview(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunComplete, domain.VerdictApproved, "555", "LGTM")}

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("notice sent before the review was observed: %v", msg.msgs)
	}

	st.reviews[donePR] = []domain.PullRequestReview{observedReview("555")}
	if err := m.ApplyPRObservation(ctx, "w-1", ports.PRObservation{Fetched: true, URL: donePR}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want done message from the PR observation, got %v", msg.msgs)
	}
	assertContains(t, msg.msgs[0], "AO review done: approved", "Inline comments: 0")
}

// Changes requested: the notice comes after the forwarded inline comments in
// the same observation and names how many there are, without repeating the
// summary.
func TestReviewDoneNotice_ChangesRequestedFollowsInlineComments(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunComplete, domain.VerdictChangesRequested, "777", "Summary with optional ideas")}
	st.reviews[donePR] = []domain.PullRequestReview{observedReview("777")}
	st.comments[donePR] = []domain.PullRequestComment{
		{ID: "c1", ThreadID: "T1", ReviewID: "777", Author: "me", File: "a.go", Line: 3, Body: "nil check", AutoInjectReview: true},
		{ID: "c2", ThreadID: "T2", ReviewID: "777", Author: "me", File: "b.go", Line: 9, Body: "missing test", AutoInjectReview: true},
	}

	if err := m.ApplyPRObservation(ctx, "w-1", ports.PRObservation{Fetched: true, URL: donePR}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) < 2 {
		t.Fatalf("want comment feedback then done message, got %v", msg.msgs)
	}
	last := msg.msgs[len(msg.msgs)-1]
	assertContains(t, last, "AO review done: changes requested", "Inline comments: 2", "This review round is complete")
	if strings.Contains(last, "optional ideas") {
		t.Fatalf("done message repeated the review summary:\n%s", last)
	}
	for _, earlier := range msg.msgs[:len(msg.msgs)-1] {
		if strings.Contains(earlier, "AO review done") {
			t.Fatalf("done message was not last: %v", msg.msgs)
		}
	}
}

func TestReviewDoneNotice_FailedAndCancelledSendImmediately(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	st.runs = []domain.ReviewRun{
		doneRun("run-f", domain.ReviewRunFailed, domain.VerdictNone, "", "reviewer exited before submitting a result"),
		doneRun("run-c", domain.ReviewRunCancelled, domain.VerdictNone, "", "cancelled by user"),
	}
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("want two done messages, got %v", msg.msgs)
	}
	assertContains(t, msg.msgs[0], "AO review done: failed", "Run: run-f", "Reason: reviewer exited before submitting a result")
	assertContains(t, msg.msgs[1], "AO review done: cancelled", "Run: run-c", "Reason: cancelled by user")
	for _, m := range msg.msgs {
		if strings.Contains(m, "Inline comments") {
			t.Fatalf("failed/cancelled notice reported inline comments:\n%s", m)
		}
	}
}

// A provider review AO never observes (observer off, provider lag) must not
// hold the notice forever.
func TestReviewDoneNotice_SendsAfterGraceWhenReviewNotObserved(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunComplete, domain.VerdictChangesRequested, "777", "Fix the nil check")}

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(reviewNoticeObserveGrace - time.Second)
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("notice sent inside the grace: %v", msg.msgs)
	}
	now = now.Add(2 * time.Second)
	m.SweepReviewDoneNotices(ctx)
	if len(msg.msgs) != 1 {
		t.Fatalf("want notice after the grace, got %v", msg.msgs)
	}
	assertContains(t, msg.msgs[0], "AO review done: changes requested", "Review ID: 777", "has not observed this review", "Fix the nil check")
	if strings.Contains(msg.msgs[0], "Inline comments:") {
		t.Fatalf("unobserved review reported an inline count:\n%s", msg.msgs[0])
	}
}

// A worker waiting on the user is not interrupted; the notice stays owed and
// goes out once the worker can take it.
func TestReviewDoneNotice_SuppressedSendStaysPending(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	rec := st.sessions["w-1"]
	rec.Activity.State = domain.ActivityWaitingInput
	st.sessions["w-1"] = rec
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunCancelled, domain.VerdictNone, "", "cancelled by user")}

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 || st.notice("run-1") != domain.ReviewWorkerNoticePending {
		t.Fatalf("suppressed notice: msgs=%v notice=%q", msg.msgs, st.notice("run-1"))
	}
	rec.Activity.State = domain.ActivityIdle
	st.sessions["w-1"] = rec
	m.SweepReviewDoneNotices(ctx)
	if len(msg.msgs) != 1 || st.notice("run-1") != domain.ReviewWorkerNoticeSent {
		t.Fatalf("notice not delivered after resume: msgs=%v notice=%q", msg.msgs, st.notice("run-1"))
	}
}

// --rerun records a second pass on the same head; each pass gets its own
// message, in the order the passes were requested.
func TestReviewDoneNotice_RerunGetsItsOwnMessage(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	first := doneRun("run-1", domain.ReviewRunComplete, domain.VerdictApproved, "555", "first pass")
	first.WorkerNotice = domain.ReviewWorkerNoticeSent
	st.runs = []domain.ReviewRun{first, doneRun("run-2", domain.ReviewRunComplete, domain.VerdictApproved, "556", "second pass")}
	st.reviews[donePR] = []domain.PullRequestReview{observedReview("555"), observedReview("556")}

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want only the rerun's message, got %v", msg.msgs)
	}
	assertContains(t, msg.msgs[0], "Run: run-2", "second pass")
}

// No pending notice (feedback delivery off, --no-inject on such a session, or
// a pass from before notices existed) means no message.
func TestReviewDoneNotice_NothingOwedSendsNothing(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	run := doneRun("run-1", domain.ReviewRunComplete, domain.VerdictApproved, "555", "LGTM")
	run.WorkerNotice = domain.ReviewWorkerNoticeNone
	st.runs = []domain.ReviewRun{run}
	st.reviews[donePR] = []domain.PullRequestReview{observedReview("555")}

	m.SweepReviewDoneNotices(ctx)
	if err := m.ApplyPRObservation(ctx, "w-1", ports.PRObservation{Fetched: true, URL: donePR}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("message sent for a pass that owes none: %v", msg.msgs)
	}
}

// A pass replaced by a newer head's trigger, or stopped by the user, must not
// send the worker off to request another review: the newer pass is already
// running, and the user's stop was deliberate.
func TestReviewDoneNotice_SupersededAndUserCancelledDoNotAskForAnotherReview(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	st.runs = []domain.ReviewRun{
		doneRun("run-s", domain.ReviewRunFailed, domain.VerdictNone, "", domain.ReviewRunSupersededReason),
		doneRun("run-u", domain.ReviewRunCancelled, domain.VerdictNone, "", domain.ReviewRunCancelledByUserReason),
		doneRun("run-x", domain.ReviewRunFailed, domain.VerdictNone, "", "reviewer exited before submitting a result"),
	}
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 3 {
		t.Fatalf("want three done messages, got %v", msg.msgs)
	}
	assertContains(t, msg.msgs[0], "AO review done: failed", "already under way", "instead of requesting another review")
	assertContains(t, msg.msgs[1], "AO review done: cancelled", "stopped this review on purpose", "do not request another unless they ask")
	for _, m := range msg.msgs[:2] {
		if strings.Contains(m, "Request another review if you still need one") {
			t.Fatalf("superseded/user-cancelled notice asks for another review:\n%s", m)
		}
	}
	assertContains(t, msg.msgs[2], "Request another review if you still need one")
}

// Notices go out oldest first: a later pass is not announced while an
// earlier one still waits for its provider review to be observed.
func TestReviewDoneNotice_KeepsOldestFirstOrder(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	st.runs = []domain.ReviewRun{
		doneRun("run-1", domain.ReviewRunComplete, domain.VerdictApproved, "555", "first"),
		doneRun("run-2", domain.ReviewRunCancelled, domain.VerdictNone, "", "cancelled by user"),
	}
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("later notice sent ahead of a waiting earlier one: %v", msg.msgs)
	}
	st.reviews[donePR] = []domain.PullRequestReview{observedReview("555")}
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[0], "Run: run-1") || !strings.Contains(msg.msgs[1], "Run: run-2") {
		t.Fatalf("want run-1 then run-2, got %v", msg.msgs)
	}
}

// A worker that ends while a pass waits for its review to be observed does
// not leave the wait behind.
func TestReviewDoneNotice_ForgetsWaitsOfEndedWorkers(t *testing.T) {
	m, st, _ := newNoticeManager(t)
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunComplete, domain.VerdictApproved, "555", "LGTM")}
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(m.reviewNoticeSeen) != 1 {
		t.Fatalf("waits = %v, want the run waiting", m.reviewNoticeSeen)
	}
	rec := st.sessions["w-1"]
	rec.IsTerminated = true
	st.sessions["w-1"] = rec
	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(m.reviewNoticeSeen) != 0 {
		t.Fatalf("waits = %v, want none after the worker ended", m.reviewNoticeSeen)
	}
}

// A send that fails (the guard reports the messenger error as Sent plus an
// error) did not reach the worker: the notice stays pending, the error is kept
// on the run, and the sweep retries it after a doubling backoff.
func TestReviewDoneNotice_FailedSendStaysPendingAndRetriesWithBackoff(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunCancelled, domain.VerdictNone, "", "reviewer exited before submitting a result")}
	msg.err = errors.New("no chat controller")

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	got := st.run("run-1")
	if got.WorkerNotice != domain.ReviewWorkerNoticePending || got.WorkerNoticeAttempts != 1 || !strings.Contains(got.WorkerNoticeError, "no chat controller") || got.WorkerNoticeAttemptedAt == nil || !got.WorkerNoticeAttemptedAt.Equal(now) {
		t.Fatalf("after a failed send: %+v", got)
	}

	// Inside the first backoff nothing is tried.
	now = now.Add(reviewNoticeRetryBase - time.Second)
	m.SweepReviewDoneNotices(ctx)
	if got := st.run("run-1"); got.WorkerNoticeAttempts != 1 {
		t.Fatalf("retried inside the backoff: attempts=%d", got.WorkerNoticeAttempts)
	}
	// After it, the retry fails again and the next wait doubles.
	now = now.Add(2 * time.Second)
	m.SweepReviewDoneNotices(ctx)
	if got := st.run("run-1"); got.WorkerNoticeAttempts != 2 {
		t.Fatalf("attempts = %d, want a second try after the backoff", got.WorkerNoticeAttempts)
	}
	now = now.Add(reviewNoticeRetryBase + time.Second)
	msg.err = nil
	m.SweepReviewDoneNotices(ctx)
	if len(msg.msgs) != 0 {
		t.Fatalf("retried before the doubled backoff: %v", msg.msgs)
	}
	now = now.Add(reviewNoticeRetryBase)
	m.SweepReviewDoneNotices(ctx)
	if len(msg.msgs) != 1 || st.notice("run-1") != domain.ReviewWorkerNoticeSent {
		t.Fatalf("want the notice sent once the send works: msgs=%v notice=%q", msg.msgs, st.notice("run-1"))
	}
	if got := st.run("run-1"); got.WorkerNoticeAttempts != 2 || got.WorkerNoticeError == "" {
		t.Fatalf("the last failure should stay visible after delivery: %+v", got)
	}
}

func TestReviewDoneNotice_RetryBackoffIsCapped(t *testing.T) {
	m, _, _ := newNoticeManager(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	at := now.Add(-reviewNoticeRetryMax)
	run := domain.ReviewRun{WorkerNoticeAttempts: 40, WorkerNoticeAttemptedAt: &at}
	if !m.reviewNoticeRetryDue(run) {
		t.Fatal("backoff should be capped at reviewNoticeRetryMax")
	}
	at = now.Add(-reviewNoticeRetryMax + time.Second)
	if m.reviewNoticeRetryDue(run) {
		t.Fatal("retried before the capped backoff")
	}
}

// A changes-requested notice for a commit the PR has moved past names both
// commits and does not present its findings as new work.
func TestReviewDoneNotice_EarlierCommitIsNotNewWork(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	st.prs["w-1"] = []domain.PullRequest{{URL: donePR, HeadSHA: "def4567890"}}
	st.runs = []domain.ReviewRun{doneRun("run-1", domain.ReviewRunComplete, domain.VerdictChangesRequested, "777", "Summary")}
	st.reviews[donePR] = []domain.PullRequestReview{observedReview("777")}
	st.comments[donePR] = []domain.PullRequestComment{{ID: "c1", ThreadID: "T1", ReviewID: "777", File: "a.go", Line: 3, Body: "nil check", AutoInjectReview: true}}

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one notice, got %v", msg.msgs)
	}
	assertContains(t, msg.msgs[0], "Reviewed commit: abc123", "PR head now: def4567890", "about an earlier commit (abc123); the PR head is now def4567", "still apply to the current head")
	if strings.Contains(msg.msgs[0], "This review round is complete") {
		t.Fatalf("stale notice asked for the round's work:\n%s", msg.msgs[0])
	}
}

// A notice for a pass that already has a newer pass on the PR says so.
func TestReviewDoneNotice_NamesANewerPass(t *testing.T) {
	m, st, msg := newNoticeManager(t)
	newer := doneRun("run-2", domain.ReviewRunRunning, domain.VerdictNone, "", "")
	newer.TargetSHA = "def4567890"
	newer.CreatedAt = time.Date(2026, 10, 22, 12, 5, 0, 0, time.UTC)
	newer.WorkerNotice = domain.ReviewWorkerNoticeNone
	old := doneRun("run-1", domain.ReviewRunComplete, domain.VerdictApproved, "555", "LGTM")
	old.CreatedAt = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	st.runs = []domain.ReviewRun{old, newer}
	st.reviews[donePR] = []domain.PullRequestReview{observedReview("555")}

	if err := m.DeliverReviewDoneNotices(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one notice, got %v", msg.msgs)
	}
	assertContains(t, msg.msgs[0], "AO review done: approved", "A newer review of this PR (run run-2, head def4567) is running.", "covers the reviewed commit only", "LGTM")
}
