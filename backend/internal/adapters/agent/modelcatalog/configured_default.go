package modelcatalog

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// configuredDefaultSource describes where an agent CLI keeps the model it runs
// when no --model flag is passed. Paths are listed lowest precedence first: a
// later file that sets a model overrides an earlier one, matching how these
// CLIs layer global and project configuration.
type configuredDefaultSource struct {
	paths func(home, workingDir string, env map[string]string) []string
	parse func(raw []byte) string
	// envOverride names a variable that wins over every file when set.
	envOverride string
	// resolve replaces paths/parse for agents whose layering cannot be
	// expressed as a flat file list. It returns "" when the effective model
	// cannot be determined.
	resolve func(home, workingDir string, env map[string]string) string
}

// configuredDefaultSources covers agents whose model-list command reports
// models but never marks which one the CLI will run. Without this, the picker
// has no default to show and the task form reads "Model not reported" even
// though the user has already chosen a model in the agent's own settings.
var configuredDefaultSources = map[string]configuredDefaultSource{
	"opencode": {resolve: func(home, workingDir string, env map[string]string) string {
		return resolveOpenCodeModel(1, home, workingDir, env)
	}},
	"opencode-v2": {resolve: func(home, workingDir string, env map[string]string) string {
		return resolveOpenCodeModel(2, home, workingDir, env)
	}},
	"aider": {
		paths: func(home, workingDir string, _ map[string]string) []string {
			var paths []string
			for _, dir := range []string{home, workingDir} {
				if dir != "" {
					paths = append(paths, filepath.Join(dir, ".aider.conf.yml"), filepath.Join(dir, ".aider.conf.yaml"))
				}
			}
			return paths
		},
		parse:       parseYAMLModelKey,
		envOverride: "AIDER_MODEL",
	},
	"droid": {
		paths: func(home, _ string, _ map[string]string) []string {
			if home == "" {
				return nil
			}
			return []string{filepath.Join(home, ".factory", "settings.json")}
		},
		parse: parseJSONCModelKey,
	},
	"copilot": {
		resolve:     resolveCopilotModel,
		envOverride: "COPILOT_MODEL",
	},
	"pi": {
		paths: func(home, _ string, _ map[string]string) []string {
			if home == "" {
				return nil
			}
			return []string{filepath.Join(home, ".pi", "agent", "settings.json")}
		},
		parse: parsePiDefaultModel,
	},
	"crush": {
		paths: func(home, workingDir string, env map[string]string) []string {
			var paths []string
			if configHome := xdgDir(home, env, "XDG_CONFIG_HOME", ".config"); configHome != "" {
				paths = append(paths, filepath.Join(configHome, "crush", "crush.json"))
			}
			// Crush's own model picker persists the selection to its data dir.
			if dataHome := xdgDir(home, env, "XDG_DATA_HOME", filepath.Join(".local", "share")); dataHome != "" {
				paths = append(paths, filepath.Join(dataHome, "crush", "crush.json"))
			}
			if workingDir != "" {
				paths = append(paths, filepath.Join(workingDir, "crush.json"), filepath.Join(workingDir, ".crush.json"))
			}
			return paths
		},
		parse: parseCrushDefaultModel,
	},
}

// resolveCopilotModel returns the model Copilot CLI will run for a session
// launched from an AO worktree of workingDir's repository. Copilot keeps
// user-editable settings, including the model /model selects, in
// settings.json; config.json is legacy managed state and is not read, so a
// stale value there can never be marked as the default. Model precedence is
// user settings < repository settings < repository local settings <
// COPILOT_MODEL (checked by the caller).
//
// Discovery reads the project checkout, but sessions launch from worktrees
// that contain only tracked files. settings.local.json is usually gitignored:
// an untracked copy in the checkout is absent from the worktree and is skipped,
// while a tracked one is present there and overrides the repository model.
// When tracking cannot be determined, the default is left unresolved.
// https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference
func resolveCopilotModel(home, workingDir string, env map[string]string) string {
	root := envValue(env, "COPILOT_HOME")
	if root == "" && home != "" {
		root = filepath.Join(home, ".copilot")
	}
	model := ""
	if root != "" {
		model = readConfiguredModel(filepath.Join(root, "settings.json"), parseJSONCModelKey, model)
	}
	if workingDir == "" {
		return model
	}
	model = readConfiguredModel(filepath.Join(workingDir, ".github", "copilot", "settings.json"), parseJSONCModelKey, model)
	localRel := filepath.Join(".github", "copilot", "settings.local.json")
	raw, err := readModelConfig(filepath.Join(workingDir, localRel))
	if err != nil {
		return model
	}
	local := strings.TrimSpace(parseJSONCModelKey(raw))
	if local == "" {
		return model
	}
	tracked, ok := gitTracksFile(workingDir, localRel)
	if !ok {
		return ""
	}
	if tracked {
		return local
	}
	return model
}

