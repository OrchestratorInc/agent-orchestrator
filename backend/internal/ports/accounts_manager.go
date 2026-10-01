package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AccountsManagerLaunchRoute is private launch material. It must only be
// carried to the child process being launched and must never enter public
// session DTOs, logs, or durable session metadata.
type AccountsManagerLaunchRoute struct {
	BaseURL         string
	Token           string
	BindingRevision int64
}

// AccountsManagerLaunchRouter supplies private child routes without exposing runner implementation details.
type AccountsManagerLaunchRouter interface {
	PrepareAgentLaunchRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider, string) (*AccountsManagerLaunchRoute, error)
	AgentRoutingEnabled(context.Context, domain.AccountsManagerProvider) (bool, error)
	HasAgentSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (bool, error)
}

// AccountsManagerModelCatalog scopes managed launch validation to the chosen credential.
type AccountsManagerModelCatalog interface {
	AgentAccountModels(context.Context, domain.AccountsManagerProvider, string) (AgentModelCatalog, error)
}

// AccountsManagerNativeRecorder freezes native intent for an unsupported managed mode.
type AccountsManagerNativeRecorder interface {
	RecordNativeAgentSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) error
}

// AccountsManagerSwitchStore makes binding changes and controller witnesses durable.
type AccountsManagerSwitchStore interface {
	GetAccountsManagerSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error)
	GetOrCreateAccountsManagerSessionRoute(context.Context, domain.AccountsManagerSessionRoute) (domain.AccountsManagerSessionRoute, bool, error)
	CreateAccountsManagerSwitch(context.Context, domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, bool, error)
	GetAccountsManagerSwitch(context.Context, string) (domain.AccountsManagerSwitch, bool, error)
	GetLatestAccountsManagerSwitch(context.Context, domain.SessionID) (domain.AccountsManagerSwitch, bool, error)
	ListActiveAccountsManagerSwitches(context.Context) ([]domain.AccountsManagerSwitch, error)
	AdvanceAccountsManagerSwitch(context.Context, string, domain.AccountsManagerSwitchPhase, domain.AccountsManagerSwitchPhase, string) (domain.AccountsManagerSwitch, error)
	CommitAccountsManagerSwitch(context.Context, string) (domain.AccountsManagerSwitch, error)
	AcknowledgeAccountsManagerSwitch(context.Context, string, string) (domain.AccountsManagerSwitch, error)
	RetryAccountsManagerSwitch(context.Context, string, string, domain.SessionControllerOwner) (domain.AccountsManagerSwitch, error)
	PrepareAccountsManagerSwitchStop(context.Context, string, bool, string, domain.SessionControllerOwner) (domain.AccountsManagerSwitch, error)
}

// AccountsManagerSwitchRouter serializes target admission and commitment with credential removal.
type AccountsManagerSwitchRouter interface {
	AdmitAgentAccountSwitch(context.Context, domain.AccountsManagerSwitch, string) (domain.AccountsManagerSwitch, bool, error)
	ValidateAgentAccountTarget(context.Context, domain.AccountsManagerConnectionMode, domain.AccountsManagerProvider, string, string) error
	CommitAgentAccountSwitch(context.Context, string, string) (domain.AccountsManagerSwitch, error)
	SynchronizeAgentBindings(context.Context) error
}

// AccountsManagerSwitchPendingReader prevents work admission while a switch is unresolved.
type AccountsManagerSwitchPendingReader interface {
	AgentAccountSwitchPending(context.Context, domain.SessionID) (bool, error)
}

// AccountsManagerSwitchRetryReader reports an observation, not mutation admission.
type AccountsManagerSwitchRetryReader interface {
	AccountsManagerSwitchCanRetry(domain.AccountsManagerSwitch) bool
}
