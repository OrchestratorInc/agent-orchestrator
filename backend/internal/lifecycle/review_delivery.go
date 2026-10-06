package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reviewDeliveryStore is the persistence AO-review delivery needs. It stays
// optional on the broad lifecycle store so focused reducer fakes do not have
// to implement it; production SQLite does.
type reviewDeliveryStore interface {
	ListUndeliveredReviewRunsForPR(ctx context.Context, id domain.SessionID, prURL string) ([]domain.ReviewRun, error)
	ListUndeliveredReviewRuns(ctx context.Context) ([]domain.ReviewRun, error)
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListReviewFindingsByRun(ctx context.Context, runID string) ([]domain.ReviewFinding, error)
	MarkReviewRunDelivered(ctx context.Context, id string, deliveredAt time.Time) (bool, error)
}

// reviewApprovalBoundary keeps an AO approval from being mistaken for the
// approval a protected branch may require. AO's reviewer is not an independent
// provider account, so it can never supply one.
const reviewApprovalBoundary = "This approval is AO's internal review only. It is not a GitHub approval: it does not satisfy required or independent reviews or branch protection, and it does not authorize merging. Report the result; merge only when explicitly asked and the project's rules allow it."

// DeliverReviewRuns tells the worker about each completed AO review pass on one
// of its PRs that it has not heard about yet: the verdict, and for requested
// changes every open finding with the command that resolves it. A pass is
// delivered once. It is skipped, and retried by a later call, while the worker
// cannot take a message; it is never delivered when the PR has moved past the
// reviewed commit, the PR now belongs to another session, or a newer pass by
// the same reviewer replaced it.
func (m *Manager) DeliverReviewRuns(ctx context.Context, id domain.SessionID, prURL string) error {
	store, ok := m.store.(reviewDeliveryStore)
	if !ok || m.guard == nil || prURL == "" {
		return nil
	}
	runs, err := store.ListUndeliveredReviewRunsForPR(ctx, id, prURL)
	if err != nil || len(runs) == 0 {
		return err
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if cannotNudge(rec) {
		return nil
	}
	pr, ok, err := m.store.GetPR(ctx, prURL)
	if err != nil || !ok {
		return err
	}
	if pr.SessionID != id || pr.Merged || pr.Closed || pr.HeadSHA == "" {
		return nil
	}
	all, err := store.ListReviewRunsBySession(ctx, id)
	if err != nil {
		return err
	}
	var errs []error
	for _, run := range runs {
		if run.TargetSHA != pr.HeadSHA || replacedByNewerRun(run, all) {
			continue
		}
		findings, err := store.ListReviewFindingsByRun(ctx, run.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		msg := formatReviewRunMessage(pr, run, findings)
		outcome, err := m.sendOnce(ctx, id, prURL, reviewRunDeliveryKey(prURL, run.ID), run.ID, msg, 0, false)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if outcome == sendOnceSuppressed {
			// Nothing reached the worker; leave the pass undelivered so the next
			// call retries it once the worker can take a message.
			continue
		}
		if _, err := store.MarkReviewRunDelivered(ctx, run.ID, m.clock()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DeliverPendingReviews retries every undelivered AO review pass. The daemon
// runs it periodically, because a pass that finished while its worker was
// waiting for input has no other event to bring it back.
func (m *Manager) DeliverPendingReviews(ctx context.Context) error {
	store, ok := m.store.(reviewDeliveryStore)
	if !ok {
		return nil
	}
	runs, err := store.ListUndeliveredReviewRuns(ctx)
	if err != nil {
		return err
	}
	type target struct {
		session domain.SessionID
		prURL   string
	}
	seen := map[target]bool{}
	var errs []error
	for _, run := range runs {
		t := target{run.SessionID, run.PRURL}
		if seen[t] {
			continue
		}
		seen[t] = true
		if err := m.DeliverReviewRuns(ctx, t.session, t.prURL); err != nil {
			errs = append(errs, fmt.Errorf("deliver AO reviews to %s for %s: %w", t.session, t.prURL, err))
		}
	}
	return errors.Join(errs...)
}

// StartReviewDeliverySweep runs DeliverPendingReviews every interval until ctx
// ends. The returned channel closes when the loop has stopped.
func (m *Manager) StartReviewDeliverySweep(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.DeliverPendingReviews(ctx); err != nil && ctx.Err() == nil {
					slog.Default().Warn("lifecycle: AO review delivery sweep failed", "err", err)
				}
			}
		}
	}()
	return done
}

// replacedByNewerRun reports whether the same reviewer finished a later pass on
// the same PR, which makes this one history rather than news for the worker.
func replacedByNewerRun(run domain.ReviewRun, all []domain.ReviewRun) bool {
	for _, other := range all {
		if other.ID == run.ID || other.PRURL != run.PRURL || other.Harness != run.Harness {
			continue
		}
		if other.Status != domain.ReviewRunComplete && other.Status != domain.ReviewRunDelivered {
			continue
		}
		if other.CreatedAt.After(run.CreatedAt) {
			return true
		}
	}
	return false
}

func formatReviewRunMessage(pr domain.PullRequest, run domain.ReviewRun, findings []domain.ReviewFinding) string {
	ident := "your PR"
	if pr.Number > 0 {
		ident = fmt.Sprintf("PR #%d", pr.Number)
		if pr.Title != "" {
			ident += fmt.Sprintf(" %q", domain.SanitizeControlChars(pr.Title))
		}
	}
	reviewer := domain.SanitizeControlChars(string(run.Harness))
	if reviewer == "" {
		reviewer = "reviewer"
	}
	head := domain.SanitizeControlChars(run.TargetSHA)
	if len(head) > 7 {
		head = head[:7]
	}
	var msg strings.Builder
	if run.Verdict == domain.VerdictApproved {
		fmt.Fprintf(&msg, "[AO review] AO's reviewer (%s) approved %s at commit %s.\nPR: %s\n", reviewer, ident, head, domain.SanitizeControlChars(pr.URL))
		if body := strings.TrimSpace(domain.SanitizeControlChars(run.Body)); body != "" {
			fmt.Fprintf(&msg, "\nReviewer summary:\n%s\n", body)
		}
		msg.WriteString("\n" + reviewApprovalBoundary)
		return msg.String()
	}
	open := make([]domain.ReviewFinding, 0, len(findings))
	for _, f := range findings {
		if f.Status == domain.ReviewFindingOpen {
			open = append(open, f)
		}
	}
	fmt.Fprintf(&msg, "[AO review] AO's reviewer (%s) requested changes on %s at commit %s.\nPR: %s\n", reviewer, ident, head, domain.SanitizeControlChars(pr.URL))
	if len(open) > 0 {
		fmt.Fprintf(&msg, "\n%d open finding(s). They are tracked in AO, not as GitHub review threads:\n", len(open))
		for i, f := range open {
			location := "(general)"
			if f.Path != "" {
				location = domain.SanitizeControlChars(f.Path)
				if f.Line > 0 {
					location = fmt.Sprintf("%s:%d", location, f.Line)
				}
			}
			// Finding text is reviewer output shaped by repository content; strip
			// control and escape characters before it reaches the worker's pane.
			fmt.Fprintf(&msg, "\n%d. %s\n%s\n   Finding ID: %s\n", i+1, location, domain.SanitizeControlChars(f.Body), f.ID)
		}
	} else if len(findings) > 0 {
		msg.WriteString("\nEvery finding from this pass has already been resolved.\n")
	}
	if body := strings.TrimSpace(domain.SanitizeControlChars(run.Body)); body != "" {
		fmt.Fprintf(&msg, "\nReviewer summary:\n%s\n", body)
	}
	if len(open) > 0 {
		msg.WriteString("\nFix what needs fixing and push. Then resolve each finding in AO, saying what you changed or why no change is needed:\n  ao review resolve <finding-id> --note \"<how it was handled>\"\nDo not reply on GitHub for these findings.")
	}
	return msg.String()
}

// reviewRunDeliveryKey is the sendOnce key for one AO review pass. It must
// carry the PR URL in the "<type>:<url>:<extra>" shape: only keys that
// reactionKeyTargetsPR matches are persisted, and the persisted entry is what
// stops a restart between the send and the delivered stamp from resending.
func reviewRunDeliveryKey(prURL, runID string) string {
	return "ao-review:" + prURL + ":" + runID
}
