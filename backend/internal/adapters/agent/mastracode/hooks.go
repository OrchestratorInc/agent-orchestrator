package mastracode

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

const managedID = "ao-runtime"
const sentinel = "agent-orchestrator: managed mastra-code"

type nativeState struct {
	Owner    string `json:"owner"`
	Profile  string `json:"profile"`
	Database string `json:"database"`
}

func configPath(workspace string, parts ...string) string {
	return filepath.Join(append([]string{workspace, ".mastracode"}, parts...)...)
}

func readObject(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("preserve invalid native config %s: %w", path, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("native config %s must be an object", path)
	}
	return fields, nil
}

func writeObject(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func writeManaged(path, content string) error {
	data, err := os.ReadFile(path)
	if err == nil && !strings.Contains(string(data), sentinel) {
		return fmt.Errorf("refusing to overwrite non-AO file %s", path)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return hookutil.AtomicWriteFile(path, []byte(content), 0o600)
}

// GetAgentHooks adds a native instruction plugin and flat command hooks. Native
// TUI hook additionalContext is deliberately not used for standing instructions.
// User/global hooks and plugins, disabledPlugins, unknown fields and AGENTS.md
// are preserved; conflicting or disabled AO plugin IDs fail closed.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return errors.New("mastra-code requires an absolute workspace")
	}
	profile := cfg.Env["MASTRA_APP_DATA_DIR"]
	if profile == "" {
		profile = filepath.Join(cfg.DataDir, adapterID, "profile")
	}
	if !filepath.IsAbs(profile) {
		return errors.New("mastra-code requires an absolute native profile")
	}
	for _, name := range []string{"MASTRA_STORAGE_BACKEND", "MASTRA_DB_URL"} {
		if cfg.Env[name] != "" || os.Getenv(name) != "" {
			return errors.New("mastra-code remote/custom storage is not qualified")
		}
	}
	for _, path := range []string{configPath(cfg.WorkspacePath, "database.json"), filepath.Join(userHome(), ".mastracode", "database.json")} {
		if _, err := os.Stat(path); err == nil {
			return errors.New("mastra-code legacy database overrides are not qualified; preserve and configure native local default storage")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := validateNativeSettings(profile, cfg.Config.Permissions); err != nil {
		return err
	}
	database := cfg.Env["MASTRA_DB_PATH"]
	if database == "" {
		database = os.Getenv("MASTRA_DB_PATH")
	}
	if database == "" {
		database = filepath.Join(profile, "mastra.db")
	}
	if !filepath.IsAbs(database) {
		return errors.New("mastra-code requires an absolute native database path")
	}
	root := configPath(cfg.WorkspacePath)
	pluginDir := configPath(cfg.WorkspacePath, "plugins", managedID)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		return err
	}
	registryPath := configPath(cfg.WorkspacePath, "plugins", "plugins.json")
	registry, err := readObject(registryPath)
	if err != nil {
		return err
	}
	global, err := readObject(filepath.Join(userHome(), ".mastracode", "plugins", "plugins.json"))
	if err != nil {
		return err
	}
	for _, fields := range []map[string]json.RawMessage{registry, global} {
		var disabled []string
		if raw, ok := fields["disabledPlugins"]; ok {
			if err := json.Unmarshal(raw, &disabled); err != nil {
				return err
			}
		}
		for _, id := range disabled {
			if id == managedID {
				return errors.New("native disabledPlugins blocks the AO Mastra instruction plugin")
			}
		}
	}
	plugins := map[string]json.RawMessage{}
	if raw, ok := registry["plugins"]; ok {
		if err := json.Unmarshal(raw, &plugins); err != nil {
			return err
		}
		if plugins == nil {
			return errors.New("native plugins must be an object")
		}
	}
	record := map[string]any{"enabled": true, "source": "local", "specifier": "agent-orchestrator", "path": managedID, "entry": "index.ts"}
	encoded, _ := json.Marshal(record)
	if raw, ok := plugins[managedID]; ok {
		var existing map[string]any
		if err := json.Unmarshal(raw, &existing); err != nil {
			return err
		}
		if existing["specifier"] != "agent-orchestrator" || existing["path"] != managedID || existing["entry"] != "index.ts" || existing["enabled"] != true || existing["source"] != "local" {
			return errors.New("native project plugin conflicts with AO's reserved plugin ID")
		}
	}
	var globalPlugins map[string]json.RawMessage
	if raw, ok := global["plugins"]; ok {
		if err := json.Unmarshal(raw, &globalPlugins); err != nil {
			return err
		}
		if _, ok := globalPlugins[managedID]; ok {
			return errors.New("native global plugin conflicts with AO's reserved plugin ID")
		}
	}
	prompt := cfg.SystemPrompt
	if prompt == "" && cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return fmt.Errorf("required standing instructions: %w", err)
		}
		prompt = string(data)
	}
	text, _ := json.Marshal(prompt + "\n\nThe actual AO workspace for this session is " + cfg.WorkspacePath + ".")
	if err := writeManaged(filepath.Join(pluginDir, "index.ts"), "// "+sentinel+"\nexport default {id: "+fmt.Sprintf("%q", managedID)+", instructions: "+string(text)+"};\n"); err != nil {
		return err
	}
	plugins[managedID] = encoded
	registry["plugins"], _ = json.Marshal(plugins)
	if err := writeObject(registryPath, registry); err != nil {
		return err
	}
	if err := writeManaged(configPath(cfg.WorkspacePath, "ao-hook.cjs"), hookSource); err != nil {
		return err
	}
	statePath := configPath(cfg.WorkspacePath, "ao-runtime.json")
	if _, err := os.Stat(statePath); err == nil {
		if _, err := readState(cfg.WorkspacePath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeObject(statePath, nativeState{Owner: sentinel, Profile: profile, Database: database}); err != nil {
		return err
	}
	hooksPath := configPath(cfg.WorkspacePath, "hooks.json")
	hooks, err := readObject(hooksPath)
	if err != nil {
		return err
	}
	for _, event := range []string{"SessionStart", "AgentStart", "UserPromptSubmit", "Stop", "PermissionRequest", "PermissionResult", "Interrupt"} {
		var entries []json.RawMessage
		if raw, ok := hooks[event]; ok {
			if err := json.Unmarshal(raw, &entries); err != nil {
				return err
			}
		}
		kept := make([]json.RawMessage, 0, len(entries)+1)
		for _, raw := range entries {
			var entry struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(raw, &entry); err != nil {
				return err
			}
			if !strings.HasPrefix(entry.Command, "node .mastracode/ao-hook.cjs ") {
				kept = append(kept, raw)
			}
		}
		entry, _ := json.Marshal(map[string]any{"type": "command", "command": "node .mastracode/ao-hook.cjs " + event, "timeout": 5000})
		hooks[event], _ = json.Marshal(append(kept, entry))
	}
	if err := writeObject(hooksPath, hooks); err != nil {
		return err
	}
	if err := hookutil.EnsureWorkspaceGitignore(root, "hooks.json", "ao-hook.cjs", "ao-runtime.json"); err != nil {
		return err
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(registryPath), "plugins.json", managedID+"/"); err != nil {
		return err
	}
	return hookutil.EnsureWorkspaceGitignore(pluginDir, "index.ts")
}

func userHome() string { home, _ := os.UserHomeDir(); return home }

func readState(workspace string) (nativeState, error) {
	data, err := os.ReadFile(configPath(workspace, "ao-runtime.json"))
	if err != nil {
		return nativeState{}, fmt.Errorf("mastra-code native state has not been prepared: %w", err)
	}
	var state nativeState
	err = json.Unmarshal(data, &state)
	if err == nil && (state.Owner != sentinel || !filepath.IsAbs(state.Profile) || !filepath.IsAbs(state.Database)) {
		err = errors.New("invalid or non-AO Mastra state descriptor; refusing overwrite")
	}
	return state, err
}

const hookSource = `// agent-orchestrator: managed mastra-code
const fs = require("node:fs");
const {spawnSync} = require("node:child_process");
const event = process.argv[2];
const payload = JSON.parse(fs.readFileSync(0, "utf8"));
const names = {SessionStart:"session-start", AgentStart:"agent-start", UserPromptSubmit:"user-prompt-submit", Stop:"stop", PermissionRequest:"permission-request", PermissionResult:"permission-result", Interrupt:"interrupt"};
if (event === "SessionStart" && process.env.AO_MASTRA_RECEIPT_PATH) {
  // Local evidence precedes daemon delivery: a callback can be parked while
  // the manager verifies this generation before publishing the launch.
  const receipt = {nativeId:payload.session_id, cwd:payload.cwd, generation:process.env.AO_RUNTIME_LAUNCH_ID};
  const path = process.env.AO_MASTRA_RECEIPT_PATH;
  fs.writeFileSync(path+".tmp", JSON.stringify(receipt), {mode:0o600});
  fs.renameSync(path+".tmp", path);
}
if (payload.session_id === "session-init") delete payload.session_id;
if (event === "UserPromptSubmit") payload.prompt = payload.user_message;
if (names[event]) spawnSync("ao", ["hooks", "mastra-code", names[event]], {input:JSON.stringify(payload), stdio:["pipe","ignore","ignore"], timeout:4000, windowsHide:true});
`
