// Package testenv keeps the environment of an enclosing AO session out of tests.
package testenv

import "os"

// sessionVars are exported by AO, or by Claude Code, into the shell of an agent
// session, a reviewer, a side chat, or a session terminal tab. Product code reads
// them, so tests run from inside one would otherwise use its tmux socket,
// session id, and daemon.
var sessionVars = []string{
	"AO_SESSION_ID",
	"AO_PROJECT_ID",
	"AO_ISSUE_ID",
	"AO_RUNTIME_LAUNCH_ID",
	"AO_SUPERVISED_PROCESS",
	"AO_DATA_DIR",
	"AO_PERMISSION_MODE",
	"AO_RUN_FILE",
	"AO_PORT",
	"AO_BROWSER_CAPABILITY",
	"AO_AGENT_BROWSER_URL",
	"AO_AGENT_BROWSER_TOKEN",
	"AO_BROWSER_RUNTIME_TOKEN",
	"AO_BROWSER_RUNTIME_TOKEN_STDIN",
	"AO_BROWSER_RUNTIME_ADDRESS",
	"AO_BROWSER_STREAM_ADDRESS",
	"AO_TMUX_BINARY",
	"AO_TMUX_SOCKET_NAME",
	"AO_PATH_PIN",
	"AO_OWNER",
	"AO_APP_RUN_ID",
	"AO_ACP_RUNTIME_DIR",
	"AO_CLOUDFLARED_PATH",
	"AO_WAKE_HELPER",
	"AO_USER_ZDOTDIR",
	"AO_USER_BASH_ENV",
	"AO_REVIEW_SESSION_ID",
	"AO_REVIEW_WORKER_SESSION_ID",
	"AO_SIDE_CONVERSATION_ID",
	"AO_SESSION",
	"AO_PREVIEW_CAPABILITY",
	"AO_SHELL_TERMINAL",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_PID",
}

// ClearSession unsets the session variables for the test process. Call it from
// TestMain before m.Run. It names each variable instead of clearing every AO_
// name because some tests re-run the test binary with an AO_ marker set.
func ClearSession() {
	for _, name := range sessionVars {
		_ = os.Unsetenv(name)
	}
}
