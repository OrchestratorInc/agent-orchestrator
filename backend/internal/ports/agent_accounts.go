package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Agent account errors the API maps to client responses.
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

// AgentAccountProviderCLI reads and prepares one provider's CLI config
// folders. home is empty for the computer's own folder. Implementations ask the
// CLI itself; none of them reads a token.
type AgentAccountProviderCLI interface {
	Provider() domain.AgentAccountProvider
	// Identity reports who home is signed in to.
	Identity(ctx context.Context, home string) (domain.AgentAccountIdentityCheck, error)
	// Limits reports the subscription limits of the account signed in to home.
	Limits(ctx context.Context, home string) (domain.AgentAccountLimits, error)
	// LoginArgv is the command that signs home in, run in a terminal with LaunchEnv(home).
	LoginArgv(ctx context.Context) ([]string, error)
	// Logout signs home out through the CLI.
	Logout(ctx context.Context, home string) error
	// Prepare links home to the computer's own folder so a conversation started
	// under one account resumes under another, and the user's skills, MCP servers
	// and settings follow the account. It is safe to run before every launch.
	Prepare(home string) error
	// LaunchEnv is the environment that makes the CLI use home.
	LaunchEnv(home string) map[string]string
}

// AgentAccountStore persists accounts, the default per provider, and which
// account each session uses.
type AgentAccountStore interface {
	ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error)
	CreateAgentAccount(ctx context.Context, account domain.AgentAccount) error
	DeleteAgentAccount(ctx context.Context, id string) error
	GetAgentAccountDefault(ctx context.Context, provider domain.AgentAccountProvider) (string, bool, error)
	SetAgentAccountDefault(ctx context.Context, provider domain.AgentAccountProvider, accountID string) error
	// BindSessionAgentAccount records that the session uses accountID for the
	// provider from now on. Earlier bindings are kept for usage attribution.
	BindSessionAgentAccount(ctx context.Context, sessionID domain.SessionID, provider domain.AgentAccountProvider, accountID string, at time.Time) error
	GetSessionAgentAccount(ctx context.Context, sessionID domain.SessionID, provider domain.AgentAccountProvider) (string, bool, error)
	// AgentAccountUsageSince sums each account's model usage since the time,
	// attributing every usage event to the account its session used then.
	AgentAccountUsageSince(ctx context.Context, since time.Time) ([]domain.AgentAccountUsage, error)
}

// AgentAccountLaunchEnv gives Session Manager the account environment of a
// session's agent launch.
type AgentAccountLaunchEnv interface {
	// SessionLaunchEnv returns the variables that run each provider's CLI under
	// the session's account, binding the provider's default first when the
	// session has none yet.
	SessionLaunchEnv(ctx context.Context, sessionID domain.SessionID) (map[string]string, error)
	// SessionLaunchEnvWith is SessionLaunchEnv with accountID in place of the
	// session's account for its provider. It binds nothing.
	SessionLaunchEnvWith(ctx context.Context, sessionID domain.SessionID, accountID string) (map[string]string, error)
	// BindSession pins the session to accountID, or to the provider's default
	// when accountID is empty.
	BindSession(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, accountID string) error
	// CommitSession records that the session now uses accountID, with no
	// sign-in probe: a move calls it once its agent already runs there.
	CommitSession(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, accountID string) error
	// SessionAccountID is the account the session uses for harness's provider,
	// or "" when the harness has no accounts.
	SessionAccountID(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness) (string, error)
	// ValidateAccount checks that accountID can run harness now: it is of the
	// harness's provider and signed in as the login it was added with.
	ValidateAccount(ctx context.Context, harness domain.AgentHarness, accountID string) error
}
