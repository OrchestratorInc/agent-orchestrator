package modelcatalog

import (
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// parseNeovateModels exposes explicit native configuration plus the built-in
// models of providers whose key was saved there by native /login.
func parseNeovateModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		Model       string `json:"model"`
		PlanModel   string `json:"planModel"`
		SmallModel  string `json:"smallModel"`
		VisionModel string `json:"visionModel"`
		Provider    map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	var models []ports.AgentModelInfo
	if id := strings.TrimSpace(config.Model); id != "" {
		models = append(models, ports.AgentModelInfo{ID: id, Label: id, IsDefault: true})
	}
	for _, id := range []string{config.PlanModel, config.SmallModel, config.VisionModel} {
		if id = strings.TrimSpace(id); id != "" {
			models = append(models, ports.AgentModelInfo{ID: id, Label: id})
		}
	}
	for provider, config := range config.Provider {
		for name := range config.Models {
			models = append(models, ports.AgentModelInfo{ID: provider + "/" + name, Label: name, Provider: provider})
		}
		if strings.TrimSpace(config.Options.APIKey) != "" {
			models = append(models, neovateProviderModels(provider)...)
		}
	}
	return normalize(models), nil
}

// neovateEnvModels returns the built-in models whose provider key is set, in
// provider preference order.
func neovateEnvModels(env map[string]string) []ports.AgentModelInfo {
	var models []ports.AgentModelInfo
	for _, provider := range neovateBuiltInProviders {
		for _, name := range provider.env {
			if envValue(env, name) != "" {
				models = append(models, neovateProviderModels(provider.id)...)
				break
			}
		}
	}
	return models
}

// neovateAllModels returns every built-in model. One whose provider has no
// credentials stays selectable: Neovate rejects it until /login stores a key.
func neovateAllModels() []ports.AgentModelInfo {
	var models []ports.AgentModelInfo
	for _, provider := range neovateBuiltInProviders {
		models = append(models, neovateProviderModels(provider.id)...)
	}
	return models
}

func neovateProviderModels(id string) []ports.AgentModelInfo {
	for _, provider := range neovateBuiltInProviders {
		if provider.id != id {
			continue
		}
		models := make([]ports.AgentModelInfo, 0, len(provider.models))
		for _, name := range provider.models {
			models = append(models, ports.AgentModelInfo{ID: id + "/" + name, Label: name, Provider: id})
		}
		return models
	}
	return nil
}
