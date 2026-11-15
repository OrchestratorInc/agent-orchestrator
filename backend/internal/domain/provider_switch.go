package domain

import "errors"

// ProviderSwitchMaxBootstrapChars is the hard upper bound for the text sent to
// a target provider during a handoff.
const ProviderSwitchMaxBootstrapChars = 32_000

// ProviderSwitchRefusalKind is a stable reason a provider switch was not
// admitted. The HTTP layer maps it to a status and an error code.
type ProviderSwitchRefusalKind string

// The reasons a switch is refused.
const (
	ProviderSwitchRefusalBusy               ProviderSwitchRefusalKind = "busy"
	ProviderSwitchRefusalPendingInteraction ProviderSwitchRefusalKind = "pending_interaction"
	ProviderSwitchRefusalUnsupported        ProviderSwitchRefusalKind = "unsupported"
	ProviderSwitchRefusalUnavailable        ProviderSwitchRefusalKind = "unavailable"
	ProviderSwitchRefusalRecoveryRequired   ProviderSwitchRefusalKind = "recovery_required"
)

// ProviderSwitchRequest is what a switch to another provider asks for.
type ProviderSwitchRequest struct {
	TargetHarness  AgentHarness
	IdempotencyKey string
	// Model and Effort are what the target starts on. Empty keeps its default.
	Model  string
	Effort string
	// FastMode starts the target on its faster tier, where its model has one.
	FastMode bool
	// PermissionMode is the mode the user picked for the target. Empty keeps
	// the chat's. A mode the target cannot run in gives way to its strictest.
	PermissionMode PermissionMode
	// Ultracode and AutoCompactWindow are Claude Code launch choices for a
	// terminal target. See AgentLaunchOptions.
	Ultracode         bool
	AutoCompactWindow string
	// HandoffNote is the note the target provider starts with. The switch
	// front door builds it; it is never accepted from a client.
	HandoffNote string
}

// ChatProviderSwitch is an internal launch reservation. The source controller
// is fenced before the target starts; Start uses this proof to reuse the same
// AO conversation while creating a fresh provider branch.
type ChatProviderSwitch struct {
	ConversationID string
	SourceBranchID string
	SourceSequence int64
	Bootstrap      string
	// AutoCompactWindow is the Claude Code compaction window the target starts with.
	AutoCompactWindow string
}

// ErrProviderAlreadyActive is the cause of a switch to the agent already running.
var ErrProviderAlreadyActive = errors.New("target provider is already active")

// ProviderSwitchError crosses the Chat/TUI dispatch boundary without coupling
// the session manager to either transport controller.
type ProviderSwitchError struct {
	Kind   ProviderSwitchRefusalKind
	Detail string
	Cause  error
}

func (e *ProviderSwitchError) Error() string { return e.Detail }
func (e *ProviderSwitchError) Unwrap() error { return e.Cause }
