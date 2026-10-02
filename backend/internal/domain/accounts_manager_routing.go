package domain

import (
	"errors"
	"time"
)

// ErrAccountsManagerBindingConflict rejects mutation against a stale session choice.
var ErrAccountsManagerBindingConflict = errors.New("accounts manager session binding changed")

// AccountsManagerConnectionMode distinguishes native credentials from an explicit managed route.
type AccountsManagerConnectionMode string

// AccountsManagerNative and AccountsManagerManaged never imply automatic fallback.
const (
	AccountsManagerNative  AccountsManagerConnectionMode = "native"
	AccountsManagerManaged AccountsManagerConnectionMode = "managed"
)

// AccountsManagerProvider identifies a provider supported by AO's embedded
// Accounts Manager. It is intentionally narrower than AgentHarness.
type AccountsManagerProvider string

// AccountsManagerProviderCodex and the other provider constants restrict managed routing support.
const (
	AccountsManagerProviderCodex  AccountsManagerProvider = "codex"
	AccountsManagerProviderClaude AccountsManagerProvider = "claude"
)

// Valid rejects providers outside the managed-routing contract.
func (p AccountsManagerProvider) Valid() bool {
	return p == AccountsManagerProviderCodex || p == AccountsManagerProviderClaude
}

// AccountsManagerRoutingPolicy holds the explicit default for new sessions.
type AccountsManagerRoutingPolicy struct {
	Provider   AccountsManagerProvider
	Enabled    bool
	AccountIDs []string
}

// AccountsManagerSessionRoute pins one provider in one AO session to a single
// public-safe account identifier.
type AccountsManagerSessionRoute struct {
	Blocked   bool
	Mode      AccountsManagerConnectionMode
	Revision  int64
	SessionID SessionID
	Provider  AccountsManagerProvider
	AccountID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AccountsManagerBindingSnapshot fences runner admission at a durable revision.
type AccountsManagerBindingSnapshot struct {
	Revision int64
	Bindings []AccountsManagerSessionRoute
}