// gitTracksFile reports whether rel (relative to dir) is tracked by git. ok is
// false when git cannot answer, for example outside a repository.
var gitTracksFile = func(dir, rel string) (tracked, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--", filepath.ToSlash(rel))
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(string(out)) != "", true
}

// readConfiguredModel returns the model set in path, or fallback when the file
// is missing or sets none.
func readConfiguredModel(path string, parse func([]byte) string, fallback string) string {
	raw, err := readModelConfig(path)
	if err != nil {
		return fallback
	}
	if value := strings.TrimSpace(parse(raw)); value != "" {
		return value
	}
	return fallback
}

// configuredDefaultModel returns the model the agent's local configuration
// selects, or "" when the agent has no such source or nothing is configured.
func configuredDefaultModel(agentID, workingDir string, env map[string]string) string {
	source, ok := configuredDefaultSources[agentID]
	if !ok {
		return ""
	}
	if source.envOverride != "" {
		if value := envValue(env, source.envOverride); value != "" {
			return value
		}
	}
	home, _ := os.UserHomeDir()
	if source.resolve != nil {
		return strings.TrimSpace(source.resolve(home, workingDir, env))
	}
	configured := ""
	for _, path := range source.paths(home, workingDir, env) {
		configured = readConfiguredModel(path, source.parse, configured)
	}
	return configured
}

// applyConfiguredDefault marks the configured model as the catalog default. A
// catalog that already reports a default is left alone: the CLI's own answer
// is more authoritative than AO's reading of its config files. A configured
// model missing from the list is appended, because it is what the CLI will
// actually run.
func applyConfiguredDefault(models []ports.AgentModelInfo, configured string) []ports.AgentModelInfo {
	configured = strings.TrimSpace(configured)
	if configured == "" || !isConcreteModel(configured) {
		return models
	}
	for _, item := range models {
		if item.IsDefault && isConcreteModel(item.ID) {
			return models
		}
	}
	for i := range models {
		if strings.EqualFold(models[i].ID, configured) {
			models[i].IsDefault = true
			return models
		}
	}
	return append(models, ports.AgentModelInfo{ID: configured, Label: configured, IsDefault: true})
}

// isConcreteModel mirrors the renderer's rule: "default" is a placeholder, not
// a model the picker can resolve to a name.
func isConcreteModel(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && !strings.EqualFold(id, "default")
}

func parseJSONCModelKey(raw []byte) string {
	var config struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(stripJSONC(raw), &config) != nil {
		return ""
	}
	return config.Model
}

func parseYAMLModelKey(raw []byte) string {
	var config struct {
		Model string `yaml:"model"`
	}
	if yaml.Unmarshal(raw, &config) != nil {
		return ""
	}
	return config.Model
}

func parsePiDefaultModel(raw []byte) string {
	var settings struct {
		DefaultProvider string `json:"defaultProvider"`
		DefaultModel    string `json:"defaultModel"`
	}
	if json.Unmarshal(stripJSONC(raw), &settings) != nil {
		return ""
	}
	return joinProviderModel(settings.DefaultProvider, settings.DefaultModel)
}

func parseCrushDefaultModel(raw []byte) string {
	var config struct {
		Models struct {
			Large struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"large"`
		} `json:"models"`
	}
	if json.Unmarshal(stripJSONC(raw), &config) != nil {
		return ""
	}
	return joinProviderModel(config.Models.Large.Provider, config.Models.Large.Model)
}

// joinProviderModel builds the provider/model id these CLIs list. A model with
// no provider is returned bare; a provider with no model selects nothing.
func joinProviderModel(provider, model string) string {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if provider == "" || strings.HasPrefix(model, provider+"/") {
		return model
	}
	return provider + "/" + model
}

// stripJSONC removes // and /* */ comments and trailing commas so JSONC config
// files (opencode.jsonc) decode with encoding/json. String contents are kept
// verbatim, including sequences that look like comments inside URLs.
func stripJSONC(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inString, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(raw) && raw[i+1] == '/':
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			if i < len(raw) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(raw) && raw[i+1] == '*':
			i += 2
			for i+1 < len(raw) && (raw[i] != '*' || raw[i+1] != '/') {
				i++
			}
			i++
		case c == ']' || c == '}':
			// Drop a trailing comma left before this closer.
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

func xdgDir(home string, env map[string]string, key, fallback string) string {
	if dir := envValue(env, key); dir != "" {
		return dir
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, fallback)
}

// envValue prefers the discovery request's environment and falls back to the
// daemon's own, the same precedence the spawned CLI would see.
func envValue(env map[string]string, key string) string {
	if value, ok := env[key]; ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(os.Getenv(key))
}
