// claudecode_profile.go holds the per-role env-profile surface of the Claude
// Code adapter: the MCP/plugin flags a project or role config contributes to
// the launch and restore argv, plus the transcript probe that decides whether
// a --resume target is real.
//
// It is deliberately a separate file from claudecode.go. Upstream rewrites the
// command-building functions regularly (most recently moving them wholesale
// into pkg/agentruntime), and every such rewrite used to collide with these
// additions line-by-line. Keeping them here leaves claudecode.go carrying only
// two ProviderArgs fields and one guard call.
//
// This shrinks textual conflicts, not the coupling: the file still depends on
// claudecode.go's Plugin and on agentruntime's ProviderArgs. If upstream moves
// or renames those, this file will not conflict — it will simply stop
// compiling after an otherwise clean rebase.
package claudecode

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeProfileArgs renders the MCP and plugin flags a role's config
// contributes. Placement is agentruntime's job — see ProviderArgs.
//
// Upstream documents that field as "trusted host-owned flags"; these values
// come from an AO project/role config instead, so we lean on the wording
// slightly. It holds: callers validate the config (ports.AgentConfig.Validate)
// before building a command, the flags land in an argv array rather than a
// shell string, and the sibling codex adapter fills the same field the same
// way.
func claudeProfileArgs(cfg ports.AgentConfig) []string {
	var args []string
	appendMCPFlags(&args, cfg.MCP)
	appendPluginFlags(&args, cfg.PluginDirs)
	return args
}

// appendMCPFlags emits claude-code's per-session MCP flags. Each MCPConfig
// entry is passed to the repeatable --mcp-config as-is (a JSON string or a path
// to a JSON file — both accepted by the CLI). Strict adds --strict-mcp-config
// so the session ignores every other MCP source, isolating the worker. A nil
// MCPConfig emits nothing, so an unset config inherits the global MCP set as
// before. Strict alone (empty Configs) is valid: it means "no MCP at all".
func appendMCPFlags(cmd *[]string, mcp *domain.MCPConfig) {
	if mcp == nil {
		return
	}
	for _, c := range mcp.Configs {
		if c = strings.TrimSpace(c); c != "" {
			*cmd = append(*cmd, "--mcp-config", c)
		}
	}
	if mcp.Strict {
		*cmd = append(*cmd, "--strict-mcp-config")
	}
}

// appendPluginFlags emits --plugin-dir / --plugin-url for each entry. An
// http(s):// entry maps to --plugin-url (a fetched zip); any other value is
// treated as a local path and mapped to --plugin-dir. Both flags are repeatable,
// so one is emitted per entry. Empty/whitespace entries are skipped.
func appendPluginFlags(cmd *[]string, dirs []string) {
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		if isPluginURL(d) {
			*cmd = append(*cmd, "--plugin-url", d)
		} else {
			*cmd = append(*cmd, "--plugin-dir", d)
		}
	}
}

// isPluginURL reports whether s is an http(s) plugin URL rather than a local
// path, deciding --plugin-url vs --plugin-dir.
func isPluginURL(s string) bool {
	// URL schemes are case-insensitive (RFC 3986): HTTPS://example.com/p.zip is
	// a valid plugin URL, so parse and compare the scheme rather than matching a
	// lowercase prefix (which would route it to --plugin-dir as a local path).
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

// claudeTranscriptExists reports whether a --resume target actually has a
// transcript on disk. Without this check, GetRestoreCommand would report
// ok=true for any non-blank session id — including one this process
// synthesized itself (see claudeSessionUUID) that may never correspond to a
// real Claude session, e.g. after a reboot that dropped the SessionStart hook
// call, or a pruned ~/.claude/projects entry. `claude --resume` then exits 1
// with "No conversation found with session ID", and because ok=true short-
// circuits restoreArgv's fresh-launch fallback (session_manager/manager.go),
// the session is stranded rather than relaunched.
//
// The transcript is located by scanning every project directory under the
// Claude config dir's projects/ for <sessionID>.jsonl instead of deriving the
// directory name from the workspace path. Claude Code encodes ALL
// non-alphanumeric characters of the path (underscores and dots included) into
// that name; a probe that mirrored only "/" and "." computed a different
// directory for underscore workspaces, misdiagnosed the conversation as
// missing, and routed restore into the fresh-launch fallback — whose
// deterministic --session-id is create-only and collides with the existing
// transcript, leaving claude dead with "Session ID ... is already in use".
// The scan matches claude's own --resume lookup (which searches beyond the
// cwd's project directory) and survives future encoding changes; it mirrors
// NativeConversationExists in claudecode.go. If we can't tell (empty workspace
// path, or a read error other than "not found"), we don't block — only a
// confirmed absence should force a fallback.
func claudeTranscriptExists(workspacePath, sessionID string) bool {
	if workspacePath == "" {
		return true
	}
	projectsDir, ok := claudeProjectsDir()
	if !ok {
		return true
	}
	projects, err := os.ReadDir(projectsDir)
	if os.IsNotExist(err) {
		// No projects directory at all is a confirmed absence, not an
		// unreadable one — same distinction NativeConversationExists makes.
		return false
	}
	if err != nil {
		return true
	}
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(projectsDir, project.Name(), sessionID+".jsonl"))
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

// claudeProjectsDir resolves the transcript root the same way
// NativeConversationExists does: CLAUDE_CONFIG_DIR first, then ~/.claude. The
// session's resolved env is not reachable here (ports.RestoreConfig carries no
// env), so a CLAUDE_CONFIG_DIR delivered only through per-project session env
// is invisible to this probe — the daemon-process variable and the default
// location are what it can see.
func claudeProjectsDir() (string, bool) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "projects"), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".claude", "projects"), true
}
