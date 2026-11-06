package sentryobs

import (
	"context"
	"errors"
	"net/http"
)

// faultCodes are daemon faults that answer with a 4xx or 503 so clients can
// branch on them, but that are not bad input or expected user state. The
// request logger captures them whatever the status. Status codes are left
// unchanged on purpose: clients already depend on them.
//
// Deliberately left out: WORKSPACE_PROVISION_FAILED and
// WORKSPACE_CLEANUP_FAILED (the user's own setup or cleanup script),
// GIT_CLONE_FAILED and network codes (remote or connectivity), and anything
// auth related (the user is signed out).
var faultCodes = map[string]struct{}{
	// Session spawn (409 / 400).
	"WORKSPACE_CREATE_FAILED":     {},
	"SPAWN_DELIVER_PROMPT_FAILED": {},
	"CHAT_CONTROLLER_FAILED":      {},
	"SPAWN_TIMEOUT":               {},
	"SPAWN_ATTACHMENTS_FAILED":    {},
	// Session resume, recovery and delivery (409).
	"CHAT_RESUME_FAILED":           {},
	"CHAT_RECOVERY_INCONCLUSIVE":   {},
	"CHAT_DRIVER_UNAVAILABLE":      {},
	"CHAT_DRIVER_INCOMPATIBLE":     {},
	"SESSION_DELIVERY_UNCONFIRMED": {},
	"SESSION_INCOMPLETE_HANDLE":    {},
	// Project create and clone: local git writes failing (400).
	"WORKSPACE_PARENT_INIT_FAILED":      {},
	"WORKSPACE_PARENT_GITIGNORE_FAILED": {},
	"WORKSPACE_PARENT_COMMIT_FAILED":    {},
	"WORKSPACE_PARENT_INDEX_FAILED":     {},
	"GIT_INIT_FAILED":                   {},
	"INITIAL_COMMIT_FAILED":             {},
	"PROJECT_NESTED_REPO_SCAN_FAILED":   {},
	"CLONE_CLEANUP_FAILED":              {},
	"CLONE_PREPARATION_FAILED":          {},
	// Codex account staging and Computer Use (503).
	"CODEX_ACCOUNT_SETUP_INCOMPLETE":              {},
	"CODEX_ACCOUNT_SWITCH_ACTIVATION_UNCONFIRMED": {},
	"COMPUTER_USE_DAEMON_START_FAILED":            {},
}

func init() {
	for code := range faultCodes {
		safeErrorCodes[code] = struct{}{}
	}
}

// IsFaultCode reports whether a 4xx or 503 error code is a daemon fault worth
// a Sentry issue.
func IsFaultCode(code string) bool {
	_, ok := faultCodes[code]
	return ok
}

// IsServerTimeout reports whether a 503 was caused by a server-side deadline
// (a hung git, provider or exec call) rather than lock contention.
func IsServerTimeout(status int, err error) bool {
	return status == http.StatusServiceUnavailable && err != nil && errors.Is(err, context.DeadlineExceeded)
}
