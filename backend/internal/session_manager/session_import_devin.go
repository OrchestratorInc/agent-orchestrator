package sessionmanager

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Devin holds an OS writer lock, not merely a persistent lock-file marker.
func devinImportOwnedElsewhere(_ context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil {
		return false
	}
	id := source.NativeID
	if rec.Metadata.ProviderConversationID != "" {
		id = rec.Metadata.ProviderConversationID
	}
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return false
	}
	return providerLockHeld(filepath.Join(source.ConfigDir, "session_locks", id+".lock"))
}

func devinImportDataHome(root string) string { return filepath.Dir(filepath.Dir(root)) }
