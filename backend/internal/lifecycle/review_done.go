package lifecycle

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// DefaultReviewNoticeSweepInterval is how often the daemon retries "review
// done" notices that could not be sent yet: the session was busy or awaiting
// the user, or the daemon restarted.
const DefaultReviewNoticeSweepInterval = 30 * time.Second

const reviewNoticeObserveGrace = 3 * time.Minute

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
}

// reviewNoticeChatStore reads the Chat turn behind a notice or feedback
// delivery attempt. A relay into a sleeping Chat session is queued and delivered by a
// background wake, so a successful send there does not yet mean the worker
// got the message.
type reviewNoticeChatStore interface {
	ReviewNoticeChatDelivery(ctx context.Context, sessionID domain.SessionID, clientMessageID string) (state domain.TurnState, failure string, started, found bool, err error)
}

// chatNoticeDelivery is where a notice's Chat delivery attempts stand.
type chatNoticeDelivery int

const (
	chatNoticeNotSent   chatNoticeDelivery = iota // no attempt is waiting or delivered
	chatNoticeQueued                              // an attempt waits for the session to wake
	chatNoticeDelivered                           // an attempt reached the agent or the user removed it
	chatNoticeFailed                              // the current attempt failed before starting
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
		turn, turnErr, started, found, err := chat.ReviewNoticeChatDelivery(ctx, id, reviewNoticeClientID(run, attempt))
		if err != nil {
			return chatNoticeNotSent, "", err
		}
		if !found {
			continue
		}
		if started {
			return chatNoticeDelivered, "", nil
		}
		switch turn {
		case domain.TurnStateQueued:
			state = chatNoticeQueued
		case domain.TurnStateFailed, domain.TurnStateInterrupted:
			if attempt == run.WorkerNoticeAttempts && state == chatNoticeNotSent {
				state, failure = chatNoticeFailed, fmt.Sprintf("chat turn %s: %s", turn, turnErr)
			}
		case domain.TurnStateRunning, domain.TurnStateCompleted, domain.TurnStateRecovered, domain.TurnStateCancelled:
			// Cancelled means the user removed the queued item; leave it removed.
			return chatNoticeDelivered, "", nil
		}
	}
	return state, failure, nil
}

