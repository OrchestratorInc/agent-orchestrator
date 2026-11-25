// Package turnfailure delivers durable failed-worker-turn facts to an orchestrator.
package turnfailure

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	pollInterval = 2 * time.Second
	maxNameBytes = 80
)

// ErrUnacknowledgedDelivery marks a send that may have reached the orchestrator
// even though AO did not see it accepted. Its recipient stays pinned.
var ErrUnacknowledgedDelivery = errors.New("prompt submission was not observed")

// Store is the durable failed-turn and session lookup surface.
type Store interface {
	ListDueWorkerTurnFailures(context.Context, time.Time, int64) ([]domain.WorkerTurnFailure, error)
	BindWorkerTurnFailureTarget(context.Context, string, domain.SessionID) (bool, error)
	ReleaseWorkerTurnFailureTarget(context.Context, string, domain.SessionID) (bool, error)
	AcknowledgeWorkerTurnFailure(context.Context, string, time.Time) error
	RetryWorkerTurnFailure(context.Context, string, time.Time, string) error
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
}

// Delivery sends a message through the target session's semantic acceptance path.
type Delivery interface {
	SendSemantic(context.Context, domain.SessionID, string, string) error
}

// Coordinator retries failed worker turns until an orchestrator accepts them.
type Coordinator struct {
	store    Store
	delivery Delivery
	now      func() time.Time
	log      *slog.Logger
	wake     chan struct{}
	mu       sync.Mutex
}

// New constructs a worker turn failure delivery coordinator.
func New(store Store, delivery Delivery, log *slog.Logger) *Coordinator {
	if log == nil {
		log = slog.Default()
	}
	return &Coordinator{store: store, delivery: delivery, now: time.Now, log: log, wake: make(chan struct{}, 1)}
}

// Wake schedules an immediate check after a new failure is committed.
func (c *Coordinator) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Start also inspects existing rows immediately, so a daemon restart cannot
// strand a failure that was committed before its in-memory wake signal.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			if err := c.RunDue(ctx); err != nil && ctx.Err() == nil {
				c.log.Warn("worker turn failure delivery", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-c.wake:
			}
		}
	}()
	return done
}

// RunDue processes one durable event. The stable delivery key makes a retry
// safe when AO loses the response after the orchestrator accepted the message.
//
// Semantic deduplication is per session, so every send that might be accepted
// is pinned to its recipient first: a retry after a lost reply or a daemon
// restart then goes to the same orchestrator and cannot be accepted twice. A
// pin this attempt made is dropped again when the send is definitively
// refused, so the next attempt can pick whichever orchestrator is newest.
//
// A pin is also released once its orchestrator is terminated or replaced. That
// session may already hold the notice, but it can no longer act on it, and its
// replacement must not wait forever behind it.
func (c *Coordinator) RunDue(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.delivery == nil {
		return fmt.Errorf("worker turn failure delivery is not configured")
	}
	rows, err := c.store.ListDueWorkerTurnFailures(ctx, c.now().UTC(), 1)
	if err != nil || len(rows) == 0 {
		return err
	}
	failure := rows[0]
	live, newest, err := c.orchestrators(ctx, failure.ProjectID)
	if err != nil {
		return c.deferFailure(ctx, failure, "orchestrator lookup failed", err)
	}
	target := failure.TargetSessionID
	if target != "" && !live[target] {
		released, err := c.store.ReleaseWorkerTurnFailureTarget(ctx, failure.TurnID, target)
		if err != nil {
			return err
		}
		if !released {
			// Accepted or re-pinned meanwhile; re-read the row on the next pass.
			return nil
		}
		target = ""
	}
	pinnedHere := false
	if target == "" {
		if newest == "" {
			return c.deferFailure(ctx, failure, "no active orchestrator", nil)
		}
		bound, err := c.store.BindWorkerTurnFailureTarget(ctx, failure.TurnID, newest)
		if err != nil {
			return err
		}
		if !bound {
			// Accepted or pinned meanwhile; re-read the row on the next pass.
			return nil
		}
		target, pinnedHere = newest, true
	}
	key := "worker-turn-failed:" + failure.TurnID
	sendErr := c.delivery.SendSemantic(ctx, target, failureMessage(failure), key)
	if sendErr == nil {
		return c.store.AcknowledgeWorkerTurnFailure(ctx, failure.TurnID, c.now().UTC())
	}
	// Only a pin this attempt made is known never to have carried an accepted
	// send. An older pin may follow an uncertain send, so it stays.
	if pinnedHere && !errors.Is(sendErr, ErrUnacknowledgedDelivery) {
		if _, err := c.store.ReleaseWorkerTurnFailureTarget(ctx, failure.TurnID, target); err != nil {
			return err
		}
	}
	return c.deferFailure(ctx, failure, "semantic delivery was not accepted", sendErr)
}

