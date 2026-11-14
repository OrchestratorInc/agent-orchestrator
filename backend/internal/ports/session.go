package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrSessionNotFound reports an observation for an unknown session id.
var ErrSessionNotFound = errors.New("session not found")

// ErrActivityProjectionContention means no signal projection committed after
// retrying concurrent session writes. The same hook payload may be retried.
var ErrActivityProjectionContention = errors.New("activity projection contention")

// SpawnConfig is the request to start a new session: which project/issue, which
// agent harness, and the branch/prompt the agent launches with.
type SpawnConfig struct {
	ProjectID domain.ProjectID
	IssueID   domain.IssueID
	// AutomationRunID makes one scheduled occurrence idempotent across daemon
	// restarts. Ordinary interactive spawns leave this unset.
	AutomationRunID *domain.AutomationRunID
	// ClientRequestID and hash identify one optional interactive create request.
	ClientRequestID   string
	ClientRequestHash string
	// ParentSessionID identifies the AO orchestrator that requested this worker
	// through `ao spawn`. The daemon validates this reference and derives any
	// inherited settings itself; callers never supply an inherited policy.
	ParentSessionID domain.SessionID
	// CreatedBy names an unattended source the daemon cannot infer from the
	// other fields (tracker intake sets automation). Empty derives it: an
	// AutomationRunID is scheduled, a parent orchestrator is orchestrator, any
	// other parent session is automation, and everything else is user.
	CreatedBy domain.SessionCreator
	// TrackerProvider is the issue-tracker provider hint from the CLI's
	// --tracker-provider flag (defaults to "github"). It is used as a fallback
	// when the project's SCM origin cannot be classified by the configured
	// SCM provider. When the SCM origin resolves successfully, the resolved
	// provider takes precedence over this hint.
	TrackerProvider domain.TrackerProvider
	// IssueContext is optional pre-fetched tracker context for the task prompt.
	// Standing rules stay in SystemPrompt; issue facts belong to the user task.
	IssueContext string
	Kind         domain.SessionKind
	Harness      domain.AgentHarness
	Branch       string
	// BaseBranch starts the session's branch from this branch instead of the
	// project's default, and its pull request is meant to target it: stacking
	// on another PR. Only ordinary repository projects can honor it.
	BaseBranch       string
	RequireNewBranch bool
	Prompt           string
	// AgentConfig overrides the resolved project/role agent config for this
	// single spawn. Empty fields keep the project defaults.
	AgentConfig AgentConfig
	// EffortOverride preserves the distinction between an omitted task override
	// and an explicit empty value meaning provider default.
	EffortOverride bool
	// AgentConfigResolved means AgentConfig already contains the fully merged
	// project/role/task settings and may intentionally clear inherited values.
	AgentConfigResolved bool
	// AgentAccountID is the provider account the agent runs under. Empty uses
	// the provider's default account.
	AgentAccountID string

	// RequestedMode is the caller's explicit session mode, or empty to let the
	// daemon resolve its default. It is validated and persisted before any
	// controller launches. A later explicit interface transition may replace that
	// controller while preserving the AO session. An unsupported explicit request
	// fails the spawn rather than falling back to the other mode.
	RequestedMode domain.SessionMode

	// RequestedOutput is the deliverable the caller asks for. Empty is the ordinary
	// task flow; artifact adds an artifact-delivery section to the worker prompt.
	RequestedOutput domain.RequestedOutput

	// DisplayName is the user-facing sidebar label. Empty falls back to the
	// session id in the read model (e.g. orchestrator sessions).
	DisplayName string
	// DisplayNameExplicit marks DisplayName as chosen rather than derived from
	// the prompt, so a terminal program's title cannot outrank it.
	DisplayNameExplicit bool
	// NameFromPrompt generates task and workspace names before launch.
	NameFromPrompt bool
	// KeepDisplayName preserves an explicit CLI/API title while naming its workspace.
	KeepDisplayName bool
	// Attachments are files pasted or dropped into the task brief. They are
	// written into the session worktree and referenced by path in the prompt so
	// the agent can read them (CLI agents receive the prompt as text and cannot
	// consume inline binary data). Any file type is accepted except for
	// explicitly blocked types (e.g., SVG for security reasons).
	Attachments []SpawnAttachment

	// Async asks for a Chat spawn that answers as soon as the session row and
	// its conversation exist, leaving the worktree and the agent controller to
	// finish in the background. Only a caller with a UI to return to should set
	// it: `ao spawn` and the tests rely on a returned session being fully live.
	// Ignored for TUI mode. Orchestrator callers serialize project-scoped
	// starts and reuse the published session while its controller is starting.
	Async bool
	// TaskPreparation is the opaque speculative-worktree token returned while
	// the desktop's New Task dialog is open. It is a hint: an absent or expired
	// token falls back to ordinary workspace creation.
	TaskPreparation domain.TaskPreparationToken
	// Fork makes this spawn a copy of another session. Nil for ordinary spawns.
	Fork *SpawnFork
}

