package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/jcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// discoverJCodeCatalog lists the models of JCode's active provider with
// `jcode model list --json`. JCODE_NON_INTERACTIVE keeps a signed-out CLI from
// opening its login picker; it exits with "No credentials configured" instead.
func discoverJCodeCatalog(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	base := Base(request.AgentID)
	if strings.TrimSpace(request.Binary) == "" {
		return base, errors.New("agent binary is not installed")
	}
	env := maps.Clone(request.Env)
	if env == nil {
		env = map[string]string{}
	}
	env["JCODE_NON_INTERACTIVE"] = "1"
	env["JCODE_CHECK_UPDATES"] = "0"
	env["JCODE_NO_AUTO_UPDATE"] = "1"
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := modelCommand(runCtx, request.Binary, []string{"--no-update", "model", "list", "--json"}, request.WorkingDir, env).CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "No credentials configured") {
			return base, fmt.Errorf("jcode model discovery: %w", ports.ErrAgentModelDiscoverySignInRequired)
		}
		return base, modelDiscoveryError(runCtx, request.AgentID, err, output)
	}
	var listed struct {
		Provider      string   `json:"provider"`
		SelectedModel string   `json:"selected_model"`
		Models        []string `json:"models"`
	}
	if err := json.Unmarshal(output, &listed); err != nil {
		return base, fmt.Errorf("jcode model discovery: %w", err)
	}
	ids, err := jcodeLocalOllamaModels(runCtx, request, env, listed.Provider, listed.Models)
	if err != nil {
		return base, fmt.Errorf("jcode model discovery: %w", err)
	}
	models := make([]ports.AgentModelInfo, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			models = append(models, ports.AgentModelInfo{ID: id, Label: id, Provider: listed.Provider, IsDefault: id == listed.SelectedModel})
		}
	}
	models = normalize(models)
	if len(models) == 0 {
		return base, errors.New("jcode model discovery returned no models")
	}
	base.Models = models
	base.Source = "cli"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}

// jcodeLocalOllamaModels narrows jcode's list to the models the local Ollama
// server has installed when jcode runs in its keyless local Ollama mode.
// jcode's list mixes the server's models with its built-in hosted model names,
// which a local Ollama server does not serve. IDs stay exactly as jcode lists
// them. Any other provider or mode keeps jcode's own list.
func jcodeLocalOllamaModels(ctx context.Context, request ports.AgentModelDiscoveryRequest, env map[string]string, provider string, listed []string) ([]string, error) {
	if !strings.EqualFold(strings.TrimSpace(provider), "ollama") {
		return listed, nil
	}
	status, err := modelCommand(ctx, request.Binary, []string{"--no-update", "auth", "status", "--json"}, request.WorkingDir, env).Output()
	if err != nil {
		return listed, nil //nolint:nilerr // without jcode's mode report, its own list stands
	}
	inventory, ok := jcode.OllamaInventoryURL(status)
	if !ok {
		return listed, nil
	}
	installed, err := jcode.InstalledOllamaModels(ctx, inventory)
	if err != nil {
		return nil, fmt.Errorf("local Ollama server at %s: %w", inventory, err)
	}
	served := make([]string, 0, len(listed))
	for _, id := range listed {
		if jcode.OllamaModelInstalled(installed, id) {
			served = append(served, id)
		}
	}
	return served, nil
}
