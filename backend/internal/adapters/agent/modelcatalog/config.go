package modelcatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const modelConfigReadLimit = 2 << 20

type configParser func([]byte) ([]ports.AgentModelInfo, error)

func hasConfigDiscoverySource(agentID string) bool {
	switch agentID {
	case "qwen", "continue", "goose", "vibe", "cline", "autohand", "hermes":
		return true
	default:
		return false
	}
}

func discoverConfigCatalog(agentID, workingDir string, env map[string]string) (ports.AgentModelCatalog, error) {
	base := Base(agentID)
	if agentID == "hermes" {
		return discoverHermesCatalog(base, modelConfigPaths(agentID, workingDir, env))
	}
	parser := configModelParser(agentID)
	if parser == nil {
		return base, fmt.Errorf("%s has no configuration model parser", agentID)
	}
	var models []ports.AgentModelInfo
	var found bool
	for _, path := range modelConfigPaths(agentID, workingDir, env) {
		raw, err := readModelConfig(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return base, fmt.Errorf("%s model discovery read %s: %w", agentID, path, err)
		}
		found = true
		parsed, err := parser(raw)
		if err != nil {
			return base, fmt.Errorf("%s model discovery parse %s: %w", agentID, path, err)
		}
		models = append(models, parsed...)
	}
	if !found {
		return base, fmt.Errorf("%s model configuration was not found", agentID)
	}
	models = normalize(models)
	if len(models) == 0 {
		return base, fmt.Errorf("%s model configuration returned no models", agentID)
	}
	base.Models = models
	base.SelectionMode = ports.ModelSelectionCatalog
	base.Source = "config"
	return base, nil
}

func configModelParser(agentID string) configParser {
	switch agentID {
	case "qwen":
		return parseQwenModels
	case "continue":
		return parseContinueModels
	case "goose":
		return parseGooseModels
	case "vibe":
		return parseVibeModels
	case "cline":
		return parseClineModels
	case "autohand":
		return parseAutoHandModels
	case "hermes":
		return parseHermesModels
	default:
		return nil
	}
}

func modelConfigPaths(agentID, workingDir string, env map[string]string) []string {
	home, _ := os.UserHomeDir()
	var paths []string
	switch agentID {
	case "qwen":
		if root := qwenConfigHome(home, env); root != "" {
			paths = append(paths, filepath.Join(root, "settings.json"))
		}
		if workingDir != "" {
			paths = append(paths, filepath.Join(workingDir, ".qwen", "settings.json"))
		}
	case "continue":
		if root := continueHomeDir(func(name string) string { return envValue(env, name) }); root != "" {
			paths = append(paths, filepath.Join(root, "config.yaml"))
		}
	case "hermes":
		if root := hermesConfigHome(home, env); root != "" {
			paths = append(paths, filepath.Join(root, "config.yaml"), filepath.Join(root, "provider_models_cache.json"))
		}
	case "goose":
		if root := strings.TrimSpace(env["GOOSE_PATH_ROOT"]); root != "" {
			paths = append(paths, filepath.Join(root, "config", "config.yaml"))
		} else if home != "" {
			paths = append(paths, filepath.Join(home, ".config", "goose", "config.yaml"))
		}
	case "deepseek-harness":
		// DeepSeek Harness's catalog comes from a live `dsh --profile acp`
		// session, and the profile decides the model route, so the profile's own
		// files are the configuration discovery consults. Without them the
		// fingerprint would be the dsh binary alone, and a route changed in the
		// web setup flow would leave the day's cached choices in place —
		// including choices the new session rejects.
		if root := deepseekProfileDir(home, env); root != "" {
			for _, name := range [...]string{"cordis.yml", "cordis.patch.yml", "package.json"} {
				paths = append(paths, filepath.Join(root, name))
			}
		}
	case "vibe":
		if root := strings.TrimSpace(env["VIBE_HOME"]); root != "" {
			paths = append(paths, filepath.Join(root, "config.toml"))
		} else if home != "" {
			paths = append(paths, filepath.Join(home, ".vibe", "config.toml"))
		}
		if workingDir != "" {
			paths = append(paths, filepath.Join(workingDir, ".vibe", "config.toml"))
		}
	case "cline":
		if home != "" {
			paths = append(paths, filepath.Join(home, ".cline", "data", "settings", "providers.json"))
		}
	case "autohand":
		if home != "" {
			paths = append(paths, filepath.Join(home, ".autohand", "config.json"))
		}
	}
	return paths
}

