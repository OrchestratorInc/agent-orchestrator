package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SessionInteractionRecorder stores deliberate direction without changing human
// authorship. Nonempty sender identity is validated by the persistence boundary.
type SessionInteractionRecorder interface {
	RecordSessionInteraction(context.Context, domain.SessionID, string, time.Time) error
}
