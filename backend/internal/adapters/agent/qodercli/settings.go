package qodercli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// hookCommandPrefix identifies the hook commands AO owns.
	hookCommandPrefix = "ao hooks qodercli "

	// hookTimeoutSeconds is in seconds: Qoder CLI measures hook timeouts in
	// seconds like Claude Code, unlike its gemini-cli ancestor (milliseconds).
	hookTimeoutSeconds = 30
)

// sessionStartMatcher is referenced by pointer so SessionStart serializes with
// Qoder CLI's documented source matcher. "startup" alone would miss --resume
// relaunches, /clear, /compact, and the "new" source.
var sessionStartMatcher = "startup|resume|clear|new|compact"

// managedHooks is the source of truth for the hooks AO installs. The event
// names, their payload fields, and their notification and session-end
// vocabularies are Claude Code's, which is why activity derivation reuses
// claudecode.DeriveActivityState.
//
// SubagentStop is deliberately absent: it carries no activity signal, and its
// payload includes last_assistant_message, which the hook CLI would latch as the
// session summary — a subagent's last line overwriting the agent's.
//
// The hooks carry no env block. Qoder CLI hook processes inherit the pane
// environment, which is where AO_SESSION_ID and AO_RUNTIME_LAUNCH_ID come from.
var managedHooks = []hookSpec{
	{Event: "SessionStart", Matcher: &sessionStartMatcher, Command: hookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: hookCommandPrefix + "user-prompt-submit"},
	{Event: "PreToolUse", Command: hookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: hookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFailure", Command: hookCommandPrefix + "post-tool-use-failure"},
	{Event: "PermissionRequest", Command: hookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: hookCommandPrefix + "stop"},
	{Event: "Notification", Command: hookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: hookCommandPrefix + "session-end"},
}

type hookSpec struct {
	Event   string
	Matcher *string
	Command string
}

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type matcherGroup struct {
	Matcher *string     `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

type settingsPermissions struct {
	TrustDirectories []string `json:"trustDirectories,omitempty"`
}

type settingsGeneral struct {
	// EnableAutoUpdate is false so Qoder CLI cannot replace its own binary
	// underneath a running AO session.
	EnableAutoUpdate bool `json:"enableAutoUpdate"`
}

type settingsPayload struct {
	Permissions *settingsPermissions      `json:"permissions,omitempty"`
	General     settingsGeneral           `json:"general"`
	Hooks       map[string][]matcherGroup `json:"hooks,omitempty"`
}

// SettingsJSON builds the --settings payload for one launch: trust for the
// session's workspace, AO's activity hooks, and auto-update off.
//
// AO passes this through an AO-owned file rather than writing
// <workspace>/.qoder/settings.json. Flag-scoped settings apply whatever the
// folder's trust state and need no trust of their own, and they leave nothing
// behind in the checkout: Qoder CLI does not gitignore a local settings file, so
// an AO-written one would dirty every session's worktree.
//
// Trust is the load-bearing part: in an untrusted directory Qoder CLI drops
// project hooks, forces --permission-mode back to default, and opens a blocking
// trust dialog that no one is there to answer.
func SettingsJSON(workspacePath string) (string, error) {
	hooks := make(map[string][]matcherGroup, len(managedHooks))
	for _, spec := range managedHooks {
		hooks[spec.Event] = []matcherGroup{{
			Matcher: spec.Matcher,
			Hooks: []hookEntry{{
				Type:    "command",
				Command: spec.Command,
				Timeout: hookTimeoutSeconds,
			}},
		}}
	}

	payload := settingsPayload{General: settingsGeneral{EnableAutoUpdate: false}, Hooks: hooks}
	if trusted := trustDirectories(workspacePath); len(trusted) > 0 {
		payload.Permissions = &settingsPermissions{TrustDirectories: trusted}
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("qodercli: encode settings: %w", err)
	}
	return string(encoded), nil
}

// SettingsArg returns the value AO passes to --settings: the path of an
// AO-owned settings file when a data dir is available, otherwise the inline
// JSON.
//
// Qoder CLI accepts either form, but it materializes an inline payload into a
// content-addressed file in the shared system temp dir — one file per distinct
// payload, never cleaned up. Writing the file ourselves under AO's data dir
// keeps AO-owned state under AO's own root. A data dir that cannot be written
// falls back to the inline form, which still launches.
func SettingsArg(dataDir, sessionKey, workspacePath string) (string, error) {
	payload, err := SettingsJSON(workspacePath)
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(dataDir)
	key := safeSessionKey(sessionKey)
	if dir == "" || key == "" {
		return payload, nil
	}
	settingsDir := filepath.Join(dir, "agent-runtime", "qodercli", key)
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		return payload, nil
	}
	path := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		return payload, nil
	}
	return path, nil
}

// safeSessionKey keeps an id usable as a single path segment.
func safeSessionKey(sessionID string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(sessionID) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "._")
}

// trustDirectories lists the workspace under both the path AO knows and its
// symlink-resolved form. Qoder CLI decides trust by realpath containment, and
// AO's checkout path can differ from its resolved path, so listing both keeps an
// unresolved path from silently costing the session its trust — and with it its
// permission mode.
func trustDirectories(workspacePath string) []string {
	workspace := strings.TrimSpace(workspacePath)
	if workspace == "" {
		return nil
	}
	dirs := []string{workspace}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil && resolved != workspace {
		dirs = append(dirs, resolved)
	}
	return dirs
}
