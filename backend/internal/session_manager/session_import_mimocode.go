package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Bind the database itself, without changing MiMo's credential/config home.
func mimoCodeImportDatabase(root string) string {
	return filepath.Join(root, "mimocode.db")
}

// The shared SQLite file cannot identify which conversation a process owns.
// With no verified per-session PID signal, do not infer ownership from it.
func mimoCodeImportOwnedElsewhere(context.Context, domain.SessionRecord) bool {
	return false
}
