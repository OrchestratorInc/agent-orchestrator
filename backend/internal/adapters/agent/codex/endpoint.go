package codex

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
)

// endpointProviderID names the model provider AO defines for a user's custom
// endpoint. It lives only in launch overrides, never in the user's config.
const endpointProviderID = "ao_endpoint"

// EndpointArgs returns the Codex config overrides that point it at the custom
// endpoint carried in env, or nil when env carries none. The key stays in env:
// the provider's env_key names the variable holding it, so it never appears on
// a command line. A model set on the endpoint is the default; an explicit
// --model still wins.
func EndpointArgs(env map[string]string) []string {
	baseURL := strings.TrimSpace(env[harnessendpoint.CodexBaseURLEnv])
	if baseURL == "" || strings.TrimSpace(env[harnessendpoint.CodexAPIKeyEnv]) == "" {
		return nil
	}
	prefix := "model_providers." + endpointProviderID + "."
	args := []string{
		"-c", prefix + "name=" + codexTOMLConfigString("Custom endpoint"),
		"-c", prefix + "base_url=" + codexTOMLConfigString(baseURL),
		"-c", prefix + "env_key=" + codexTOMLConfigString(harnessendpoint.CodexAPIKeyEnv),
		"-c", prefix + "wire_api=" + codexTOMLConfigString("responses"),
		"-c", "model_provider=" + codexTOMLConfigString(endpointProviderID),
	}
	if model := strings.TrimSpace(env[harnessendpoint.CodexModelEnv]); model != "" {
		args = append(args, "-c", "model="+codexTOMLConfigString(model))
	}
	return args
}

// appendEndpointFlags uses a prepared launch environment when supplied. A
// caller without one must load the saved endpoint before constructing argv.
func appendEndpointFlags(args *[]string, env map[string]string, dataDir string) error {
	if env == nil {
		stored, err := harnessendpoint.ForDataDir(dataDir).Env(string(domain.HarnessCodex))
		if err != nil {
			return fmt.Errorf("load custom endpoint for codex: %w", err)
		}
		env = stored
	}
	*args = append(*args, EndpointArgs(env)...)
	return nil
}
