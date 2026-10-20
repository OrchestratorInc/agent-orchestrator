package modelcatalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/fx"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LocalConfigured reads the harness's existing local configuration sources.
// It never runs the CLI, asks a provider for models, or returns the public
// catalog. An empty result means configuration was not established locally.
// A configured provider with no explicit model selects its native default.
func LocalConfigured(ctx context.Context, request ports.AgentModelDiscoveryRequest) ports.AgentModelCatalog {
	result := ports.AgentModelCatalog{AgentID: request.AgentID, Source: "local-configuration"}
	if ctx.Err() != nil {
		return result
	}
	if hasConfigDiscoverySource(request.AgentID) {
		if catalog, err := discoverConfigCatalog(request.AgentID, request.WorkingDir, request.Env); err == nil {
			result.Models = catalog.Models
		}
	}
	configured := configuredDefaultModel(request.AgentID, request.WorkingDir, request.Env)
	switch request.AgentID {
	case "pi", "kimchi":
		configured = readConfiguredModel(filepath.Join(piCatalogDir(request.AgentID, request.Env), "settings.json"), parsePiDefaultModel, "")
	case "prime-agent":
		configured = readConfiguredModel(filepath.Join(primeAgentDir(request.Env), "settings.json"), parsePiDefaultModel, "")
	case "fx":
		configured = envValue(request.Env, "FX_MODEL")
		if provider := envValue(request.Env, "FX_PROVIDER"); configured != "" && provider != "" {
			configured = fx.CatalogModelID(provider, configured)
		}
	case "openhands":
		root := envValue(request.Env, "OPENHANDS_PERSISTENCE_DIR")
		if root == "" {
			root = filepath.Join(catalogHome(request.Env), ".openhands")
		}
		configured = readConfiguredModel(filepath.Join(root, "agent_settings.json"), func(raw []byte) string {
			var settings struct {
				LLM struct {
					Model string `json:"model"`
				} `json:"llm"`
			}
			if json.Unmarshal(raw, &settings) != nil {
				return ""
			}
			return settings.LLM.Model
		}, "")
	case "gemini":
		configured = readConfiguredModel(filepath.Join(catalogHome(request.Env), ".gemini", "settings.json"), func(raw []byte) string {
			var settings struct {
				Model struct {
					Name string `json:"name"`
				} `json:"model"`
			}
			if json.Unmarshal(raw, &settings) != nil {
				return ""
			}
			return settings.Model.Name
		}, "")
	case "unreal-agent":
		// An explicit route/model is local configuration. The adapter's built-in
		// model is not evidence that the user configured this harness.
		if envValue(request.Env, "UNREAL_HARNESS_LLM_PROVIDER") != "" || envValue(request.Env, "UNREAL_HARNESS_LLM_API_KEY") != "" || envValue(request.Env, "UNREAL_HARNESS_LLM_MODEL") != "" {
			_, configured = unrealConfiguredModel(request.Env)
		}
	}
	result.Models = applyConfiguredDefault(result.Models, configured)
	markConfiguredProviders(ctx, &result, request)
	if len(result.Models) == 0 && len(result.ConfiguredProviders) > 0 {
		result.Models = []ports.AgentModelInfo{{ID: "", Label: "Configured default", IsDefault: true}}
	}
	// These are documented harness-owned key variables used by the existing
	// native adapters. Presence selects that harness's configured default;
	// values and public model tables never leave this function.
	if len(result.Models) == 0 {
		keys := map[string][]string{
			"amp": {"AMP_API_KEY"}, "auggie": {"AUGMENT_SESSION_AUTH"}, "cursor": {"CURSOR_API_KEY"},
			"devin": {"DEVIN_API_KEY"}, "droid": {"FACTORY_API_KEY"}, "gemini": {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
			"grok": {"XAI_API_KEY"}, "kiro": {"KIRO_API_KEY"}, "muse": {"META_API_KEY"},
			"memcode":          {"MEMCODE_API_TOKEN", "MEMCODE_CREDENTIALS", "MEMCODE_ENDPOINT_URL"},
			"copilot":          {"COPILOT_GITHUB_TOKEN", "COPILOT_PROVIDER_BASE_URL"},
			"deepseek-harness": {"DEEPSEEK_API_KEY"},
		}
		for _, key := range keys[request.AgentID] {
			if credentialPresence(envValue(request.Env, key)) != "" {
				result.Models = []ports.AgentModelInfo{{ID: "", Label: "Configured default", IsDefault: true}}
				break
			}
		}
	}
	// Preserve exact native selectors and explicit defaults. A local cache of
	// all provider models is deliberately not a configured model list.
	for i := range result.Models {
		result.Models[i].ID = strings.TrimSpace(result.Models[i].ID)
	}
	return result
}