// orchestrators returns the project's live orchestrators and the newest of them.
func (c *Coordinator) orchestrators(ctx context.Context, project domain.ProjectID) (map[domain.SessionID]bool, domain.SessionID, error) {
	sessions, err := c.store.ListSessions(ctx, project)
	if err != nil {
		return nil, "", err
	}
	live := map[domain.SessionID]bool{}
	var newest domain.SessionRecord
	for _, session := range sessions {
		// A session whose start failed refuses every message, so it can never
		// be the recipient even when it is the newest.
		if session.Kind != domain.KindOrchestrator || session.IsTerminated || session.Activity.State == domain.ActivityExited ||
			session.ProvisionState == domain.SessionProvisionFailed {
			continue
		}
		live[session.ID] = true
		if newest.ID == "" || session.CreatedAt.After(newest.CreatedAt) {
			newest = session
		}
	}
	return live, newest.ID, nil
}

// deferFailure counts the backoff from when the attempt ended, so a slow send
// does not use up its own retry delay.
func (c *Coordinator) deferFailure(ctx context.Context, failure domain.WorkerTurnFailure, reason string, cause error) error {
	next := c.now().UTC().Add(retryDelay(failure.Attempts))
	if err := c.store.RetryWorkerTurnFailure(ctx, failure.TurnID, next, reason); err != nil {
		return err
	}
	return cause
}

func retryDelay(attempts int64) time.Duration {
	delay := 5 * time.Second
	for i := int64(0); i < attempts && delay < 5*time.Minute; i++ {
		delay *= 2
	}
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

// Whole words only: a substring match would read "oauth" or "author" as an
// authentication failure.
var (
	timeoutError = regexp.MustCompile(`(?i)\btime[ -]?outs?\b|\btimed out\b|\bdeadline exceeded\b`)
	authError    = regexp.MustCompile(`(?i)\b(?:authentication|unauthori[sz]ed|unauthenticated|not authenticated)\b`)
)

func failureReason(f domain.WorkerTurnFailure) string {
	switch f.FailureCode {
	case domain.ConversationFailureAuthRequired:
		return "authentication error"
	case domain.ConversationFailureProviderKeyMissing:
		return "missing provider API key"
	}
	switch {
	case timeoutError.MatchString(f.ErrorMessage):
		return "provider timeout"
	case authError.MatchString(f.ErrorMessage):
		return "authentication error"
	default:
		return "provider error"
	}
}

func failureMessage(f domain.WorkerTurnFailure) string {
	name := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(f.DisplayName, "\n", " "), "\r", " "))
	if len(name) > maxNameBytes {
		// Cut at a character boundary so the notice stays valid UTF-8.
		cut := maxNameBytes
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	if name == "" {
		name = string(f.SessionID)
	}
	return fmt.Sprintf("Worker %s (ao://sessions/%s/%s) had a failed Chat turn %s (%s). The work may have partially landed. Inspect its conversation and worktree before retrying or re-delegating; queued work remains paused.",
		name, f.ProjectID, f.SessionID, f.TurnID, failureReason(f))
}
