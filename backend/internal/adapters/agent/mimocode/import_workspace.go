package mimocode

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// validateMiMoImportedWorkspace preserves the implicit-local identity used by
// execution-ownership.ts:26-32, using the effective launch environment. Shared
// restore hooks identify the prepared native binding on first and later opens.
func validateMiMoImportedWorkspace(cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir == "" || cfg.NativeSessionID == "" {
		return nil
	}
	value := func(key string) string {
		if v, ok := cfg.Env[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	// 3de989f packages/cli/src/flag/flag.ts:424 feeds InstanceState.workspaceID.
	if value("MIMOCODE_WORKSPACE_ID") != "" {
		return errors.New("mimo imported history requires an implicit local workspace")
	}
	if path := value("MIMOCODE_DB"); path != "" && path != "mimocode.db" && filepath.Clean(path) != filepath.Join(cfg.ImportConfigDir, "mimocode.db") {
		return errors.New("mimo imported history cannot use a different database")
	}
	return nil
}
