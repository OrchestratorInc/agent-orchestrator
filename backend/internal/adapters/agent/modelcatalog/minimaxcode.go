package modelcatalog

import (
	"encoding/json"
	"fmt"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"strings"
)

// parseMiniMaxModels reads configured model IDs, never provider rows or keys.
func parseMiniMaxModels(data []byte) ([]ports.AgentModelInfo, error) {
	var snapshot struct {
		Providers []struct {
			ID      string `json:"providerId"`
			Enabled bool   `json:"enabled"`
			Models  []struct {
				ID       string `json:"modelId"`
				Name     string `json:"displayName"`
				Selected bool   `json:"selected"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("MiniMax model list is not valid JSON")
	}
	models := []ports.AgentModelInfo{}
	for _, p := range snapshot.Providers {
		if !p.Enabled || strings.TrimSpace(p.ID) == "" {
			continue
		}
		for _, m := range p.Models {
			if strings.TrimSpace(m.ID) == "" {
				continue
			}
			id := p.ID + "/" + m.ID
			name := m.Name
			if name == "" {
				name = id
			}
			models = append(models, model(id, name, m.Selected))
		}
	}
	return models, nil
}
