package openhands

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The source and private store intentionally retain the same native ID. Never
// let a later launch find the source merely because it also contains that ID.
func importedConversationCommand(cmd []string, cfg ports.RestoreConfig) ([]string, error) {
	if cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] == "" {
		return cmd, nil
	}
	root := cfg.Session.Metadata[ports.MetadataKeyImportConfigDir]
	if !filepath.IsAbs(root) {
		return nil, errors.New("openhands imported restore requires its recorded config root")
	}
	if value, explicit := cfg.Env["OPENHANDS_CONVERSATIONS_DIR"]; explicit && filepath.Clean(value) != filepath.Clean(root) {
		return nil, errors.New("openhands conversation root override does not match the imported store")
	}
	return append([]string{"env", "OPENHANDS_CONVERSATIONS_DIR=" + root}, cmd...), nil
}

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
