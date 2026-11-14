package sentryobs

import (
	"context"
	"errors"
	"net/http"
)

var faultCodes = map[string]struct{}{
	"WORKSPACE_CREATE_FAILED":                     {},
	"SPAWN_DELIVER_PROMPT_FAILED":                 {},
	"CHAT_CONTROLLER_FAILED":                      {},
	"SPAWN_TIMEOUT":                               {},
	"SPAWN_ATTACHMENTS_FAILED":                    {},
	"CHAT_RESUME_FAILED":                          {},
	"CHAT_RECOVERY_INCONCLUSIVE":                  {},
	"CHAT_DRIVER_UNAVAILABLE":                     {},
	"CHAT_DRIVER_INCOMPATIBLE":                    {},
	"SESSION_DELIVERY_UNCONFIRMED":                {},
	"SESSION_INCOMPLETE_HANDLE":                   {},
	"WORKSPACE_PARENT_INIT_FAILED":                {},
	"WORKSPACE_PARENT_GITIGNORE_FAILED":           {},
	"WORKSPACE_PARENT_COMMIT_FAILED":              {},
	"WORKSPACE_PARENT_INDEX_FAILED":               {},
	"GIT_INIT_FAILED":                             {},
	"INITIAL_COMMIT_FAILED":                       {},
	"PROJECT_NESTED_REPO_SCAN_FAILED":             {},
	"CLONE_CLEANUP_FAILED":                        {},
	"CLONE_PREPARATION_FAILED":                    {},
	"CODEX_ACCOUNT_SWITCH_ACTIVATION_UNCONFIRMED": {},
	"COMPUTER_USE_DAEMON_START_FAILED":            {},
}

func init() {
	for code := range faultCodes {
		safeErrorCodes[code] = struct{}{}
	}
}

func IsFaultCode(code string) bool {
	_, ok := faultCodes[code]
	return ok
}

func IsServerTimeout(status int, err error) bool {
	return status == http.StatusServiceUnavailable && err != nil && errors.Is(err, context.DeadlineExceeded)
}

var spawnFailedSteps = map[string]struct{}{
	"model_check": {}, "branch_fetch": {}, "worktree": {}, "setup": {},
	"agent_start": {}, "prompt": {}, "other": {},
}

var spawnFailureCauses = map[string]struct{}{
	"branch_exists": {}, "path_exists": {}, "locked": {}, "permission": {},
	"path_too_long": {}, "not_a_repo": {}, "killed": {}, "exited": {},
	"no_response": {}, "not_logged_in": {}, "timeout": {}, "other": {},
}
