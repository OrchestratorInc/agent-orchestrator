package modelcatalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type configuredDefaultSource struct {
	paths       func(home string, env map[string]string) []string
	parse       func(raw []byte) string
	repoFiles   []string
	envOverride string
	resolve     func(home, workingDir string, env map[string]string) string
}

var configuredDefaultSources = map[string]configuredDefaultSource{
	"opencode": {resolve: func(home, workingDir string, env map[string]string) string {
		return resolveOpenCodeModel(1, home, workingDir, env)
	}},
	"opencode-v2": {resolve: func(home, workingDir string, env map[string]string) string {
		return resolveOpenCodeModel(2, home, workingDir, env)
	}},
	"aider": {
		paths: func(home string, _ map[string]string) []string {
			if home == "" {
				return nil
			}
			return []string{filepath.Join(home, ".aider.conf.yml"), filepath.Join(home, ".aider.conf.yaml")}
		},
		parse:       parseYAMLModelKey,
		repoFiles:   []string{".aider.conf.yml", ".aider.conf.yaml"},
		envOverride: "AIDER_MODEL",
	},
	"droid": {
		paths: func(home string, _ map[string]string) []string {
			if home == "" {
				return nil
			}
			return []string{filepath.Join(home, ".factory", "settings.json")}
		},
		parse: parseJSONCModelKey,
	},
	"copilot": {
		paths: func(home string, env map[string]string) []string {
			root := envValue(env, "COPILOT_HOME")
			if root == "" && home != "" {
				root = filepath.Join(home, ".copilot")
			}
			if root == "" {
				return nil
			}
			return []string{filepath.Join(root, "settings.json")}
		},
		parse:       parseJSONCModelKey,
		repoFiles:   []string{".github/copilot/settings.json", ".github/copilot/settings.local.json"},
		envOverride: "COPILOT_MODEL",
	},
	"pi": {
		paths: func(home string, _ map[string]string) []string {
			if home == "" {
				return nil
			}
			return []string{filepath.Join(home, ".pi", "agent", "settings.json")}
		},
		parse: parsePiDefaultModel,
	},
	"crush": {
		paths: func(home string, env map[string]string) []string {
			var paths []string
			if configHome := xdgDir(home, env, "XDG_CONFIG_HOME", ".config"); configHome != "" {
				paths = append(paths, filepath.Join(configHome, "crush", "crush.json"))
			}
			if dataHome := xdgDir(home, env, "XDG_DATA_HOME", filepath.Join(".local", "share")); dataHome != "" {
				paths = append(paths, filepath.Join(dataHome, "crush", "crush.json"))
			}
			return paths
		},
		parse:     parseCrushDefaultModel,
		repoFiles: []string{"crush.json", ".crush.json"},
	},
}

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
	if repoMayHoldConfig(workingDir, source.repoFiles...) {
		return ""
	}
	configured := ""
	for _, path := range source.paths(home, env) {
		configured = readConfiguredModel(path, source.parse, configured)
	}
	return configured
}

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

func envValue(env map[string]string, key string) string {
	if value, ok := env[key]; ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(os.Getenv(key))
}
