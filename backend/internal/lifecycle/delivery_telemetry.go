package lifecycle

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (m *Manager) DeliveryTelemetry() *deliverytelemetry.Recorder { return m.delivery }

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

func (m *Manager) reportEndIfTerminated(ctx context.Context, before domain.SessionRecord, live bool, endErr error, reason deliverytelemetry.EndReason) {
	if !live {
		return
	}
	if endErr != nil {
		after, ok, err := m.store.GetSession(context.WithoutCancel(ctx), before.ID)
		if err != nil || !ok || !after.IsTerminated {
			return
		}
	}
	m.delivery.SessionEnded(ctx, before, reason)
}