// DeliverReviewDoneNotices delivers durable terminal outcomes to the requesting session.
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
	chat, chatOK := m.store.(reviewNoticeChatStore)
	isChat := chatOK && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
	for _, run := range runs {
		// Keep notices in the order the passes were requested: a later pass
		// waits for earlier deliveries in retry backoff or a sleeping Chat session.
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
		msg, ready, err := m.reviewDoneMessage(ctx, run)
		if err != nil || !ready {
			return err
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

func reviewFeedbackClientID(key, sig string, attempt int) string {
	return fmt.Sprintf("review-feedback:%x:%d", sha256.Sum256([]byte(key+"\x00"+sig)), attempt)
}

func chatReviewFeedbackState(ctx context.Context, chat reviewNoticeChatStore, id domain.SessionID, key, sig string, attempt int) (delivered, queued bool, err error) {
	turn, _, started, found, err := chat.ReviewNoticeChatDelivery(ctx, id, reviewFeedbackClientID(key, sig, attempt))
	delivered = found && (started || turn == domain.TurnStateRunning || turn == domain.TurnStateCompleted || turn == domain.TurnStateRecovered || turn == domain.TurnStateCancelled)
	return delivered, found && !delivered && turn == domain.TurnStateQueued, err
}

// Caller holds react.mu. A queued Chat relay is an attempt, not delivery.
// Its stable id lets another observation or a restart settle it without a
// second paste. Only turns that never started retry within the attempt cap.
func (m *Manager) sendChatReviewFeedbackLocked(ctx context.Context, chat reviewNoticeChatStore, id domain.SessionID, prURL, key, sig, msg string, maxAttempts int) (sendOnceOutcome, error) {
	attempts := m.react.attempts[key]
	if attempts > 0 {
		delivered, queued, err := chatReviewFeedbackState(ctx, chat, id, key, sig, attempts-1)
		if err != nil || queued {
			return sendOnceSuppressed, err
		}
		if delivered {
			if m.react.seen[key] == sig {
				return sendOnceAccounted, nil
			}
			m.react.seen[key] = sig
			return sendOnceAccounted, m.persistPRSignaturesLocked(ctx, prURL)
		}
	}
	if m.react.seen[key] == sig || (maxAttempts > 0 && attempts >= maxAttempts) {
		return sendOnceAccounted, nil
	}
	outcome, sendErr := m.guard.Nudge(ports.WithClientMessageID(ctx, reviewFeedbackClientID(key, sig, attempts)), id, msg)
	if outcome != sessionguard.Sent {
		return sendOnceSuppressed, sendErr
	}
	m.react.attempts[key] = attempts + 1
	delivered, _, readErr := chatReviewFeedbackState(ctx, chat, id, key, sig, attempts)
	if delivered && readErr == nil {
		m.react.seen[key] = sig
	}
	if err := errors.Join(sendErr, readErr, m.persistPRSignaturesLocked(ctx, prURL)); err != nil {
		return sendOnceAccounted, err
	}
	if delivered {
		return sendOnceAccounted, nil
	}
	return sendOnceSuppressed, nil
}

// A posted review can finish before its findings reach the local SCM snapshot.
// Use the persisted completion time so a restart cannot extend the grace period.
func (m *Manager) reviewDoneMessage(ctx context.Context, run domain.ReviewRun) (string, bool, error) {
	msg := formatReviewDoneNotice(run)
	if run.Status != domain.ReviewRunCompleted || run.GithubReviewID == "" {
		return msg, true, nil
	}
	anchor := "#pullrequestreview-" + run.GithubReviewID
	reviews, err := m.store.ListPRReviews(ctx, run.PRURL)
	if err != nil {
		return "", false, err
	}
	observed := false
	reviewURL := ""
	for _, r := range reviews {
		if r.ID == run.GithubReviewID || strings.HasSuffix(r.URL, anchor) {
			observed, reviewURL = true, r.URL
			break
		}
	}
	if !observed && run.EndedAt != nil && m.clock().Before(run.EndedAt.Add(reviewNoticeObserveGrace)) {
		return "", false, nil
	}
	if reviewURL == "" {
		webURL := run.PRURL
		if pr, found, err := m.store.GetPR(ctx, run.PRURL); err != nil {
			return "", false, err
		} else if found && pr.HTMLURL != "" {
			webURL = pr.HTMLURL
		}
		if ref, err := domain.ParseChangeRequestURL(webURL); err == nil && ref.Provider == "github" {
			reviewURL = ref.URL + anchor
		}
	}
	if reviewURL != "" {
		msg += "\nReview: " + domain.SanitizeControlChars(reviewURL)
	} else {
		msg += "\nReview ID: " + domain.SanitizeControlChars(run.GithubReviewID)
	}
	if !observed {
		return msg + "\nInline comments: unknown. Orchestrator has not observed the posted review yet. Check the review before acting on this verdict.", true, nil
	}
	comments, err := m.store.ListPRComments(ctx, run.PRURL)
	if err != nil {
		return "", false, err
	}
	count := 0
	for _, c := range comments {
		if c.ReviewID == run.GithubReviewID {
			count++
		}
	}
	msg += fmt.Sprintf("\nInline comments observed: %d", count)
	if run.Verdict == domain.VerdictChangesRequested {
		msg += "\nCheck the posted review for findings. Inline feedback is forwarded separately to the PR worker."
	}
	return msg, true, nil
}

func formatReviewDoneNotice(run domain.ReviewRun) string {
	var msg strings.Builder
	fmt.Fprintf(&msg, "AO review done: %s\nPR: %s\nReviewed commit: %s\nRun: %s", reviewDoneVerdict(run), domain.SanitizeControlChars(run.PRURL), domain.SanitizeControlChars(run.TargetSHA), run.ID)
	if run.Harness != "" {
		fmt.Fprintf(&msg, "\nReviewer: %s", run.Harness)
		if run.ReviewerModel != "" {
			fmt.Fprintf(&msg, " (model %s)", domain.SanitizeControlChars(run.ReviewerModel))
		}
	}
	if run.Status != domain.ReviewRunCompleted {
		reason := strings.TrimSpace(domain.SanitizeControlChars(run.Body))
		if reason == "" {
			switch run.Status {
			case domain.ReviewRunFailed:
				reason = strings.ReplaceAll(string(run.FailureReason), "_", " ")
			case domain.ReviewRunCancelled:
				reason = string(run.CancelledBy)
			case domain.ReviewRunSuperseded:
				reason = domain.ReviewRunSupersededReason
			}
		}
		if r := []rune(reason); len(r) > reviewNoticeBodyLimit {
			reason = string(r[:reviewNoticeBodyLimit]) + "…"
		}
		if reason != "" {
			fmt.Fprintf(&msg, "\nReason: %s", reason)
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
	case domain.ReviewRunSuperseded:
		return "superseded"
	}
	if run.Verdict == domain.VerdictChangesRequested {
		return "changes requested"
	}
	return "approved"
}
