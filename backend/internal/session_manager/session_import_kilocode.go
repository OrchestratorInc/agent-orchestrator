package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func kiloCodeImportDataHome(root string) string {
	return filepath.Dir(root) // Kilo appends "kilo" to XDG_DATA_HOME.
}

// A process holding the shared kilo.db is not evidence of ownership of a
// particular session. The provider exposes no verified per-session PID signal.
func kiloCodeImportOwnedElsewhere(context.Context, domain.SessionRecord) bool {
	return false
}
