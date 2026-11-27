package ports

import (
	"context"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// SessionVersionMoveStore journals a stop before it affects a process. Updating
// the stage never creates intent for unrelated manual resume operations.
type SessionVersionMoveStore interface {
	PutSessionVersionMove(context.Context, domain.SessionVersionMove) error
	ListSessionVersionMoves(context.Context) ([]domain.SessionVersionMove, error)
	SetSessionVersionMoveStage(context.Context, domain.SessionID, string) error
	AttemptSessionVersionMove(context.Context, domain.SessionID, int) (bool, error)
	DeleteSessionVersionMove(context.Context, domain.SessionID) error
}
