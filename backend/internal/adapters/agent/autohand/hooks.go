package autohand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	autohandConfigDirName  = ".autohand"
	autohandConfigFileName = "config.json"

	// autohandHookCommandPrefix identifies the hook commands AO owns, so
	// install skips duplicates and uninstall recognizes AO entries by prefix
	// without an embedded template to diff against.
	autohandHookCommandPrefix = "ao hooks autohand "
	autohandHookTimeout       = 30
)

// autohandManagedHookKeys are the entry keys AO owns. On marshal they are
// written from the typed fields below; any other key the user set is preserved
// from Extra. Keep in sync with the json tags on autohandHookEntry.
var autohandManagedHookKeys = []string{"event", "command", "description", "enabled", "timeout"}

// autohandHookEntry is the on-disk shape of one entry in the config's
// hooks.hooks array. AO owns the five typed fields; any other key the user set
// on an entry (matcher, filter, async, ...) is captured in Extra so a rewrite
// preserves fields AO does not own instead of silently dropping them.
type autohandHookEntry struct {
	Event       string `json:"event"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Timeout     int    `json:"timeout,omitempty"`

	// Extra holds keys AO does not manage, captured on unmarshal and written
	// back on marshal so they round-trip. encoding/json does not support
	// `json:",inline"`, so the round-trip is implemented via the custom
	// UnmarshalJSON/MarshalJSON below.
	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the entry's typed fields and captures every key AO does
// not manage into Extra, so a later MarshalJSON can write them back verbatim.
func (e *autohandHookEntry) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Decode the managed fields via a type alias to avoid recursing into this
	// method, then drop the managed keys so Extra holds only unknown ones.
	type managedAlias autohandHookEntry
	var managed managedAlias
	if err := json.Unmarshal(data, &managed); err != nil {
		return err
	}
	*e = autohandHookEntry(managed)

	for _, key := range autohandManagedHookKeys {
		delete(raw, key)
	}
	if len(raw) > 0 {
		e.Extra = raw
	} else {
		e.Extra = nil
	}
	return nil
}

// MarshalJSON writes AO's managed fields merged with any preserved unknown keys
// from Extra. Managed fields win on key collision so AO's values stay
// authoritative.
func (e autohandHookEntry) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(e.Extra)+len(autohandManagedHookKeys))
	for key, val := range e.Extra {
		out[key] = val
	}

	type managedAlias autohandHookEntry
	managedJSON, err := json.Marshal(managedAlias(e))
	if err != nil {
		return nil, err
	}
	var managed map[string]json.RawMessage
	if err := json.Unmarshal(managedJSON, &managed); err != nil {
		return nil, err
	}
	for key, val := range managed {
		out[key] = val
	}
	return json.Marshal(out)
}

// autohandHookSpec describes one hook AO installs. Event is Autohand's native
// lifecycle event name; Subcommand is the AO hook sub-command appended after the
// command prefix (and the value DeriveActivityState switches on).
type autohandHookSpec struct {
	Event      string
	Subcommand string
}

// autohandManagedHooks is the source of truth for the hooks AO installs. Each
// native Autohand event is routed to the AO sub-command DeriveActivityState
// understands. Autohand's pre-prompt event is the user-prompt-submit signal.
var autohandManagedHooks = []autohandHookSpec{
	{Event: "session-start", Subcommand: "session-start"},
	{Event: "pre-prompt", Subcommand: "user-prompt-submit"},
	{Event: "permission-request", Subcommand: "permission-request"},
	{Event: "stop", Subcommand: "stop"},
}

// GetAgentHooks installs AO's Autohand hooks into the Autohand config's
// hooks.hooks array. Existing user hooks are preserved and duplicate AO commands
// are not appended. The rest of the config (auth, provider, ...) is preserved
// byte-for-byte because only the hooks section is decoded and rewritten.
//
// Autohand loads hooks from a single config file (default ~/.autohand/config.json,
// overridable via AUTOHAND_CONFIG); it does not merge a workspace-local file at
// runtime, so AO installs into that config rather than a per-workspace file. The
// AUTOHAND_CONFIG env var, when set, takes precedence so AO and the agent agree
// on the target.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	configPath := autohandConfigPath()
	topLevel, hooksSection, entries, err := readAutohandHooks(configPath)
	if err != nil {
		return fmt.Errorf("autohand.GetAgentHooks: %w", err)
	}

	for _, spec := range autohandManagedHooks {
		command := autohandHookCommandPrefix + spec.Subcommand
		if autohandHookCommandExists(entries, command) {
			continue
		}
		entries = append(entries, autohandHookEntry{
			Event:       spec.Event,
			Command:     command,
			Description: "Orchestrator activity hook",
			Enabled:     true,
			Timeout:     autohandHookTimeout,
		})
	}

	// Autohand only fires hooks when the hooks section is enabled.
	hooksSection["enabled"] = json.RawMessage(`true`)

	if err := writeAutohandHooks(configPath, topLevel, hooksSection, entries); err != nil {
		return fmt.Errorf("autohand.GetAgentHooks: %w", err)
	}
	return nil
}

// UninstallHooks removes AO's Autohand hooks from the config's hooks.hooks
// array, leaving user-defined hooks and the rest of the config untouched. A
// missing file is a no-op. The hooks.enabled flag is left in place because it
// enables every Autohand hook, not just AO's.
func (p *Plugin) UninstallHooks(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	configPath := autohandConfigPath()
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, hooksSection, entries, err := readAutohandHooks(configPath)
	if err != nil {
		return fmt.Errorf("autohand.UninstallHooks: %w", err)
	}

	kept := make([]autohandHookEntry, 0, len(entries))
	for _, entry := range entries {
		if !isAutohandManagedHook(entry.Command) {
			kept = append(kept, entry)
		}
	}

	if err := writeAutohandHooks(configPath, topLevel, hooksSection, kept); err != nil {
		return fmt.Errorf("autohand.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any AO Autohand hook is present in the
// config. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, _ string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	configPath := autohandConfigPath()
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, _, entries, err := readAutohandHooks(configPath)
	if err != nil {
		return false, fmt.Errorf("autohand.AreHooksInstalled: %w", err)
	}
	for _, entry := range entries {
		if isAutohandManagedHook(entry.Command) {
			return true, nil
		}
	}
	return false, nil
}

// autohandConfigPath returns the config file Autohand loads hooks from: the
// AUTOHAND_CONFIG override if set, else ~/.autohand/config.json.
func autohandConfigPath() string {
	if env := strings.TrimSpace(os.Getenv("AUTOHAND_CONFIG")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to a relative path; callers surface the resulting error.
		return filepath.Join(autohandConfigDirName, autohandConfigFileName)
	}
	return filepath.Join(home, autohandConfigDirName, autohandConfigFileName)
}

// readAutohandHooks loads the config into a top-level raw map, the decoded
// "hooks" section (preserving keys AO doesn't manage such as "enabled"), and the
// decoded hooks array. A missing or empty file yields empty maps and a nil
// slice.
func readAutohandHooks(configPath string) (topLevel, hooksSection map[string]json.RawMessage, entries []autohandHookEntry, err error) {
	topLevel = map[string]json.RawMessage{}
	hooksSection = map[string]json.RawMessage{}

	data, err := os.ReadFile(configPath) //nolint:gosec // path is the user's own Autohand config
	if errors.Is(err, os.ErrNotExist) {
		return topLevel, hooksSection, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return topLevel, hooksSection, nil, nil
	}
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return nil, nil, nil, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if hooksRaw, ok := topLevel["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &hooksSection); err != nil {
			return nil, nil, nil, fmt.Errorf("parse hooks in %s: %w", configPath, err)
		}
	}
	if arrRaw, ok := hooksSection["hooks"]; ok {
		if err := json.Unmarshal(arrRaw, &entries); err != nil {
			return nil, nil, nil, fmt.Errorf("parse hooks array in %s: %w", configPath, err)
		}
	}
	return topLevel, hooksSection, entries, nil
}

// writeAutohandHooks folds the entries back into the hooks section, the hooks
// section back into topLevel, and writes the file atomically. An empty entries
// slice drops the "hooks" array key.
func writeAutohandHooks(configPath string, topLevel, hooksSection map[string]json.RawMessage, entries []autohandHookEntry) error {
	if len(entries) == 0 {
		delete(hooksSection, "hooks")
	} else {
		arrJSON, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("encode hooks array: %w", err)
		}
		hooksSection["hooks"] = arrJSON
	}

	if len(hooksSection) == 0 {
		delete(topLevel, "hooks")
	} else {
		hooksJSON, err := json.Marshal(hooksSection)
		if err != nil {
			return fmt.Errorf("encode hooks section: %w", err)
		}
		topLevel["hooks"] = hooksJSON
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", configPath, err)
	}
	data = append(data, '\n')
	if err := hookutil.AtomicWriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

func isAutohandManagedHook(command string) bool {
	return strings.HasPrefix(command, autohandHookCommandPrefix)
}

func autohandHookCommandExists(entries []autohandHookEntry, command string) bool {
	for _, entry := range entries {
		if entry.Command == command {
			return true
		}
	}
	return false
}
