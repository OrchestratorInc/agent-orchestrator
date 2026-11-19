package openhands

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// importedEndpointCommand fences the ambient SDK fallback only for an imported
// conversation explicitly launched with an OpenAI model. Fresh/managed sessions,
// other providers and explicit session endpoints keep their configured routing.
func importedEndpointCommand(cmd []string, cfg ports.RestoreConfig) []string {
	if cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] == "" {
		return cmd
	}
	if _, explicit := cfg.Env["OPENAI_BASE_URL"]; explicit || sessionEnv(cfg.Env, "LLM_BASE_URL") != "" {
		return cmd
	}
	model := strings.TrimSpace(cfg.Config.Model)
	if model == "" {
		model = strings.TrimSpace(sessionEnv(cfg.Env, "LLM_MODEL"))
	}
	if !strings.HasPrefix(model, "openai/") {
		return cmd
	}
	return append([]string{"env", "OPENAI_BASE_URL=https://api.openai.com/v1"}, cmd...)
}
