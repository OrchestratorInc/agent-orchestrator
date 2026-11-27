// Package sessionenv names the environment variables that belong to one
// agent session rather than to the machine: the ones Orchestrator exports into
// a session's shell, and the ones a coding agent sets in the shell it runs
// commands in. A daemon started from such a shell must not inherit them, or
// every session it spawns would claim to be that session or run nested in
// that agent.
package sessionenv

import "strings"

// SessionVars are exported by Orchestrator, or by Claude Code, into the shell
// of an agent session, a reviewer, a side chat, or a session terminal tab.
var SessionVars = []string{
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
	"AO_DESKTOP_APP",
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

// CallerMarkers are the tags coding agents set in the shells they run
// commands in, which tell a child process it runs inside that agent.
var CallerMarkers = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_PID",
	"CODEX_THREAD_ID",
	"CODEX_SANDBOX",
	"CODEX_CI",
	"GEMINI_CLI",
	"CURSOR_AGENT",
	"OPENCODE",
	"OPENCODE_CLIENT",
	"AI_AGENT",
}

// callerSessionPrefixes name the per-session state a calling agent exports:
// its session, host, messaging channel, child-session notes, and SDK. They are
// stripped by prefix. User settings such as CLAUDE_CODE_USE_BEDROCK are kept.
var callerSessionPrefixes = []string{
	"CLAUDE_CODE_SESSION_",
	"CLAUDE_CODE_HOST_",
	"CLAUDE_CODE_MESSAGING_",
	"CLAUDE_CODE_CHILD_",
	"CLAUDE_AGENT_SDK_",
}

// callerSessionVars are single per-session or per-account values a calling
// agent exports.
var callerSessionVars = []string{
	"CLAUDE_CODE_SSE_PORT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_ACCOUNT_UUID",
	"CLAUDE_CODE_ORGANIZATION_UUID",
	"CLAUDE_CODE_USER_EMAIL",
	"CLAUDE_CODE_DESKTOP_APP_VERSION",
	"CLAUDE_CODE_SDK_HAS_HOST_AUTH_REFRESH",
}

// claudeUserSettings are CLAUDE_* variables a user sets for every Claude Code
// run (model provider, credentials, network and output limits). When the
// caller is itself a Claude Code session, every other CLAUDE_* variable is that
// host session's own state or the settings its host app applied to it (tool
// lists, effort, feature switches), so ScrubForDaemon drops them.
var claudeUserSettings = map[string]bool{
	"CLAUDE_CODE_USE_BEDROCK":                  true,
	"CLAUDE_CODE_USE_VERTEX":                   true,
	"CLAUDE_CODE_USE_FOUNDRY":                  true,
	"CLAUDE_CODE_SKIP_BEDROCK_AUTH":            true,
	"CLAUDE_CODE_SKIP_VERTEX_AUTH":             true,
	"CLAUDE_CODE_SKIP_FOUNDRY_AUTH":            true,
	"CLAUDE_CODE_CLIENT_CERT":                  true,
	"CLAUDE_CODE_CLIENT_KEY":                   true,
	"CLAUDE_CODE_CLIENT_KEY_PASSPHRASE":        true,
	"CLAUDE_CODE_API_KEY_HELPER_TTL_MS":        true,
	"CLAUDE_CODE_MAX_OUTPUT_TOKENS":            true,
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": true,
	"CLAUDE_CONFIG_DIR":                        true,
}

// daemonConfigVars are session variables that are also the daemon's own
// configuration: they pick the data dir, run file and port the daemon serves,
// a self-hosted host's agent browser and wake helper, or the machine paths of
// the bundled tmux, ACP runtime and cloudflared. ScrubForDaemon keeps them, so
// `ao serve` still targets the daemon the caller configured and finds the same
// tools; none of them identifies a session.
var daemonConfigVars = map[string]bool{
	"AO_DATA_DIR":            true,
	"AO_RUN_FILE":            true,
	"AO_PORT":                true,
	"AO_AGENT_BROWSER_URL":   true,
	"AO_AGENT_BROWSER_TOKEN": true,
	"AO_WAKE_HELPER":         true,
	"AO_TMUX_BINARY":         true,
	"AO_ACP_RUNTIME_DIR":     true,
	"AO_CLOUDFLARED_PATH":    true,
}

// ScrubForDaemon returns env ("KEY=value" entries) without the variables of
// the session or coding agent it was taken from, keeping the daemon's own
// configuration. When the caller is a Claude Code session (CLAUDECODE set), it
// also drops that session's CLAUDE_* settings except claudeUserSettings; from
// a plain shell, CLAUDE_* user settings are kept. Use it for the environment of a daemon started from a shell
// that may be an agent's.
func ScrubForDaemon(env []string) []string {
	drop := map[string]bool{}
	for _, k := range SessionVars {
		if !daemonConfigVars[k] {
			drop[k] = true
		}
	}
	for _, k := range CallerMarkers {
		drop[k] = true
	}
	for _, k := range callerSessionVars {
		drop[k] = true
	}
	insideClaude := false
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == "CLAUDECODE" && v != "" {
			insideClaude = true
		}
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] || hasCallerSessionPrefix(k) {
			continue
		}
		if insideClaude && strings.HasPrefix(k, "CLAUDE") && !claudeUserSettings[k] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func hasCallerSessionPrefix(k string) bool {
	for _, p := range callerSessionPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}
