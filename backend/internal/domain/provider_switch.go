package domain

import "errors"

const ProviderSwitchMaxBootstrapChars = 32_000

type ProviderSwitchRefusalKind string

const (
	ProviderSwitchRefusalBusy               ProviderSwitchRefusalKind = "busy"
	ProviderSwitchRefusalPendingInteraction ProviderSwitchRefusalKind = "pending_interaction"
	ProviderSwitchRefusalUnsupported        ProviderSwitchRefusalKind = "unsupported"
	ProviderSwitchRefusalUnavailable        ProviderSwitchRefusalKind = "unavailable"
	ProviderSwitchRefusalRecoveryRequired   ProviderSwitchRefusalKind = "recovery_required"
)

type ProviderSwitchRequest struct {
	TargetHarness     AgentHarness
	IdempotencyKey    string
	Model             string
	Effort            string
	FastMode          bool
	PermissionMode    PermissionMode
	Ultracode         bool
	AutoCompactWindow string
	HandoffNote       string
}

type ChatProviderSwitch struct {
	ConversationID    string
	SourceBranchID    string
	SourceSequence    int64
	Bootstrap         string
	AutoCompactWindow string
}

var ErrProviderAlreadyActive = errors.New("target provider is already active")

type ProviderSwitchError struct {
	Kind   ProviderSwitchRefusalKind
	Detail string
	Cause  error
}

func (e *ProviderSwitchError) Error() string { return e.Detail }
func (e *ProviderSwitchError) Unwrap() error { return e.Cause }
