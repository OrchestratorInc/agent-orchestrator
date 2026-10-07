// Package branchstate keeps each live session's persisted branch facts (commits
// on top of the base, whether they reached the remote) current. Workspace
// watches reconcile immediately while a client is subscribed; this tick covers
// changes no watch saw, such as a first push without -u.
package branchstate

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

// DefaultTickInterval is the branch-state reconciliation cadence.
const DefaultTickInterval = 30 * time.Second

// Config controls branch-state reconciliation.
type Config struct {
	Tick   time.Duration
	Logger *slog.Logger
}

type sessionSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type branchStateSink interface {
	ReconcileSessionBranchState(ctx context.Context, id domain.SessionID) error
}

// Observer periodically reconciles every live session's branch facts.
type Observer struct {
	sessions sessionSource
	sink     branchStateSink
	tick     time.Duration
	logger   *slog.Logger
}

// New builds a branch-state observer.
func New(sessions sessionSource, sink branchStateSink, cfg Config) *Observer {
	o := &Observer{sessions: sessions, sink: sink, tick: cfg.Tick, logger: cfg.Logger}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start runs reconciliation until the context is canceled.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "branch state observer")
}

// Poll reconciles every non-terminated session.
func (o *Observer) Poll(ctx context.Context) error {
	sessions, err := o.sessions.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.IsTerminated {
			continue
		}
		if err := o.sink.ReconcileSessionBranchState(ctx, sess.ID); err != nil {
			o.logger.Debug("branch state observer: reconcile failed", "session", sess.ID, "err", err)
		}
	}
	return nil
}
