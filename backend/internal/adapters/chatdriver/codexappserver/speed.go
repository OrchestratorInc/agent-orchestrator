package codexappserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// filterSpeedTiers checks the effective provider and managed feature policy in
// the same app-server process. Missing policy support leaves Normal available.
// See https://learn.chatgpt.com/docs/agent-configuration/speed and the
// config/read and configRequirements/read contracts in the app-server docs.
func filterSpeedTiers(ctx context.Context, connection *conn, models []ports.ChatModel, workdir string) {
	hasTiers := false
	for _, model := range models {
		hasTiers = hasTiers || len(model.ServiceTiers) > 0
	}
	if !hasTiers {
		return
	}
	var config struct {
		Config *struct {
			Provider string `json:"model_provider"`
			Model    string `json:"model"`
		} `json:"config"`
	}
	var policy struct {
		Requirements json.RawMessage `json:"requirements"`
	}
	params := map[string]any{"includeLayers": false}
	if workdir != "" {
		params["cwd"] = workdir
	}
	allowed := connection.request(ctx, "config/read", params, &config) == nil &&
		config.Config != nil && (config.Config.Provider == "" || config.Config.Provider == "openai")
	if allowed {
		allowed = connection.request(ctx, "configRequirements/read", map[string]any{}, &policy) == nil && len(policy.Requirements) > 0
	}
	if allowed {
		var requirements struct {
			Features map[string]bool `json:"featureRequirements"`
		}
		allowed = json.Unmarshal(policy.Requirements, &requirements) == nil
		if enabled, configured := requirements.Features["fast_mode"]; configured && !enabled {
			allowed = false
		}
	}
	if !allowed {
		for i := range models {
			models[i].ServiceTiers = nil
		}
		return
	}
	// Follow an effective configured model only when it is selectable in the
	// catalog. Speed policy failures must not change the model picker's default.
	for _, model := range models {
		if model.ID == config.Config.Model {
			for i := range models {
				models[i].Default = models[i].ID == config.Config.Model
			}
			break
		}
	}
}

func (c *conversation) speedTier(ctx context.Context, modelID string, speed domain.SpeedMode) (string, error) {
	if !speed.Valid() {
		return "", fmt.Errorf("%w: %q", ports.ErrUnsupportedSpeed, speed)
	}
	if !speed.Accelerated() {
		return "default", nil
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	models, err := listModels(ctx, c.conn, c.workspacePath)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ports.ErrUnsupportedSpeed, err)
	}
	if modelID == "" {
		modelID = c.threadModel
	}
	for _, model := range models {
		if model.ID == modelID || modelID == "" && model.Default {
			if tier, supported := ports.ServiceTierForSpeed(model.ServiceTiers, speed); supported {
				return tier, nil
			}
			break
		}
	}
	return "", fmt.Errorf("%w: %s is not advertised for model %q", ports.ErrUnsupportedSpeed, speed, modelID)
}

func acknowledgedSpeed(requested string, actual *string) bool {
	if requested == "default" {
		return actual == nil || *actual == "default"
	}
	if actual == nil {
		return false
	}
	return *actual == requested || (requested == "fast" || requested == "priority") && (*actual == "fast" || *actual == "priority")
}
