package kilocode

import (
	"errors"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// validateKiloImportedWorkspace runs with the effective launch environment,
// after S2 has selected the exact native binding. Ordinary sessions retain
// their provider workspace behavior. Imported local graphs must remain local.
func validateKiloImportedWorkspace(cfg ports.WorkspaceHookConfig) error {
	if cfg.ImportConfigDir == "" || cfg.NativeSessionID == "" {
		return nil
	}
	value := func(key string) string {
		if v, ok := cfg.Env[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	// v7.8.16 packages/core/src/flag/flag.ts:119 supplies WorkspaceRef.
	if value("KILO_WORKSPACE_ID") != "" {
		return errors.New("kilo imported history requires an implicit local workspace")
	}
	if path := value("KILO_DB"); path != "" && path != "kilo.db" {
		return errors.New("kilo imported history cannot use a custom database filename")
	}
	return nil
}
