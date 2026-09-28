package domain

import (
	"errors"
	"time"
)

// ErrAccountsManagerAccountInUse and related errors preserve removal admission failures.
var (
	ErrAccountsManagerAccountInUse    = errors.New("account is in use; confirm the affected sessions before removal")
	ErrAccountsManagerAccountDeleting = errors.New("account removal is pending or completed")
	ErrAccountsManagerRemovalConflict = errors.New("account removal or its affected sessions changed")
)

// AccountsManagerRemovalPhase separates cancellable intent from irreversible recovery.
type AccountsManagerRemovalPhase string

// Removal phases distinguish cancellable intent from irreversible recovery.
const (
	AccountsManagerRemovalRequested AccountsManagerRemovalPhase = "requested"
	AccountsManagerRemovalStopping  AccountsManagerRemovalPhase = "stopping"
	AccountsManagerRemovalRevoked   AccountsManagerRemovalPhase = "revoked"
	AccountsManagerRemovalComplete  AccountsManagerRemovalPhase = "complete"
	AccountsManagerRemovalRecovery  AccountsManagerRemovalPhase = "recovery_required"
	AccountsManagerRemovalCancelled AccountsManagerRemovalPhase = "cancelled"
)

// Terminal prevents completed or cancelled journals from admitting more work.
func (p AccountsManagerRemovalPhase) Terminal() bool {
	return p == AccountsManagerRemovalComplete || p == AccountsManagerRemovalCancelled
}

// AccountsManagerRemovalSession captures the exact binding and controller to retire.
type AccountsManagerRemovalSession struct {
	SessionID       SessionID
	Provider        AccountsManagerProvider
	BindingRevision int64
	Owner           SessionControllerOwner
	RuntimeHandleID string
	Stopped         bool
}

// OwnsController excludes dormant provider bindings from active controller teardown.
func (s AccountsManagerRemovalSession) OwnsController() bool {
	return (s.Provider == AccountsManagerProviderCodex && s.Owner.Harness == HarnessCodex) ||
		(s.Provider == AccountsManagerProviderClaude && s.Owner.Harness == HarnessClaudeCode)
}

// AccountsManagerRemovalImpact binds confirmation to a snapshot revision.
type AccountsManagerRemovalImpact struct {
	Revision int64
	Sessions []AccountsManagerRemovalSession
}

// AccountsManagerRemoval retains stop and revocation obligations across restart.
type AccountsManagerRemoval struct {
	ID              string
	AccountID       string
	Impact          AccountsManagerRemovalImpact
	Phase           AccountsManagerRemovalPhase
	ErrorCode       string
	StopStarted     bool
	BindingsRevoked bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
