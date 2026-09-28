package domain

import (
	"errors"
	"strings"
)

// Selection errors distinguish invalid intent from unavailable enforcement.
var (
	ErrAccountsManagerSelectionInvalid     = errors.New("invalid initial account choice")
	ErrAccountsManagerSelectionUnavailable = errors.New("initial account selection is unavailable")
)

// AccountsManagerAccountChoice is user intent, not a credential or authorization lease.
type AccountsManagerAccountChoice struct {
	Mode      AccountsManagerConnectionMode
	AccountID string
}

// Valid rejects choices that could ambiguously select device credentials.
func (c AccountsManagerAccountChoice) Valid() bool {
	if c.Mode == AccountsManagerNative {
		return c.AccountID == ""
	}
	return c.Mode == AccountsManagerManaged && c.AccountID != "" &&
		len(c.AccountID) <= 256 && strings.TrimSpace(c.AccountID) == c.AccountID
}
