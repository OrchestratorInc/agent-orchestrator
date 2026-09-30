package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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
}

// configuredDefaultSources covers agents whose model-list command reports
// models but never marks which one the CLI will run. Without this, the picker
// has no default to show and the task form reads "Model not reported" even
// though the user has already chosen a model in the agent's own settings.
var configuredDefaultSources = map[string]configuredDefaultSource{
	"opencode":    {paths: opencodeConfigPaths, parse: parseJSONCModelKey},
	"opencode-v2": {paths: opencodeConfigPaths, parse: parseJSONCModelKey},
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
		paths: func(home, _ string, env map[string]string) []string {
			root := envValue(env, "COPILOT_HOME")
			if root == "" {
				if home == "" {
					return nil
				}
				root = filepath.Join(home, ".copilot")
			}
			return []string{filepath.Join(root, "config.json")}
		},
		parse: parseJSONCModelKey,
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

func opencodeConfigPaths(home, workingDir string, env map[string]string) []string {
	var paths []string
	if configHome := xdgDir(home, env, "XDG_CONFIG_HOME", ".config"); configHome != "" {
		for _, name := range []string{"config.json", "opencode.json", "opencode.jsonc"} {
			paths = append(paths, filepath.Join(configHome, "opencode", name))
		}
	}
	if custom := envValue(env, "OPENCODE_CONFIG"); custom != "" {
		paths = append(paths, custom)
	}
	if workingDir != "" {
		paths = append(paths, filepath.Join(workingDir, "opencode.json"), filepath.Join(workingDir, "opencode.jsonc"))
	}
	return paths
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
	configured := ""
	for _, path := range source.paths(home, workingDir, env) {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		if value := strings.TrimSpace(source.parse(raw)); value != "" {
			configured = value
		}
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
