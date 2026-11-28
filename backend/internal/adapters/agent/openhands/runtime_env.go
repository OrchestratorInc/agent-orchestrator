package openhands

import (
	"errors"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
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
	if value, explicit := cfg.Env["OPENHANDS_CONVERSATIONS_DIR"]; explicit {
		recorded, rootErr := filepath.EvalSymlinks(root)
		override, overrideErr := filepath.EvalSymlinks(value)
		if rootErr != nil || overrideErr != nil || !filepath.IsAbs(override) || filepath.Clean(override) != filepath.Clean(recorded) {
			return nil, errors.New("openhands conversation root override does not match the imported store")
		}
	}
	return append([]string{"env", "OPENHANDS_CONVERSATIONS_DIR=" + root}, cmd...), nil
}
