package hermes

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

// selectedConnection supports the native Z.AI chat-completions route. Other
// providers and ambiguous pool/config overrides stay unverified rather than
// validating an unrelated account or guessing a provider's wire protocol.
func selectedConnection(home string) (authprobe.Connection, domain.AgentAccountIdentity, bool) {
	var route authprobe.Connection
	var identity domain.AgentAccountIdentity
	raw, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		return route, identity, false
	}
	var cfg map[string]any
	if unmarshalYAML11(raw, &cfg) != nil || refuseSecretSources(cfg) != nil {
		return route, identity, false
	}
	if active, _ := cfg["active_profile"].(string); active != "" && active != "default" {
		return route, identity, false
	}
	model, _ := cfg["model"].(map[string]any)
	text := func(name string) string { value, _ := model[name].(string); return strings.TrimSpace(value) }
	provider := strings.ToLower(text("provider"))
	switch provider {
	case "zai", "glm", "z-ai", "z.ai", "zhipu":
	default:
		return route, identity, false
	}
	if mode := text("api_mode"); mode != "" && mode != "chat_completions" {
		return route, identity, false
	}
	if text("api_key") != "" {
		return route, identity, false
	}
	if _, err := os.Stat(filepath.Join(home, ".op.env")); err == nil {
		return route, identity, false
	}
	assigned, known := overridingAssignments(home)
	if !known {
		return route, identity, false
	}
	lookup := func(name string) string {
		if value, ok := assigned[processEnvName(name)]; ok {
			return strings.TrimSpace(value)
		}
		return strings.TrimSpace(os.Getenv(name))
	}
	keys := []string{"GLM_API_KEY", "ZAI_API_KEY", "Z_AI_API_KEY"}
	if keyEnv := text("key_env"); keyEnv != "" {
		keys = append([]string{keyEnv}, keys...)
	} else if keyEnv := text("api_key_env"); keyEnv != "" {
		keys = append([]string{keyEnv}, keys...)
	}
	keyName := ""
	for _, name := range keys {
		if key := lookup(name); key != "" {
			route.APIKey, keyName = key, name
			break
		}
	}
	route.Provider, route.Model, route.BaseURL = "zai", text("default"), text("base_url")
	if route.Model == "" {
		route.Model = text("model")
	}
	// Hermes may auto-detect a different Z.AI billing/region endpoint when
	// none is explicitly selected. Do not substitute the general API route.
	if route.BaseURL == "" {
		route.BaseURL = lookup("GLM_BASE_URL")
	}
	if route.APIKey == "" || route.Model == "" || route.BaseURL == "" || strings.Contains(route.BaseURL, "${") || strings.Contains(route.Model, "${") {
		return route, identity, false
	}
	if raw, err := os.ReadFile(filepath.Join(home, "auth.json")); err == nil {
		var store struct {
			Pool map[string][]map[string]any `json:"credential_pool"`
		}
		if json.Unmarshal(raw, &store) != nil {
			return route, identity, false
		}
		entries := store.Pool["zai"]
		if len(entries) > 1 {
			return route, identity, false
		}
		for _, entry := range entries {
			source, _ := entry["source"].(string)
			base, _ := entry["base_url"].(string)
			if holdsSecret(entry) || source != "env:"+keyName || (base != "" && strings.TrimRight(base, "/") != strings.TrimRight(route.BaseURL, "/")) {
				return route, identity, false
			}
		}
	} else if !os.IsNotExist(err) {
		return route, identity, false
	}
	identity = domain.EnvironmentSource(keyName).WithMethod(domain.AgentAccountMethodAPIKey)
	if _, ok := assigned[processEnvName(keyName)]; ok {
		identity = authprobe.FileSource(filepath.Join(home, ".env")).WithMethod(domain.AgentAccountMethodAPIKey)
	}
	identity.Provider = "zai"
	return route, identity, true
}

func verifyAuthentication(ctx context.Context, status ports.AgentAuthStatus, identity domain.AgentAccountIdentity, steps []domain.AgentAuthDiagnostic) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	if status != ports.AgentAuthStatusConfigured {
		return status, identity, steps, nil
	}
	home, err := ResolveHome(nil)
	if err == nil {
		if route, selected, ok := selectedConnection(home); ok {
			status, step := verifyConnection(ctx, route, nil)
			ports.RecordAuthEvidence(ctx, selected)
			// Credentials are already established. Verification interruption is
			// inconclusive and is carried by the provider diagnostic.
			return status, selected, append(steps, step), nil
		}
	}
	step := domain.NewAgentAuthDiagnostic("provider_validation", "provider_unsupported")
	step.Provider = identity.Provider
	return status, identity, append(steps, step), nil
}
