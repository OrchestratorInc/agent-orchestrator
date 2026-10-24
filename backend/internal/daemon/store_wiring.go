package daemon

import (
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// openStore opens the durable store. A database that a newer AO already
// migrated is refused (sqlite.ErrSchemaNewer) so an older build never runs
// against a schema it does not know. AO_ALLOW_NEWER_SCHEMA turns that refusal
// off for developers who switch branches on a shared data dir; the override is
// announced on every start so it cannot stay on unnoticed.
func openStore(cfg config.Config, log *slog.Logger) (*sqlite.Store, error) {
	if cfg.AllowNewerSchema {
		log.Warn("AO_ALLOW_NEWER_SCHEMA is set: opening the database even if a newer AO migrated it; this build may misread or corrupt the newer schema")
	}
	store, err := sqlite.OpenWithSchemaPolicy(cfg.DataDir, cfg.AllowNewerSchema)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	return store, nil
}
