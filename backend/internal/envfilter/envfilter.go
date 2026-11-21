// Package envfilter strips ambient Claude Code parent-session identity
// markers out of an environment before it reaches a worker/agent process.
//
// A daemon started from inside a Claude Code session — a developer running
// rebuild-ao.sh from an agent terminal, or the desktop app launched from
// one — inherits that session's own identity markers (CLAUDECODE,
// CLAUDE_CODE_SESSION_ID, CLAUDE_CODE_CHILD_SESSION, and friends). The
// daemon forwards its own os.Environ() to every worker it spawns (tmux/pty
// runtime, ACP chat drivers), so without filtering, a worker's own
// claude-code process misidentifies itself as a child of that unrelated
// parent session: "Transcript saving is off — inherited
// CLAUDE_CODE_CHILD_SESSION marker".
//
// This is a denylist of specific markers, not a blanket CLAUDE_ prefix
// block: auth credentials (ANTHROPIC_*, CLAUDE_CODE_OAUTH_TOKEN) and any
// user-set var that merely shares the prefix are deliberately left alone —
// claudecode.go reads the former for its own auth probe, and a project's own
// CLAUDE_-prefixed config var is legitimate.
package envfilter

import "strings"

var parentSessionMarkers = map[string]struct{}{
	"CLAUDECODE":                     {},
	"CLAUDE_CODE_SESSION_ID":         {},
	"CLAUDE_CODE_CHILD_SESSION":      {},
	"CLAUDE_CODE_ENTRYPOINT":         {},
	"CLAUDE_CODE_BRIDGE_SESSION_ID":  {},
	"CLAUDE_CODE_MESSAGING_TOKEN":    {},
	"CLAUDE_CODE_MESSAGING_SOCKET":   {},
	"CLAUDE_CODE_EXECPATH":           {},
	"CLAUDE_PID":                     {},
	"CLAUDE_PLUGIN_DATA":             {},
	"CLAUDE_EFFORT":                  {},
	"CLAUDE_CODE_MAX_CONTEXT_TOKENS": {},
	"CLAUDE_CODE_SUBAGENT_MODEL":     {},
}

// IsParentSessionMarker reports whether key is a known Claude Code
// parent-session identity marker that must not be forwarded to a worker.
func IsParentSessionMarker(key string) bool {
	_, blocked := parentSessionMarkers[key]
	return blocked
}

// DropParentSessionMarkers filters KEY=VALUE entries (the os/exec.Cmd.Env
// form), removing any whose key is a known parent-session marker. Entries
// without an "=" are malformed but preserved as-is — filtering them is not
// this function's concern.
func DropParentSessionMarkers(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && IsParentSessionMarker(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}
