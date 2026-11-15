package modelcatalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/fx"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var fxProviderName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

type fxCatalogRunner func(context.Context, string, []string, string, map[string]string) ([]byte, error)

func fxSettingsPath(env map[string]string) string {
	return filepath.Join(catalogHome(env), ".fx", "settings.json")
}

func discoverFXCatalog(ctx context.Context, request ports.AgentModelDiscoveryRequest, run fxCatalogRunner) (ports.AgentModelCatalog, error) {
	result := Base("fx")
	result.Source = "cli"
	result.FetchedAt = time.Now().UTC()
	result.Providers = []string{"gateway", "codex", "grok"}
	type modelSelection struct {
		Provider   string            `json:"provider"`
		Models     map[string]string `json:"models"`
		CodexModel string            `json:"codex_model"`
		GrokModel  string            `json:"grok_model"`
	}
	var settings struct {
		modelSelection
		Workspaces map[string]modelSelection `json:"workspaces"`
		Providers  map[string]struct {
			ModelMetadata map[string]struct {
				ContextWindow int64 `json:"context_window"`
			} `json:"model_metadata"`
		} `json:"providers"`
	}
	if raw, err := readProviderCatalog(fxSettingsPath(request.Env)); err == nil {
		if json.Unmarshal(raw, &settings) != nil {
			settings.Providers = nil
		}
	}
	var custom []string
	for provider := range settings.Providers {
		if fxProviderName.MatchString(provider) && provider != "gateway" && provider != "codex" && provider != "grok" {
			custom = append(custom, provider)
		}
	}
	sort.Strings(custom)
	result.Providers = append(result.Providers, custom...)
	for _, provider := range custom {
		var ids []string
		for id := range settings.Providers[provider].ModelMetadata {
			if strings.TrimSpace(id) != "" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			result.Models = append(result.Models, ports.AgentModelInfo{
				ID: fx.CatalogModelID(provider, id), Label: id, Provider: provider,
				ContextWindow: max(0, settings.Providers[provider].ModelMetadata[id].ContextWindow),
			})
		}
	}
	statusCtx, cancelStatus := context.WithTimeout(ctx, signInCheckTimeout)
	output, statusErr := run(statusCtx, request.Binary, []string{"status", "--json"}, request.WorkingDir, request.Env)
	cancelStatus()
	var status struct {
		ConnectedProviders []string `json:"connected_providers"`
	}
	workingDir := request.WorkingDir
	if workingDir == "" {
		workingDir = "."
	}
	var workspace modelSelection
	if absolute, err := filepath.Abs(workingDir); err == nil {
		if canonical, err := filepath.EvalSymlinks(absolute); err == nil {
			workspace = settings.Workspaces[canonical]
		}
	}
	setUp := map[string]bool{"gateway": true, envValue(request.Env, "FX_PROVIDER"): true}
	for _, selection := range []modelSelection{settings.modelSelection, workspace} {
		setUp[selection.Provider] = true
		savedModels := map[string]string{"codex": selection.CodexModel, "grok": selection.GrokModel}
		for provider, model := range selection.Models {
			savedModels[provider] = model
		}
		for provider, model := range savedModels {
			if strings.TrimSpace(model) != "" {
				setUp[provider] = true
			}
		}
	}
	if statusErr == nil && json.Unmarshal(output, &status) == nil {
		connected := map[string]bool{}
		for _, provider := range status.ConnectedProviders {
			connected[provider] = true
			setUp[provider] = true
		}
		for _, provider := range result.Providers {
			if connected[provider] {
				result.ConfiguredProviders = append(result.ConfiguredProviders, provider)
			}
		}
	}

	type providerResult struct {
		models []ports.AgentModelInfo
		failed bool
	}
	results := make([]providerResult, 3)
	var wg sync.WaitGroup
	for index, provider := range result.Providers[:3] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env := make(map[string]string, len(request.Env)+1)
			for key, value := range request.Env {
				env[key] = value
			}
			env["FX_PROVIDER"] = provider
			runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
			defer cancel()
			output, err := run(runCtx, request.Binary, []string{"models", "--json"}, request.WorkingDir, env)
			if err != nil {
				results[index].failed = true
				return
			}
			models, err := parseFXModels(output)
			if err != nil || len(models) == 0 {
				results[index].failed = true
				return
			}
			for i := range models {
				models[i].Provider = provider
				models[i].ID = fx.CatalogModelID(provider, models[i].ID)
			}
			results[index].models = models
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var unavailable []string
	for index, provider := range result.Providers[:3] {
		result.Models = append(result.Models, results[index].models...)
		if results[index].failed && setUp[provider] {
			unavailable = append(unavailable, provider)
		}
	}
	if len(unavailable) > 0 {
		result.Metadata = map[string]string{"unavailableProviders": strings.Join(unavailable, ",")}
		result.Warning = "FX could not list models for: " + strings.Join(unavailable, ", ") + ". Previously cached models may be shown; check that provider's login or connection."
	}
	sort.SliceStable(result.Models, func(i, j int) bool {
		left, right := strings.ToLower(result.Models[i].Label), strings.ToLower(result.Models[j].Label)
		if left != right {
			return left < right
		}
		return result.Models[i].ID < result.Models[j].ID
	})
	return result, nil
}
