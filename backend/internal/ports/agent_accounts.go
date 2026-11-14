package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	ErrAgentAccountUnknown          = errors.New("agent account not found")
	ErrAgentAccountProviderMismatch = errors.New("the account belongs to another provider")
	ErrAgentAccountSignedOut        = errors.New("the account's config folder is not signed in")
	ErrAgentAccountDuplicate        = errors.New("that login is already an account")
	ErrAgentAccountLoginUnknown     = errors.New("agent account sign-in not found")
	ErrAgentAccountDeviceFixed      = errors.New("the computer's own login cannot be removed here")
	ErrAgentAccountUnchanged        = errors.New("the session already uses this account")
	ErrAgentAccountChanged          = errors.New("the session's account changed since this turn")
	ErrAgentAccountUnshared         = errors.New("the account's folder cannot link to this computer's history")
	ErrAgentAccountInUse            = errors.New("an agent that uses the account could not be stopped")
	ErrAgentAccountDefaultUnusable  = errors.New("the default account is signed out or now holds another login")
)

type AgentAccountProviderCLI interface {
	Provider() domain.AgentAccountProvider
	Identity(ctx context.Context, home string) (domain.AgentAccountIdentityCheck, error)
	Limits(ctx context.Context, home string) (domain.AgentAccountLimits, error)
	LoginArgv(ctx context.Context) ([]string, error)
	Logout(ctx context.Context, home string) error
	Prepare(home string) error
	LaunchEnv(home string) map[string]string
}

type AgentAccountStore interface {
	ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error)
	CreateAgentAccount(ctx context.Context, account domain.AgentAccount) error
	DeleteAgentAccount(ctx context.Context, id string) error
	GetAgentAccountDefault(ctx context.Context, provider domain.AgentAccountProvider) (string, bool, error)
	SetAgentAccountDefault(ctx context.Context, provider domain.AgentAccountProvider, accountID string) error
	BindSessionAgentAccount(ctx context.Context, sessionID domain.SessionID, provider domain.AgentAccountProvider, accountID string, at time.Time) error
	GetSessionAgentAccount(ctx context.Context, sessionID domain.SessionID, provider domain.AgentAccountProvider) (string, bool, error)
	AgentAccountUsageSince(ctx context.Context, since time.Time) ([]domain.AgentAccountUsage, error)
}

type AgentLimitReadingStore interface {
	RecordAgentLimitReadings(ctx context.Context, readings []domain.AgentLimitReading) error
	ListAgentLimitReadings(ctx context.Context, since time.Time) ([]domain.AgentLimitReading, error)
	LimitHistoryEnabled(ctx context.Context) bool
}

type AgentAccountLaunchEnv interface {
	SessionLaunchEnv(ctx context.Context, sessionID domain.SessionID) (map[string]string, error)
	SessionLaunchEnvWith(ctx context.Context, sessionID domain.SessionID, accountID string) (map[string]string, error)
	BindSession(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, accountID string) error
	CommitSession(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, accountID string) error
	SessionAccountID(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness) (string, error)
	ValidateAccount(ctx context.Context, harness domain.AgentHarness, accountID string) error
}
