package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type SessionInteractionRecorder interface {
	RecordSessionInteraction(context.Context, domain.SessionID, string, time.Time) error
}
