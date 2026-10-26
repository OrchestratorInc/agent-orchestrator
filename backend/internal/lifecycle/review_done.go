package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// reviewNoticeObserveGrace bounds how long a finished pass that posted a
// provider review waits for the SCM observer to see that review. Waiting lets
// the notice report the real inline-comment count and follow the forwarded
// comments; past the grace the notice goes out without them.
const reviewNoticeObserveGrace = 3 * time.Minute

// DefaultReviewNoticeSweepInterval is how often the daemon retries "review
// done" notices that could not be sent yet: the worker was busy or awaiting
// the user, the provider review was not observed yet, or the daemon restarted.
const DefaultReviewNoticeSweepInterval = 30 * time.Second

const reviewNoticeBodyLimit = 4000

// Failed deliveries are retried with a backoff that starts at the sweep
// interval and doubles per failed attempt, capped at reviewNoticeRetryMax.
const (
	reviewNoticeRetryBase = DefaultReviewNoticeSweepInterval
	reviewNoticeRetryMax  = 10 * time.Minute
)

// reviewNoticeStore is the durable "review done" notice queue. It is optional
// on the lifecycle store so stores without AO review support keep working.
type reviewNoticeStore interface {
	ListPendingReviewRunNotices(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListSessionsWithPendingReviewRunNotices(ctx context.Context) ([]domain.SessionID, error)
	MarkReviewRunNoticeSent(ctx context.Context, id string) (bool, error)
	RecordReviewRunNoticeFailure(ctx context.Context, id, sendErr string, at time.Time) (bool, error)
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// reviewNoticeChatStore reads the Chat turn behind one notice delivery
// attempt. A relay into a sleeping Chat session is queued and delivered by a
// background wake, so a successful send there does not yet mean the worker
// got the message.
type reviewNoticeChatStore interface {
	ReviewNoticeChatDelivery(ctx context.Context, sessionID domain.SessionID, clientMessageID string) (domain.TurnState, string, bool, error)
}

// chatNoticeDelivery is where a notice's Chat delivery attempts stand.
type chatNoticeDelivery int

const (
	chatNoticeNotSent   chatNoticeDelivery = iota // no attempt is waiting or delivered
	chatNoticeQueued                              // an attempt waits for the session to wake
	chatNoticeDelivered                           // an attempt reached the agent
	chatNoticeFailed                              // the current attempt's turn failed
)

// reviewNoticeClientID is the Chat client message id of a notice's delivery
// attempt. Each retry after a failure uses a new id, because the old one
// already names the failed message.
func reviewNoticeClientID(run domain.ReviewRun, attempt int) string {
	return fmt.Sprintf("review-done:%s:%d", run.ID, attempt)
}

// chatNoticeDeliveryState checks every delivery attempt of run so far. A
// delivered or still-queued earlier attempt wins over sending again, so a
// relay that drains late never doubles the notice.
func chatNoticeDeliveryState(ctx context.Context, chat reviewNoticeChatStore, id domain.SessionID, run domain.ReviewRun) (chatNoticeDelivery, string, error) {
	state, failure := chatNoticeNotSent, ""
	for attempt := 0; attempt <= run.WorkerNoticeAttempts; attempt++ {
		turn, turnErr, found, err := chat.ReviewNoticeChatDelivery(ctx, id, reviewNoticeClientID(run, attempt))
		if err != nil {
			return chatNoticeNotSent, "", err
		}
		if !found {
			continue
		}
		switch turn {
		case domain.TurnStateQueued:
			state = chatNoticeQueued
		case domain.TurnStateFailed, domain.TurnStateCancelled:
			if attempt == run.WorkerNoticeAttempts && state == chatNoticeNotSent {
				state, failure = chatNoticeFailed, fmt.Sprintf("chat turn %s: %s", turn, turnErr)
			}
		default:
			return chatNoticeDelivered, "", nil
		}
	}
	return state, failure, nil
}

// DeliverReviewDoneNotices sends the worker one message for each of its AO
// review passes that reached a final state (approved, changes requested,
// failed, cancelled) and still owes a notice. The store marks a notice pending
// on the pass's final status write, only while the session receives review
// feedback; a notice is marked sent only after it reached the worker, so a
// suppressed or failed send is retried and a sent one is never repeated,
// across restarts too. A failed send records its error on the run and is
// retried with backoff, at most domain.ReviewWorkerNoticeMaxAttempts times;
// later passes wait behind a notice still retrying, not one that gave up.
func (m *Manager) DeliverReviewDoneNotices(ctx context.Context, id domain.SessionID) error {
	store, ok := m.store.(reviewNoticeStore)
	if !ok || m.guard == nil {
		return nil
	}
	m.reviewNoticeMu.Lock()
	defer m.reviewNoticeMu.Unlock()

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
		m.forgetReviewNoticeWaits(id)
		return nil
	}
	runs, err := store.ListPendingReviewRunNotices(ctx, id)
	if err != nil {
		return err
	}
	chat, chatOK := m.store.(reviewNoticeChatStore)
	isChat := chatOK && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
	var sessionRuns []domain.ReviewRun
	for _, run := range runs {
		// Keep notices in the order the passes were requested: a later pass
		// is not announced ahead of an earlier one that is still waiting for
		// its review to be observed, its retry backoff, or a sleeping Chat
		// session to take it.
		if !m.reviewNoticeRetryDue(run) {
			return nil
		}
		if isChat {
			delivered, waiting, err := m.settleChatNotice(ctx, store, chat, id, run)
			if err != nil || waiting {
				return err
			}
			if delivered {
				continue
			}
		}
		if sessionRuns == nil {
			if sessionRuns, err = store.ListReviewRunsBySession(ctx, id); err != nil {
				return err
			}
		}
		msg, ready, err := m.reviewDoneNotice(ctx, run, sessionRuns)
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
		sendCtx := ctx
		if isChat {
			sendCtx = ports.WithClientMessageID(ctx, reviewNoticeClientID(run, run.WorkerNoticeAttempts))
		}
		outcome, err := m.guard.Nudge(sendCtx, id, msg)
		if errors.Is(err, ports.ErrDeliveryUnconfirmed) {
			// The message reached the worker's pane; only its acceptance was
			// not confirmed, and the messenger already re-sent Enter. Count it
			// as sent so it is not pasted again, and keep the warning on the
			// run for diagnosis.
			slog.Default().WarnContext(ctx, "lifecycle: review done notice delivery unconfirmed", "session", id, "run", run.ID, "err", err)
			if _, recErr := store.RecordReviewRunNoticeFailure(ctx, run.ID, err.Error(), m.clock()); recErr != nil {
				return recErr
			}
			err = nil
		}
		if err != nil {
			// The send failed, including a messenger error the guard reports
			// as Sent: the worker did not get the message. Keep it pending and
			// record why; the sweep retries it after a backoff. A partly
			// written terminal paste may then repeat, which beats losing it.
			// Retries are capped at ReviewWorkerNoticeMaxAttempts: a send
			// reported as failed may still have landed, so a persistent
			// failure must not re-paste forever. After the last attempt the
			// notice reads as failed and keeps the error.
			attempt := run.WorkerNoticeAttempts + 1
			if attempt >= domain.ReviewWorkerNoticeMaxAttempts {
				slog.Default().ErrorContext(ctx, "lifecycle: review done notice gave up", "session", id, "run", run.ID, "attempts", attempt, "outcome", outcome.String(), "err", err)
			} else {
				slog.Default().WarnContext(ctx, "lifecycle: review done notice send failed", "session", id, "run", run.ID, "attempt", attempt, "outcome", outcome.String(), "err", err)
			}
			if _, recErr := store.RecordReviewRunNoticeFailure(ctx, run.ID, err.Error(), m.clock()); recErr != nil {
				return recErr
			}
			return nil
		}
		if outcome != sessionguard.Sent {
			// Busy, awaiting the user, or gone: keep it pending for the next try.
			return nil
		}
		if isChat {
			// A sleeping Chat session queues the relay and wakes in the
			// background; it counts once the message reached the agent.
			delivered, waiting, err := m.settleChatNotice(ctx, store, chat, id, run)
			if err != nil || waiting {
				return err
			}
			if delivered {
				continue
			}
		}
		if _, err := store.MarkReviewRunNoticeSent(ctx, run.ID); err != nil {
			return err
		}
		delete(m.reviewNoticeSeen, run.ID)
	}
	return nil
}

// settleChatNotice applies where run's Chat delivery attempts stand: a
// delivered attempt marks the notice sent; a queued one waits for the wake;
// a failed one records the failure, so the next try uses a new attempt after
// the backoff and counts toward the attempt cap. With no attempt on record it
// reports neither, and the caller sends or keeps its own result.
func (m *Manager) settleChatNotice(ctx context.Context, store reviewNoticeStore, chat reviewNoticeChatStore, id domain.SessionID, run domain.ReviewRun) (delivered, waiting bool, err error) {
	state, failure, err := chatNoticeDeliveryState(ctx, chat, id, run)
	if err != nil {
		return false, false, err
	}
	switch state {
	case chatNoticeDelivered:
		if _, err := store.MarkReviewRunNoticeSent(ctx, run.ID); err != nil {
			return false, false, err
		}
		delete(m.reviewNoticeSeen, run.ID)
		return true, false, nil
	case chatNoticeQueued:
		return false, true, nil
	case chatNoticeFailed:
		slog.Default().WarnContext(ctx, "lifecycle: review done notice chat delivery failed", "session", id, "run", run.ID, "attempt", run.WorkerNoticeAttempts+1, "err", failure)
		if _, err := store.RecordReviewRunNoticeFailure(ctx, run.ID, failure, m.clock()); err != nil {
			return false, false, err
		}
		return false, true, nil
	}
	return false, false, nil
}

// reviewNoticeRetryDue reports whether a notice whose last delivery failed
// has waited out its backoff.
func (m *Manager) reviewNoticeRetryDue(run domain.ReviewRun) bool {
	if run.WorkerNoticeAttempts == 0 || run.WorkerNoticeAttemptedAt == nil {
		return true
	}
	delay := reviewNoticeRetryMax
	if shift := run.WorkerNoticeAttempts - 1; shift < 8 {
		delay = min(reviewNoticeRetryBase<<shift, reviewNoticeRetryMax)
	}
	return !m.clock().Before(run.WorkerNoticeAttemptedAt.Add(delay))
}

// reviewNoticeWait is when a finished pass was first found waiting for its
// provider review to be observed, and whose worker it belongs to.
type reviewNoticeWait struct {
	session domain.SessionID
	since   time.Time
}

// forgetReviewNoticeWaits drops the observe waits of a worker that can no
// longer take a notice. Caller holds reviewNoticeMu.
func (m *Manager) forgetReviewNoticeWaits(id domain.SessionID) {
	for runID, wait := range m.reviewNoticeSeen {
		if wait.session == id {
			delete(m.reviewNoticeSeen, runID)
		}
	}
}

// SweepReviewDoneNotices retries every worker's pending notices once.
func (m *Manager) SweepReviewDoneNotices(ctx context.Context) {
	store, ok := m.store.(reviewNoticeStore)
	if !ok {
		return
	}
	ids, err := store.ListSessionsWithPendingReviewRunNotices(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "lifecycle: list pending review done notices", "err", err)
		return
	}
	for _, id := range ids {
		if err := m.DeliverReviewDoneNotices(ctx, id); err != nil {
			slog.Default().WarnContext(ctx, "lifecycle: deliver review done notices", "session", id, "err", err)
		}
	}
}