// SpawnFork describes a fork of an existing session. The source's worktree and
// files are never modified.
type SpawnFork struct {
	SourceSessionID domain.SessionID
	// StartPoint is the source's commit at fork time. The fork's new branch
	// starts here.
	StartPoint string
	// SeedRef holds the source's uncommitted and untracked files, captured
	// before the spawn. It is applied to the new worktree, then deleted.
	SeedRef string
	// KeepSeedRef marks a SeedRef the fork borrowed, such as a turn's saved
	// rollback snapshot. It is applied but never deleted.
	KeepSeedRef bool
	// SourceTitle names the original in the fork's standing instructions.
	SourceTitle string
	// ArtifactsAsOf, when set, copies the original's artifacts as they were
	// at that time: the start of the turn after an older forked turn.
	ArtifactsAsOf time.Time
	// ArtifactsTurnID, when set, names the turn whose kept artifact list the
	// fork copies: the one after the forked turn, which started from it.
	ArtifactsTurnID string
	// LimitAttachments, for a fork of an older turn, copies only the
	// attachments in AttachmentNames: the ones the conversation had by then.
	LimitAttachments bool
	AttachmentNames  []string
	// SourceWorkspace is the original's worktree. Its dependency folders are
	// cloned into the fork so setup has less to install.
	SourceWorkspace string
	// PrepareChat runs the agent's own native fork into the new worktree and
	// returns the forked provider conversation, which the fork then resumes.
	PrepareChat func(ctx context.Context, target ForkTarget) (providerConversationID string, err error)
	// PrepareTerminal returns the command that starts a terminal agent's own
	// fork in the new worktree.
	PrepareTerminal func(ctx context.Context, target ForkTarget) (ForkTerminalLaunch, error)
	// Handoff, for an agent that can't copy its own conversation, builds the
	// text copy a fresh agent starts with instead.
	Handoff func(ctx context.Context, target ForkTarget) (string, error)
}

// ForkTarget is the new session a fork is being prepared into.
type ForkTarget struct {
	SessionID     domain.SessionID
	DataDir       string
	WorkspacePath string
	Env           map[string]string
	// SystemPrompt and SystemPromptFile are the fork's own standing
	// instructions, for terminal agents whose fork command re-applies them.
	SystemPrompt     string
	SystemPromptFile string
}

// ForkTerminalLaunch is how a terminal fork's agent starts.
type ForkTerminalLaunch struct {
	Argv []string
	// AgentSessionID is the fork's own native session, when known before launch.
	AgentSessionID string
}

// SpawnAttachment is a single file attached to a spawn request. Data holds the
// already-decoded bytes; the manager derives the on-disk filename from the
// MIME type.
type SpawnAttachment struct {
	// Ext is the file extension (including the leading dot, e.g. ".png")
	// inferred from the attachment's declared MIME type, or ".bin" for unknown types.
	Ext  string
	Data []byte
}
