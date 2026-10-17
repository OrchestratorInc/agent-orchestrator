package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

// reviewNoticeStore is the durable "review done" notice queue. It is optional
// on the lifecycle store so stores without AO review support keep working.
type reviewNoticeStore interface {
	ListPendingReviewRunNotices(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListSessionsWithPendingReviewRunNotices(ctx context.Context) ([]domain.SessionID, error)
	MarkReviewRunNoticeSent(ctx context.Context, id string) (bool, error)
}

// DeliverReviewDoneNotices sends the worker one message for each of its AO
// review passes that reached a final state (approved, changes requested,
// failed, cancelled) and still owes a notice. The store marks a notice pending
// on the pass's final status write, only while the session receives review
// feedback; a notice is marked sent only after it reached the worker, so a
// suppressed send is retried and a sent one is never repeated, across
// restarts too.
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
		return nil
	}
	runs, err := store.ListPendingReviewRunNotices(ctx, id)
	if err != nil {
		return err
	}
	for _, run := range runs {
		msg, ready, err := m.reviewDoneNotice(ctx, run)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		outcome, err := m.guard.Nudge(ctx, id, msg)
		if outcome != sessionguard.Sent {
			// Busy, awaiting the user, or gone: keep it pending for the next try.
			return err
		}
		if err != nil {
			// The write was attempted; count it as sent rather than risk a
			// duplicate, matching sendOnce.
			slog.Default().WarnContext(ctx, "lifecycle: review done notice write failed", "session", id, "run", run.ID, "err", err)
		}
		if _, err := store.MarkReviewRunNoticeSent(ctx, run.ID); err != nil {
			return err
		}
		delete(m.reviewNoticeSeen, run.ID)
	}
	return nil
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
// first and the count is real. Caller holds reviewNoticeMu.
func (m *Manager) reviewDoneNotice(ctx context.Context, run domain.ReviewRun) (string, bool, error) {
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
			first, seen := m.reviewNoticeSeen[run.ID]
			if !seen {
				m.reviewNoticeSeen[run.ID] = now
				return "", false, nil
			}
			if now.Sub(first) < reviewNoticeObserveGrace {
				return "", false, nil
			}
		}
	}
	return formatReviewDoneNotice(run, reviewURL, inline, observed), true, nil
}

func formatReviewDoneNotice(run domain.ReviewRun, reviewURL string, inline int, observed bool) string {
	posted := run.Status == domain.ReviewRunComplete && run.GithubReviewID != ""
	var msg strings.Builder
	fmt.Fprintf(&msg, "AO review done: %s\n", reviewDoneVerdict(run))
	fmt.Fprintf(&msg, "PR: %s\n", domain.SanitizeControlChars(run.PRURL))
	if run.TargetSHA != "" {
		fmt.Fprintf(&msg, "Head: %s\n", domain.SanitizeControlChars(run.TargetSHA))
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
		msg.WriteString("\nNo review result was recorded for this pass. Request another review if you still need one.")
		return msg.String()
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
