package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AccountsManagerRemovalStore preserves deletion fences and acknowledgement boundaries.
type AccountsManagerRemovalStore interface {
	AccountsManagerRemovalImpact(context.Context, string) (domain.AccountsManagerRemovalImpact, error)
	CreateAccountsManagerRemoval(context.Context, string, string, int64, bool) (domain.AccountsManagerRemoval, bool, error)
	GetAccountsManagerRemoval(context.Context, string) (domain.AccountsManagerRemoval, bool, error)
	GetAccountsManagerAccountRemoval(context.Context, string) (domain.AccountsManagerRemoval, bool, error)
	ListActiveAccountsManagerRemovals(context.Context) ([]domain.AccountsManagerRemoval, error)
	BeginAccountsManagerRemovalStop(context.Context, string) error
	CancelAccountsManagerRemoval(context.Context, string) error
	ValidateAccountsManagerRemoval(context.Context, string) error
	RecordAccountsManagerRemovalBindingsRevoked(context.Context, string) error
	RecordAccountsManagerRemovalStopped(context.Context, string, domain.SessionID) error
	RecordAccountsManagerRemovalFailure(context.Context, string, string) error
	RecordAccountsManagerRemovalRevoked(context.Context, string) error
	CompleteAccountsManagerRemoval(context.Context, string) error
	AccountsManagerAccountDeleting(context.Context, string) (bool, error)
}

// AccountsManagerRemovalRouter coordinates credential deletion with durable session obligations.
type AccountsManagerRemovalRouter interface {
	PrepareAccountRemoval(context.Context, string, string, int64, bool) (domain.AccountsManagerRemoval, bool, error)
	FinalizeAccountRemoval(context.Context, string) error
	SynchronizeAgentBindings(context.Context) error
}

// AccountsManagerChatHostStore binds non-secret ownership proof to immutable generations.
type AccountsManagerChatHostStore interface {
	RecordAccountsManagerChatHost(context.Context, domain.SessionID, domain.AccountsManagerProvider, string, string) error
	GetAccountsManagerChatHost(context.Context, domain.SessionID, string) (string, bool, error)
}
