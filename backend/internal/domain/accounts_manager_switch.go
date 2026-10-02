package domain

import (
	"errors"
	"time"
)

// ErrAccountsManagerSwitchConflict rejects changed intent or concurrent switching.
var ErrAccountsManagerSwitchConflict = errors.New("accounts manager switch changed or is already in progress")

// AccountsManagerSwitchPhase tracks durable progress independently of displayed session state.
type AccountsManagerSwitchPhase string

// AccountsManagerSwitchRequested and subsequent phases preserve the stop and commit boundaries.
const (
	AccountsManagerSwitchRequested        AccountsManagerSwitchPhase = "requested"
	AccountsManagerSwitchWaiting          AccountsManagerSwitchPhase = "waiting"
	AccountsManagerSwitchStopping         AccountsManagerSwitchPhase = "stopping"
	AccountsManagerSwitchStopped          AccountsManagerSwitchPhase = "stopped"
	AccountsManagerSwitchCommitted        AccountsManagerSwitchPhase = "committed"
	AccountsManagerSwitchStarting         AccountsManagerSwitchPhase = "starting"
	AccountsManagerSwitchReady            AccountsManagerSwitchPhase = "ready"
	AccountsManagerSwitchCancelled        AccountsManagerSwitchPhase = "cancelled"
	AccountsManagerSwitchFailed           AccountsManagerSwitchPhase = "failed"
	AccountsManagerSwitchRecoveryRequired AccountsManagerSwitchPhase = "recovery_required"
)

// Terminal excludes operations that still owe recovery work.
func (p AccountsManagerSwitchPhase) Terminal() bool {
	return p == AccountsManagerSwitchReady || p == AccountsManagerSwitchCancelled || p == AccountsManagerSwitchFailed
}

// AccountsManagerSwitch journals intent and controller ownership, never credentials.
type AccountsManagerSwitch struct {
	ID                         string
	SessionID                  SessionID
	Provider                   AccountsManagerProvider
	SourceMode                 AccountsManagerConnectionMode
	SourceAccountID            string
	SourceRevision             int64
	SourceOwner                SessionControllerOwner
	SourceRuntimeHandleID      string
	TargetMode                 AccountsManagerConnectionMode
	TargetAccountID            string
	TargetRevision             int64
	TargetGeneration           string
	RetiredTargetGeneration    string
	RetiredTargetHandleID      string
	Policy                     SessionInterfaceTransitionPolicy
	NewConversation            bool
	EmptySource                bool
	SourceNativeConversationID string
	Phase                      AccountsManagerSwitchPhase
	ErrorCode                  string
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

// SameRequest compares client intent without trusting client-supplied controller identity.
func (s AccountsManagerSwitch) SameRequest(other AccountsManagerSwitch) bool {
	return s.ID == other.ID && s.SessionID == other.SessionID && s.Provider == other.Provider &&
		s.SourceRevision == other.SourceRevision && s.TargetMode == other.TargetMode && s.TargetAccountID == other.TargetAccountID &&
		s.Policy == other.Policy && s.NewConversation == other.NewConversation
}

// CanAdvance excludes commitment and readiness, which require transactional witnesses.
func (p AccountsManagerSwitchPhase) CanAdvance(next AccountsManagerSwitchPhase) bool {
	switch p {
	case AccountsManagerSwitchRequested:
		return next == AccountsManagerSwitchWaiting || next == AccountsManagerSwitchCancelled || next == AccountsManagerSwitchFailed || next == AccountsManagerSwitchRecoveryRequired
	case AccountsManagerSwitchWaiting:
		return next == AccountsManagerSwitchWaiting || next == AccountsManagerSwitchStopping || next == AccountsManagerSwitchCancelled || next == AccountsManagerSwitchFailed || next == AccountsManagerSwitchRecoveryRequired
	case AccountsManagerSwitchStopping:
		return next == AccountsManagerSwitchStopped || next == AccountsManagerSwitchRecoveryRequired
	case AccountsManagerSwitchStopped:
		return next == AccountsManagerSwitchRecoveryRequired
	case AccountsManagerSwitchCommitted:
		return next == AccountsManagerSwitchStarting || next == AccountsManagerSwitchRecoveryRequired
	case AccountsManagerSwitchStarting:
		return next == AccountsManagerSwitchFailed || next == AccountsManagerSwitchRecoveryRequired
	case AccountsManagerSwitchRecoveryRequired:
		return next == AccountsManagerSwitchStopping || next == AccountsManagerSwitchStarting || next == AccountsManagerSwitchFailed || next == AccountsManagerSwitchRecoveryRequired
	default:
		return false
	}
}
