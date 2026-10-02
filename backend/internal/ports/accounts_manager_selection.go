package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AccountsManagerSessionCreator commits creation or promotion with its initial binding.
type AccountsManagerSessionCreator interface {
	CreateSessionWithAccount(context.Context, domain.SessionRecord, domain.AccountsManagerAccountChoice) (domain.SessionRecord, bool, error)
	PromoteTaskPreparationWithAccount(context.Context, domain.SessionID, domain.SessionRecord, domain.AccountsManagerAccountChoice) (bool, error)
}

// AccountsManagerAccountValidator checks current eligibility without minting a route.
type AccountsManagerAccountValidator interface {
	ValidateAgentAccountTarget(context.Context, domain.AccountsManagerConnectionMode, domain.AccountsManagerProvider, string, string) error
}
