package daemon

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type ownAppViaDesktop struct{ broker *browserruntime.Broker }

func (d ownAppViaDesktop) PID() int {
	status := d.broker.Status()
	if !status.Connected {
		return 0
	}
	return status.ClientPID
}

func (d ownAppViaDesktop) Drag(ctx context.Context, sessionID string, drag map[string]any) (any, error) {
	if sessionID == "" {
		sessionID = "computer-use"
	}
	result, err := d.broker.Execute(ctx, domain.SessionID(sessionID), "__app-drag", drag)
	return result.Value, err
}
