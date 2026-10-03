package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// The statusline is the only documented place Claude Code states how full its
// context window is: hook payloads carry no token or context figures. So AO
// installs a statusline command to read that figure, and the command prints a
// status line of its own (see cli.runStatuslineHook) because whatever it writes
// to stdout becomes what the user sees.
//
// That makes this different from installing a hook. A hook is additive and
// invisible; settings.local.json takes precedence over a user's project and
// global settings, so writing statusLine here REPLACES whatever status line the
// user had configured, inside AO worktrees only. AO therefore installs it only
// when the user has configured none, and a user who has their own keeps it and
// simply reports no context reading. Absent is already how the read model says
// "unknown", so that degrades honestly rather than silently overriding them.
const statusLineCommand = claudeHookCommandPrefix + "statusline"

// statusLineSetting is Claude Code's documented shape for the setting.
type statusLineSetting struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Padding int    `json:"padding,omitempty"`
}

// InstallStatusLine adds AO's statusline to the workspace settings when no
// status line is already configured, here or in the user's global settings. It
// returns whether the statusline is AO's once it is done.
func InstallStatusLine(workspacePath string) (bool, error) {
	path := claudeSettingsPath(workspacePath)
	settings, err := readSettings(path)
	if err != nil {
		return false, err
	}

	if raw, ok := settings["statusLine"]; ok {
		return isAOStatusLine(raw), nil
	}
	if userHasGlobalStatusLine() {
		return false, nil
	}

	encoded, err := json.Marshal(statusLineSetting{Type: "command", Command: statusLineCommand})
	if err != nil {
		return false, fmt.Errorf("encode statusLine: %w", err)
	}
	settings["statusLine"] = encoded
	if err := writeSettings(path, settings); err != nil {
		return false, err
	}
	return true, nil
}

// UninstallStatusLine removes AO's statusline, leaving a user's own untouched.
func UninstallStatusLine(workspacePath string) error {
	path := claudeSettingsPath(workspacePath)
	settings, err := readSettings(path)
	if err != nil {
		return err
	}
	raw, ok := settings["statusLine"]
	if !ok || !isAOStatusLine(raw) {
		return nil
	}
	delete(settings, "statusLine")
	return writeSettings(path, settings)
}

// isAOStatusLine reports whether a configured statusLine is the one AO wrote.
// Anything unparsable is treated as the user's, so AO never deletes a setting
// it does not recognize.
func isAOStatusLine(raw json.RawMessage) bool {
	var setting statusLineSetting
	if err := json.Unmarshal(raw, &setting); err != nil {
		return false
	}
	return strings.TrimSpace(setting.Command) == statusLineCommand
}

// userHasGlobalStatusLine reports whether ~/.claude/settings.json configures a
// status line. Read-only, and a home directory AO cannot read is treated as
// "has one": the safe assumption is the one that leaves the user's setup alone.
func userHasGlobalStatusLine() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return true
	}
	data, err := os.ReadFile(filepath.Join(home, claudeSettingsDirName, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return true
	}
	_, ok := settings["statusLine"]
	return ok
}

func readSettings(path string) (map[string]json.RawMessage, error) {
	settings := map[string]json.RawMessage{}
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return settings, nil
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return settings, nil
}

func writeSettings(path string, settings map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := hookutil.AtomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
