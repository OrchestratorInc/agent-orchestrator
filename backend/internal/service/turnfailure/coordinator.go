// Package turnfailure delivers durable failed-worker-turn facts to an orchestrator.
package turnfailure

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const pollInterval = 2 * time.Second

// Store is the durable failed-turn and session lookup surface.
type Store interface {
	ListDueWorkerTurnFailures(context.Context, time.Time, int64) ([]domain.WorkerTurnFailure, error)
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
func (c *Coordinator) RunDue(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.delivery == nil {
		return fmt.Errorf("worker turn failure delivery is not configured")
	}
	now := c.now().UTC()
	rows, err := c.store.ListDueWorkerTurnFailures(ctx, now, 1)
	if err != nil || len(rows) == 0 {
		return err
	}
	failure := rows[0]
	target, err := c.orchestrator(ctx, failure.ProjectID)
	if err != nil {
		return c.deferFailure(ctx, failure, now, "orchestrator lookup failed", err)
	}
	if target == "" {
		return c.deferFailure(ctx, failure, now, "no active orchestrator", nil)
	}
	key := "worker-turn-failed:" + failure.TurnID
	if err := c.delivery.SendSemantic(ctx, target, failureMessage(failure), key); err != nil {
		return c.deferFailure(ctx, failure, now, "semantic delivery was not accepted", err)
	}
	return c.store.AcknowledgeWorkerTurnFailure(ctx, failure.TurnID, c.now().UTC())
}

func (c *Coordinator) orchestrator(ctx context.Context, project domain.ProjectID) (domain.SessionID, error) {
	sessions, err := c.store.ListSessions(ctx, project)
	if err != nil {
		return "", err
	}
	var selected domain.SessionRecord
	for _, session := range sessions {
		if session.Kind != domain.KindOrchestrator || session.IsTerminated || session.Activity.State == domain.ActivityExited {
			continue
		}
		if selected.ID == "" || session.CreatedAt.After(selected.CreatedAt) {
			selected = session
		}
	}
	return selected.ID, nil
}

func (c *Coordinator) deferFailure(ctx context.Context, failure domain.WorkerTurnFailure, now time.Time, reason string, cause error) error {
	if err := c.store.RetryWorkerTurnFailure(ctx, failure.TurnID, now.Add(retryDelay(failure.Attempts)), reason); err != nil {
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

func failureMessage(f domain.WorkerTurnFailure) string {
	name := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(f.DisplayName, "\n", " "), "\r", " "))
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		name = string(f.SessionID)
	}
	reason := "provider error"
	lower := strings.ToLower(f.ErrorMessage)
	switch {
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"):
		reason = "provider timeout"
	case strings.Contains(lower, "auth"):
		reason = "authentication error"
	}
	return fmt.Sprintf("Worker %s (ao://sessions/%s/%s) had a failed Chat turn %s (%s). The work may have partially landed. Inspect its conversation and worktree before retrying or re-delegating; queued work remains paused.",
		name, f.ProjectID, f.SessionID, f.TurnID, reason)
}
