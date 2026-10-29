package lifecycle

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeliveryTelemetry returns the shared delivery recorder, so the services that
// end sessions or observe PRs report through the same one. Nil when unwired.
func (m *Manager) DeliveryTelemetry() *deliverytelemetry.Recorder { return m.delivery }

// waitingInputCounter persists how often a session stopped for the user, read
// back by ao.session.ended. Optional so focused store fakes need not have it.
type waitingInputCounter interface {
	IncrementSessionWaitingInputCount(ctx context.Context, id domain.SessionID) error
}

func (m *Manager) countWaitingInput(ctx context.Context, id domain.SessionID) {
	counter, ok := m.store.(waitingInputCounter)
	if !ok {
		return
	}
	if err := counter.IncrementSessionWaitingInputCount(context.WithoutCancel(ctx), id); err != nil {
		slog.Default().Warn("lifecycle: count waiting input", "session", id, "err", err)
	}
}

// liveBeforeEnd returns the record of a session about to be ended, or
// ok=false when no ended event should follow (unwired, unknown, or already
// terminated).
func (m *Manager) liveBeforeEnd(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool) {
	if m.delivery == nil {
		return domain.SessionRecord{}, false
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || rec.IsTerminated {
		return domain.SessionRecord{}, false
	}
	return rec, true
}
