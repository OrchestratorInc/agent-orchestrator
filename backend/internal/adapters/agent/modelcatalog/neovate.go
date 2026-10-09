package modelcatalog

import (
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// parseNeovateModels exposes only explicit native configuration. Neovate has
// no model-list CLI; arbitrary model IDs must first be configured upstream.
func parseNeovateModels(raw []byte) ([]ports.AgentModelInfo, error) {
	var config struct {
		Model       string `json:"model"`
		PlanModel   string `json:"planModel"`
		SmallModel  string `json:"smallModel"`
		VisionModel string `json:"visionModel"`
		Provider    map[string]struct {
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	var models []ports.AgentModelInfo
	for _, id := range []string{config.Model, config.PlanModel, config.SmallModel, config.VisionModel} {
		if id = strings.TrimSpace(id); id != "" {
			models = append(models, ports.AgentModelInfo{ID: id, Label: id})
		}
	}
	for provider, config := range config.Provider {
		for name := range config.Models {
			models = append(models, ports.AgentModelInfo{ID: provider + "/" + name, Label: name, Provider: provider})
		}
	}
	return normalize(models), nil
}
