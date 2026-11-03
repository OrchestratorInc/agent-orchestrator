package mimocode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var verifyConnection = authprobe.VerifyConnection

// verifySelectedConnection follows MiMo's configured-model / recent-model
// selection. Unknown defaults, plugins and unsupported wire protocols remain
// configured: verifying an arbitrary stored provider would be misleading.
func verifySelectedConnection(ctx context.Context, binary string) ports.AgentAuthStatus {
	step := domain.NewAgentAuthDiagnostic("provider_validation", "provider_inconclusive")
	defer func() {
		// AuthStatus records the timeout/cancellation diagnostic if this whole
		// verification budget expires. Otherwise every exit carries its outcome.
		if ctx.Err() == nil {
			ports.RecordAuthDiagnostic(ctx, step)
		}
	}()
	var cfg struct {
		Model    string            `json:"model"`
		Plugin   []json.RawMessage `json:"plugin"`
		Provider map[string]struct {
			NPM     string   `json:"npm"`
			Env     []string `json:"env"`
			Options struct {
				BaseURL string            `json:"baseURL"`
				APIKey  string            `json:"apiKey"`
				Headers map[string]string `json:"headers"`
			} `json:"options"`
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	raw, err := runAuthProbe(ctx, binary, "debug", "config")
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		return ports.AgentAuthStatusConfigured
	}
	if len(cfg.Plugin) > 0 {
		step = domain.NewAgentAuthDiagnostic("provider_validation", "provider_unsupported")
		return ports.AgentAuthStatusConfigured
	}
	paths, err := runAuthProbe(ctx, binary, "debug", "paths")
	if err != nil {
		return ports.AgentAuthStatusConfigured
	}
	dirs := map[string]string{}
	for _, line := range strings.Split(string(paths), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			dirs[key] = strings.TrimSpace(value)
		}
	}
	if cfg.Model == "" && dirs["state"] == "" {
		return ports.AgentAuthStatusConfigured
	}
	models, err := runAuthProbe(ctx, binary, "models")
	if err != nil {
		return ports.AgentAuthStatusConfigured
	}
	available := map[string]bool{}
	for _, line := range strings.Split(string(models), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			available[fields[0]] = true
		}
	}
	model := cfg.Model
	if model == "" {
		var state struct {
			Recent []struct {
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
			} `json:"recent"`
		}
		if dirs["state"] != "" {
			if raw, err := os.ReadFile(filepath.Join(dirs["state"], "model.json")); err == nil && json.Unmarshal(raw, &state) == nil {
				for _, recent := range state.Recent {
					id := recent.ProviderID + "/" + recent.ModelID
					if available[id] {
						model = id
						break
					}
				}
			}
		}
	}
	provider, modelID, ok := strings.Cut(model, "/")
	if !ok || !available[model] {
		step = domain.NewAgentAuthDiagnostic("model_selection", "model_selection_unresolved")
		return ports.AgentAuthStatusConfigured
	}
	var baseURL, envKey string
	switch provider {
	case "zai", "zai-coding-plan":
		baseURL, envKey = "https://api.z.ai/api/paas/v4", "ZHIPU_API_KEY"
		if provider == "zai-coding-plan" {
			baseURL = "https://api.z.ai/api/coding/paas/v4"
		}
	case "zhipuai", "zhipuai-coding-plan":
		baseURL, envKey = "https://open.bigmodel.cn/api/paas/v4", "ZHIPU_API_KEY"
		if provider == "zhipuai-coding-plan" {
			baseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
		}
	default:
		step = domain.NewAgentAuthDiagnostic("provider_validation", "provider_unsupported")
		return ports.AgentAuthStatusConfigured
	}
	options := cfg.Provider[provider]
	// Custom model definitions can change the model id, URL or SDK. Do not
	// approximate their effective route or custom authentication headers.
	if len(options.Models) > 0 || len(options.Options.Headers) > 0 || len(options.Env) > 0 || (options.NPM != "" && options.NPM != "@ai-sdk/openai-compatible") {
		step = domain.NewAgentAuthDiagnostic("provider_validation", "provider_unsupported")
		return ports.AgentAuthStatusConfigured
	}
	if options.Options.BaseURL != "" {
		baseURL = options.Options.BaseURL
	}
	key := os.Getenv(envKey)
	identity := domain.EnvironmentSource(envKey).WithMethod(domain.AgentAccountMethodAPIKey)
	if dirs["data"] != "" {
		path := filepath.Join(dirs["data"], "auth.json")
		var credentials map[string]struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		}
		raw, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return ports.AgentAuthStatusConfigured
		}
		if err == nil {
			if json.Unmarshal(raw, &credentials) != nil {
				return ports.AgentAuthStatusConfigured
			}
			if credential, found := credentials[provider]; found {
				if credential.Type != "api" {
					return ports.AgentAuthStatusConfigured
				}
				key = credential.Key
				identity = authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey)
			}
		}
	}
	if options.Options.APIKey != "" {
		key = options.Options.APIKey
		identity = domain.CommandSource(binary, "debug", "config").WithMethod(domain.AgentAccountMethodAPIKey)
	}
	if key == "" {
		return ports.AgentAuthStatusConfigured
	}
	identity.Provider = provider
	ports.RecordAuthEvidence(ctx, identity)
	var status ports.AgentAuthStatus
	status, step = verifyConnection(ctx, authprobe.Connection{Provider: provider, BaseURL: baseURL, Model: modelID, APIKey: key}, nil)
	return status
}
