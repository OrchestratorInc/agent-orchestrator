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

	autohandHookCommandPrefix = "ao hooks autohand "
	autohandHookTimeout       = 30
)

var autohandManagedHookKeys = []string{"event", "command", "description", "enabled", "timeout"}

type autohandHookEntry struct {
	Event       string `json:"event"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Timeout     int    `json:"timeout,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

func (e *autohandHookEntry) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

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

type autohandHookSpec struct {
	Event      string
	Subcommand string
}

var autohandManagedHooks = []autohandHookSpec{
	{Event: "session-start", Subcommand: "session-start"},
	{Event: "pre-prompt", Subcommand: "user-prompt-submit"},
	{Event: "permission-request", Subcommand: "permission-request"},
	{Event: "stop", Subcommand: "stop"},
}

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

	hooksSection["enabled"] = json.RawMessage(`true`)

	if err := writeAutohandHooks(configPath, topLevel, hooksSection, entries); err != nil {
		return fmt.Errorf("autohand.GetAgentHooks: %w", err)
	}
	return nil
}

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

func autohandConfigPath() string {
	if env := strings.TrimSpace(os.Getenv("AUTOHAND_CONFIG")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(autohandConfigDirName, autohandConfigFileName)
	}
	return filepath.Join(home, autohandConfigDirName, autohandConfigFileName)
}

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