// StartReviewDoneSweep runs SweepReviewDoneNotices now and then every
// interval (DefaultReviewNoticeSweepInterval when not positive) until ctx is
// cancelled. The returned channel closes on exit.
func (m *Manager) StartReviewDoneSweep(ctx context.Context, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = DefaultReviewNoticeSweepInterval
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			m.SweepReviewDoneNotices(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

// reviewDoneNotice renders run's notice and reports whether it may be sent
// now. A pass that posted a provider review waits (up to the grace) until the
// SCM observer has recorded that review, so its inline comments are forwarded
// first and the count is real. sessionRuns are the worker's review passes,
// used to tell the worker when a newer pass exists. Caller holds
// reviewNoticeMu.
func (m *Manager) reviewDoneNotice(ctx context.Context, run domain.ReviewRun, sessionRuns []domain.ReviewRun) (string, bool, error) {
	posted := run.Status == domain.ReviewRunComplete && run.GithubReviewID != ""
	inline, observed := 0, false
	reviewURL := ""
	if posted {
		comments, err := m.store.ListPRComments(ctx, run.PRURL)
		if err != nil {
			return "", false, fmt.Errorf("list comments for %s: %w", run.PRURL, err)
		}
		for _, c := range comments {
			if strings.TrimSpace(c.ReviewID) == run.GithubReviewID {
				inline++
			}
		}
		reviews, err := m.store.ListPRReviews(ctx, run.PRURL)
		if err != nil {
			return "", false, fmt.Errorf("list reviews for %s: %w", run.PRURL, err)
		}
		for _, r := range reviews {
			if r.ID == run.GithubReviewID || strings.HasSuffix(r.URL, "#pullrequestreview-"+run.GithubReviewID) {
				observed = true
				reviewURL = r.URL
				break
			}
		}
		observed = observed || inline > 0
		if !observed {
			now := m.clock()
			wait, seen := m.reviewNoticeSeen[run.ID]
			if !seen {
				m.reviewNoticeSeen[run.ID] = reviewNoticeWait{session: run.SessionID, since: now}
				return "", false, nil
			}
			if now.Sub(wait.since) < reviewNoticeObserveGrace {
				return "", false, nil
			}
		}
	}
	later := reviewNoticeLater{newer: newerReviewRun(run, sessionRuns)}
	pr, ok, err := m.store.GetPR(ctx, run.PRURL)
	if err != nil {
		return "", false, fmt.Errorf("get PR %s: %w", run.PRURL, err)
	}
	if ok {
		later.head = pr.HeadSHA
	}
	return formatReviewDoneNotice(run, reviewURL, inline, observed, later), true, nil
}

// reviewNoticeLater is what happened on the PR after a pass started: its
// current head and the newest later pass, if any. It keeps a notice for an
// earlier commit from reading as new work.
type reviewNoticeLater struct {
	head  string
	newer *domain.ReviewRun
}

// headMoved reports whether the PR's head is no longer the commit run reviewed.
func (l reviewNoticeLater) headMoved(run domain.ReviewRun) bool {
	return l.head != "" && run.TargetSHA != "" && l.head != run.TargetSHA
}

// newerReviewRun returns the newest pass on the same PR requested after run.
func newerReviewRun(run domain.ReviewRun, runs []domain.ReviewRun) *domain.ReviewRun {
	var newest *domain.ReviewRun
	for i := range runs {
		r := &runs[i]
		if r.ID == run.ID || r.PRURL != run.PRURL || !reviewRunAfter(*r, run) {
			continue
		}
		if newest == nil || reviewRunAfter(*r, *newest) {
			newest = r
		}
	}
	return newest
}

func reviewRunAfter(a, b domain.ReviewRun) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID > b.ID
}

// laterNote tells the worker that run is about an earlier commit or has been
// followed by a newer pass. It is empty when neither is true.
func (l reviewNoticeLater) laterNote(run domain.ReviewRun) string {
	var parts []string
	if l.headMoved(run) {
		parts = append(parts, fmt.Sprintf("This review is about an earlier commit (%s); the PR head is now %s.", shortNoticeSHA(run.TargetSHA), shortNoticeSHA(l.head)))
	}
	if n := l.newer; n != nil {
		var state string
		switch n.Status {
		case domain.ReviewRunRunning:
			state = "is running"
		case domain.ReviewRunComplete:
			state = "finished: " + reviewDoneVerdict(*n)
		case domain.ReviewRunCancelled:
			if n.Body == domain.ReviewRunCancelledByUserReason {
				state = "was cancelled by the user"
			} else {
				state = "was cancelled"
			}
		default:
			state = "ended: " + reviewDoneVerdict(*n)
		}
		parts = append(parts, fmt.Sprintf("A newer review of this PR (run %s, head %s) %s.", n.ID, shortNoticeSHA(n.TargetSHA), state))
	}
	return strings.Join(parts, " ")
}

func shortNoticeSHA(sha string) string {
	sha = domain.SanitizeControlChars(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func formatReviewDoneNotice(run domain.ReviewRun, reviewURL string, inline int, observed bool, later reviewNoticeLater) string {
	posted := run.Status == domain.ReviewRunComplete && run.GithubReviewID != ""
	note := later.laterNote(run)
	var msg strings.Builder
	fmt.Fprintf(&msg, "AO review done: %s\n", reviewDoneVerdict(run))
	fmt.Fprintf(&msg, "PR: %s\n", domain.SanitizeControlChars(run.PRURL))
	if run.TargetSHA != "" {
		fmt.Fprintf(&msg, "Reviewed commit: %s\n", domain.SanitizeControlChars(run.TargetSHA))
	}
	if later.headMoved(run) {
		fmt.Fprintf(&msg, "PR head now: %s\n", domain.SanitizeControlChars(later.head))
	}
	reviewer := string(run.Harness)
	if run.ReviewerModel != "" {
		reviewer += " (model " + run.ReviewerModel + ")"
	}
	fmt.Fprintf(&msg, "Reviewer: %s\n", domain.SanitizeControlChars(reviewer))
	fmt.Fprintf(&msg, "Run: %s\n", run.ID)
	if run.Status == domain.ReviewRunComplete {
		switch {
		case reviewURL != "":
			fmt.Fprintf(&msg, "Review: %s\n", domain.SanitizeControlChars(reviewURL))
		case posted:
			fmt.Fprintf(&msg, "Review ID: %s\n", domain.SanitizeControlChars(run.GithubReviewID))
		default:
			msg.WriteString("Review: not posted to the PR\n")
		}
		if posted && observed {
			fmt.Fprintf(&msg, "Inline comments: %d\n", inline)
		}
	}

	body := strings.TrimSpace(domain.SanitizeControlChars(run.Body))
	if r := []rune(body); len(r) > reviewNoticeBodyLimit {
		body = string(r[:reviewNoticeBodyLimit]) + "\n[truncated; see the review on the PR]"
	}
	switch run.Status {
	case domain.ReviewRunFailed, domain.ReviewRunCancelled:
		if body != "" {
			fmt.Fprintf(&msg, "Reason: %s\n", body)
		}
		msg.WriteString("\nNo review result was recorded for this pass. ")
		switch run.Body {
		case domain.ReviewRunSupersededReason:
			// The trigger that superseded this pass already started a review
			// of the newer head; re-triggering would race it.
			msg.WriteString("A review of the newer head is already under way; wait for its own review done message instead of requesting another review.")
		case domain.ReviewRunCancelledByUserReason:
			msg.WriteString("The user stopped this review on purpose; do not request another unless they ask.")
		default:
			msg.WriteString("Request another review if you still need one.")
		}
		if note != "" && run.Body != domain.ReviewRunSupersededReason {
			msg.WriteString(" " + note)
		}
		return msg.String()
	}
	if note != "" {
		return formatLaterReviewDoneNotice(&msg, run, body, posted, observed, inline, note)
	}
	// Changes requested with inline comments: the required changes are those
	// comments, forwarded as their own review-feedback messages. The summary is
	// not repeated here so it cannot turn optional suggestions into required
	// work.
	if run.Verdict == domain.VerdictChangesRequested && posted && observed && inline > 0 {
		fmt.Fprintf(&msg, "\nThe %d inline comment(s) on this review are delivered as separate review-feedback messages. This review round is complete: address them, push once, and request another review if you need one.", inline)
		return msg.String()
	}
	if body != "" {
		fmt.Fprintf(&msg, "\nReview body:\n%s\n", body)
	}
	msg.WriteString("\n")
	if posted && !observed {
		msg.WriteString("AO has not observed this review on the PR yet; any inline comments on it arrive as separate review-feedback messages. ")
	}
	if run.Verdict == domain.VerdictChangesRequested {
		msg.WriteString("Address the requested changes, push once, and request another review if you need one.")
	} else {
		msg.WriteString("An AO approval is not a GitHub approval and does not authorize merging.")
		if inline > 0 {
			fmt.Fprintf(&msg, " Its %d inline comment(s) are delivered as separate review-feedback messages.", inline)
		}
	}
	return msg.String()
}

func reviewDoneVerdict(run domain.ReviewRun) string {
	switch run.Status {
	case domain.ReviewRunFailed:
		return "failed"
	case domain.ReviewRunCancelled:
		return "cancelled"
	}
	if run.Verdict == domain.VerdictChangesRequested {
		return "changes requested"
	}
	return "approved"
}

// formatLaterReviewDoneNotice finishes the notice of a completed pass that is
// about an earlier commit or has a newer pass: its findings are context to
// check against the current head, not new work by themselves.
func formatLaterReviewDoneNotice(msg *strings.Builder, run domain.ReviewRun, body string, posted, observed bool, inline int, note string) string {
	if body != "" && (run.Verdict != domain.VerdictChangesRequested || !posted || !observed || inline == 0) {
		fmt.Fprintf(msg, "\nReview body:\n%s\n", body)
	}
	msg.WriteString("\n" + note)
	if inline > 0 {
		fmt.Fprintf(msg, " Its %d inline comment(s) are delivered as separate review-feedback messages.", inline)
	}
	if run.Verdict == domain.VerdictChangesRequested {
		msg.WriteString(" Change code only for findings that still apply to the current head.")
	} else {
		msg.WriteString(" This approval covers the reviewed commit only; an AO approval is not a GitHub approval and does not authorize merging.")
	}
	return msg.String()
}
