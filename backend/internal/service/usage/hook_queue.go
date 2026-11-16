package usage

import (
	"context"
	"errors"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// HookQueue records hook usage signals in arrival order without holding the
// hook's HTTP request. A usage scan can hold the collector for seconds, and the
// agent waits for each hook before its next prompt or tool call.
type HookQueue struct {
	next    hookRecorder
	signals chan queuedHookSignal
	log     *slog.Logger
}

type hookRecorder interface {
	RecordHook(ctx context.Context, id domain.SessionID, signal HookSignal) error
}

type queuedHookSignal struct {
	id     domain.SessionID
	signal HookSignal
}

const hookQueueSize = 1024

// NewHookQueue returns a queue in front of next. Run must be started for
// queued signals to be recorded.
func NewHookQueue(next hookRecorder, log *slog.Logger) *HookQueue {
	if log == nil {
		log = slog.Default()
	}
	return &HookQueue{next: next, signals: make(chan queuedHookSignal, hookQueueSize), log: log}
}

// RecordHook queues the signal. A full queue makes the caller wait for space
// rather than record out of order.
func (q *HookQueue) RecordHook(ctx context.Context, id domain.SessionID, signal HookSignal) error {
	select {
	case q.signals <- queuedHookSignal{id: id, signal: signal}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run records queued signals one at a time until ctx is done.
func (q *HookQueue) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-q.signals:
			err := q.next.RecordHook(ctx, item.id, item.signal)
			if err != nil && ctx.Err() == nil && !errors.Is(err, ErrUsageSessionNotFound) {
				q.log.Warn("usage hook processing failed", "session", item.id, "event", item.signal.Event, "err", err)
			}
		}
	}
}