// hermesConfigHome resolves HERMES_HOME exactly as the adapter and Hermes do.
func hermesConfigHome(_ string, env map[string]string) string {
	root, err := hermes.ResolveHome(env)
	if err != nil {
		return ""
	}
	return root
}

// hermesReasoningLevels are the native --reasoning values Hermes accepts.
var hermesReasoningLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}

// discoverHermesCatalog lists the models Hermes cached for its configured
// provider, with the config.yaml default marked. Hermes keeps the provider in
// its own configuration and AO passes only --model, so models from any other
// provider would launch against the wrong endpoint and are left out.
func discoverHermesCatalog(base ports.AgentModelCatalog, paths []string) (ports.AgentModelCatalog, error) {
	if len(paths) != 2 {
		return base, fmt.Errorf("hermes model configuration was not found")
	}
	raw, err := readModelConfig(paths[0])
	if err != nil {
		if os.IsNotExist(err) {
			return base, fmt.Errorf("hermes model configuration was not found")
		}
		return base, fmt.Errorf("hermes model discovery read %s: %w", paths[0], err)
	}
	configured, err := parseHermesModels(raw)
	if err != nil {
		return base, fmt.Errorf("hermes model discovery parse %s: %w", paths[0], err)
	}
	models := configured
	if provider := hermesConfiguredProvider(raw); provider != "" {
		if cache, err := readModelConfig(paths[1]); err == nil {
			cached, err := parseHermesProviderCache(cache, provider)
			if err != nil {
				return base, fmt.Errorf("hermes model discovery parse %s: %w", paths[1], err)
			}
			models = append(models, cached...)
		} else if !os.IsNotExist(err) {
			return base, fmt.Errorf("hermes model discovery read %s: %w", paths[1], err)
		}
	}
	models = normalize(models)
	if len(models) == 0 {
		return base, fmt.Errorf("hermes model configuration returned no models")
	}
	base.Models = models
	base.SelectionMode = ports.ModelSelectionCatalog
	base.Source = "config"
	return base, nil
}

// hermesConfiguredProvider returns config.yaml's explicit model.provider.
// "auto" picks a provider from credentials at run time, so no cache applies.
func hermesConfiguredProvider(raw []byte) string {
	var config struct {
		Model yaml.Node `yaml:"model"`
	}
	if yaml.Unmarshal(raw, &config) != nil || config.Model.Kind != yaml.MappingNode {
		return ""
	}
	var model struct {
		Provider string `yaml:"provider"`
	}
	if config.Model.Decode(&model) != nil {
		return ""
	}
	if provider := strings.TrimSpace(model.Provider); provider != "auto" {
		return provider
	}
	return ""
}

// parseHermesProviderCache reads one provider's models from Hermes's
// provider_models_cache.json ({provider: {"models": [ids...]}}).
func parseHermesProviderCache(raw []byte, provider string) ([]ports.AgentModelInfo, error) {
	var cache map[string]struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil, err
	}
	entry := cache[provider]
	models := make([]ports.AgentModelInfo, 0, len(entry.Models))
	for _, id := range entry.Models {
		if id = strings.TrimSpace(id); id != "" {
			models = append(models, ports.AgentModelInfo{ID: id, Label: id, Provider: provider, Efforts: append([]string(nil), hermesReasoningLevels...)})
		}
	}
	return models, nil
}

