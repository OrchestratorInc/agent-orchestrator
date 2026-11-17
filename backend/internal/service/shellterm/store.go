package shellterm

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ShellTerminalRecord is one persisted shell terminal row. It carries only what
// is needed to re-attach after daemon/desktop restarts. Transient trusted
// terminals expire when their originating app launch ends.
type ShellTerminalRecord struct {
	HandleID   string
	ProjectID  domain.ProjectID
	SessionID  domain.SessionID
	WorkingDir string
	Title      string
	// TitleExplicit marks Title as chosen, so a program's title cannot outrank it.
	TitleExplicit bool
	// AppTitle is the title the shell's program last set.
	AppTitle     string
	AppRunID     string
	CreatedAt    time.Time
	Transient    bool // Trusted auth terminal, owned by its app launch.
	CommandOwner string
	// PreviewCapabilityVerifier authorizes only managed preview lifecycle calls
	// from this session-scoped shell; the bearer stays in its process environment.
	PreviewCapabilityVerifier string
	// EndedAt is set once a session's user shell is known to have ended (its
	// process is gone). The row is kept so the tab stays visible and can be
	// reopened; nil means not known to have ended.
	EndedAt *time.Time
	// LastAgentHarness is the coding agent running in the shell when it was
	// last seen, or "" when the shell's own prompt was in front.
	LastAgentHarness domain.AgentHarness
	// AgentConversationID is the conversation LastAgentHarness was in when
	// the app quit, when the agent said; "" otherwise.
	AgentConversationID string
}

// ShellTerminalEnd is what a shell's row keeps when its process ends, so the
// tab can be restored where it was.
type ShellTerminalEnd struct {
	EndedAt             time.Time
	WorkingDir          string
	AgentHarness        domain.AgentHarness
	AgentConversationID string
}

// Store is the shell terminal service's persistence surface. The SQLite store
// satisfies it; the interface lives next to its only consumer so this service
// does not depend on storage internals.
type Store interface {
	SelectRestorableShellTerminals(ctx context.Context, appRunID string) ([]ShellTerminalRecord, error)
	InsertShellTerminal(ctx context.Context, rec ShellTerminalRecord) error
	UpdateShellTerminalTitle(ctx context.Context, handleID, title string) (ShellTerminalRecord, bool, error)
	SelectShellTerminalByHandleID(ctx context.Context, handleID string) (ShellTerminalRecord, bool, error)
	SelectShellTerminalsByAppRunID(ctx context.Context, appRunID string) ([]ShellTerminalRecord, error)
	SelectShellTerminalsBySessionID(ctx context.Context, sessionID domain.SessionID) ([]ShellTerminalRecord, error)
	SelectShellTerminalsFromPreviousAppRuns(ctx context.Context, appRunID string) ([]ShellTerminalRecord, error)
	DeleteShellTerminalByHandleID(ctx context.Context, handleID string) (bool, error)
	DeleteShellTerminalsFromPreviousAppRuns(ctx context.Context, appRunID string) (int64, error)
	MarkShellTerminalEnded(ctx context.Context, handleID string, end ShellTerminalEnd) (bool, error)
	UpdateShellTerminalAgentHarness(ctx context.Context, handleID string, harness domain.AgentHarness) error
	ReplaceShellTerminal(ctx context.Context, oldHandleID string, rec ShellTerminalRecord) (bool, error)
}