// parseHermesModels reports the model Hermes's own configuration selects. The
// provider stays native configuration, so only that provider's default is a
// reproducible catalog entry; other model IDs can still be typed directly.
func parseHermesModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		Model yaml.Node `yaml:"model"`
		Agent struct {
			ReasoningEffort string `yaml:"reasoning_effort"`
		} `yaml:"agent"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	var id, provider string
	switch config.Model.Kind {
	case yaml.ScalarNode:
		id = config.Model.Value
	case yaml.MappingNode:
		var model struct {
			Default  string `yaml:"default"`
			Model    string `yaml:"model"`
			Provider string `yaml:"provider"`
		}
		if err := config.Model.Decode(&model); err != nil {
			return nil, err
		}
		id, provider = model.Default, model.Provider
		if strings.TrimSpace(id) == "" {
			id = model.Model
		}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if provider = strings.TrimSpace(provider); provider == "auto" {
		provider = ""
	}
	effort := strings.TrimSpace(config.Agent.ReasoningEffort)
	if !slices.Contains(hermesReasoningLevels, effort) {
		effort = ""
	}
	return []ports.AgentModelInfo{{
		ID: id, Label: id, Provider: provider, IsDefault: true,
		Efforts: append([]string(nil), hermesReasoningLevels...), DefaultEffort: effort,
	}}, nil
}

func qwenConfigHome(home string, env map[string]string) string {
	root := strings.TrimSpace(env["QWEN_HOME"])
	if root == "" {
		root = strings.TrimSpace(os.Getenv("QWEN_HOME"))
	}
	if root == "" {
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".qwen")
	}
	if root == "~" {
		return home
	}
	if strings.HasPrefix(root, "~/") {
		return filepath.Join(home, strings.TrimPrefix(root, "~/"))
	}
	return root
}

// deepseekProfileDir resolves the ACP profile directory, honouring DSH_HOME the
// way DeepSeek Harness itself does (its home-paths package falls back to ~/.dsh).
func deepseekProfileDir(home string, env map[string]string) string {
	root := strings.TrimSpace(env["DSH_HOME"])
	if root == "" {
		if home == "" {
			return ""
		}
		root = filepath.Join(home, ".dsh")
	}
	return filepath.Join(root, "profiles", "acp")
}

func readModelConfig(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // paths are fixed agent configuration locations
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, modelConfigReadLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > modelConfigReadLimit {
		return nil, fmt.Errorf("configuration exceeds %d bytes", modelConfigReadLimit)
	}
	return raw, nil
}

func configDiscoveryFingerprint(agentID, workingDir string, env map[string]string) string {
	if !hasConfigDiscoverySource(agentID) {
		return ""
	}
	return fingerprintConfigPaths(modelConfigPaths(agentID, workingDir, env))
}

// fingerprintConfigPaths hashes the contents of the configuration a discovery
// consults, so an edit to any of it invalidates the cached catalog. A path that
// does not exist contributes nothing, keeping the fingerprint stable until one
// appears.
func fingerprintConfigPaths(paths []string) string {
	hash := sha256.New()
	for _, path := range paths {
		raw, err := readModelConfig(path)
		if err != nil {
			continue
		}
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(raw)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:8])
}

func parseQwenModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		ModelProviders map[string][]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"modelProviders"`
		Model struct {
			Name string `json:"name"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	var models []ports.AgentModelInfo
	for provider, configured := range config.ModelProviders {
		for _, item := range configured {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			label := strings.TrimSpace(item.Name)
			if label == "" {
				label = id
			}
			models = append(models, ports.AgentModelInfo{
				ID: id, Label: label, Provider: provider,
				IsDefault: strings.EqualFold(id, strings.TrimSpace(config.Model.Name)),
			})
		}
	}
	return normalize(models), nil
}

func parseAutoHandModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		Provider string `json:"provider"`
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if providerRaw := values["provider"]; providerRaw != nil {
		if err := json.Unmarshal(providerRaw, &config.Provider); err != nil {
			return nil, err
		}
	}
	provider := strings.TrimSpace(config.Provider)
	if provider == "" {
		return nil, nil
	}
	var providerConfig struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(values[provider], &providerConfig); err != nil {
		return nil, err
	}
	modelID := strings.TrimSpace(providerConfig.Model)
	if modelID == "" {
		return nil, nil
	}
	selector := modelID
	if !strings.Contains(modelID, "/") {
		selector = provider + "/" + modelID
	}
	return []ports.AgentModelInfo{{
		ID: selector, Label: modelID, Provider: provider, IsDefault: true,
	}}, nil
}

func parseContinueModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		Models []struct {
			Name     string `yaml:"name"`
			Provider string `yaml:"provider"`
			Model    string `yaml:"model"`
		} `yaml:"models"`
		Defaults map[string]string `yaml:"defaults"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	defaults := make(map[string]bool, len(config.Defaults))
	for _, value := range config.Defaults {
		defaults[strings.ToLower(strings.TrimSpace(value))] = true
	}
	models := make([]ports.AgentModelInfo, 0, len(config.Models))
	for _, item := range config.Models {
		id := strings.TrimSpace(item.Model)
		if id == "" {
			continue
		}
		label := strings.TrimSpace(item.Name)
		if label == "" {
			label = id
		}
		models = append(models, ports.AgentModelInfo{
			ID: id, Label: label, Provider: strings.TrimSpace(item.Provider),
			IsDefault: defaults[strings.ToLower(id)] || defaults[strings.ToLower(label)],
		})
	}
	return normalize(models), nil
}

func parseGooseModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		ActiveProvider string `yaml:"active_provider"`
		Providers      map[string]struct {
			Model  string   `yaml:"model"`
			Models []string `yaml:"models"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	// AO can pass Goose a model override but not a provider override. Offering
	// another provider's models would produce a launch Goose cannot reproduce.
	provider := strings.TrimSpace(config.ActiveProvider)
	item, ok := config.Providers[provider]
	if !ok || provider == "" {
		return nil, nil
	}
	ids := append([]string(nil), item.Models...)
	if strings.TrimSpace(item.Model) != "" {
		ids = append(ids, item.Model)
	}
	models := make([]ports.AgentModelInfo, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		models = append(models, ports.AgentModelInfo{
			ID: id, Label: id, Provider: provider,
			IsDefault: id == strings.TrimSpace(item.Model),
		})
	}
	return normalize(models), nil
}

func parseVibeModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		ActiveModel string `toml:"active_model"`
		Models      []struct {
			Name     string `toml:"name"`
			Provider string `toml:"provider"`
			Alias    string `toml:"alias"`
		} `toml:"models"`
	}
	if err := toml.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	models := make([]ports.AgentModelInfo, 0, len(config.Models))
	for _, item := range config.Models {
		id := strings.TrimSpace(item.Alias)
		if id == "" {
			id = strings.TrimSpace(item.Name)
		}
		if id == "" {
			continue
		}
		label := strings.TrimSpace(item.Name)
		if label == "" {
			label = id
		}
		models = append(models, ports.AgentModelInfo{
			ID: id, Label: label, Provider: strings.TrimSpace(item.Provider),
			IsDefault: id == strings.TrimSpace(config.ActiveModel),
		})
	}
	return normalize(models), nil
}

func parseClineModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		LastUsedProvider string                     `json:"lastUsedProvider"`
		Providers        map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	var models []ports.AgentModelInfo
	for provider, rawProvider := range config.Providers {
		var value any
		if err := json.Unmarshal(rawProvider, &value); err != nil {
			return nil, err
		}
		ids := configuredModelIDs(value)
		for _, id := range ids {
			models = append(models, ports.AgentModelInfo{
				ID: id, Label: id, Provider: provider,
				IsDefault: provider == config.LastUsedProvider,
			})
		}
	}
	return normalize(models), nil
}

func configuredModelIDs(value any) []string {
	var ids []string
	var walk func(any)
	walk = func(current any) {
		switch node := current.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			for key, child := range node {
				lower := strings.ToLower(key)
				if text, ok := child.(string); ok && (lower == "model" || lower == "modelid" || lower == "apimodelid") {
					if id := strings.TrimSpace(text); looksLikeModelID(id) {
						ids = append(ids, id)
					}
					continue
				}
				walk(child)
			}
		}
	}
	walk(value)
	sort.Strings(ids)
	return ids
}
