package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// New Crush releases lock the project DB. Older releases at least keep their
// SQLite connection open, which supplies best-effort process evidence.
func crushImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil || ctx.Err() != nil {
		return false
	}
	db := filepath.Dir(rec.Metadata.NativeTranscriptPath)
	if len(source.ResumeCwd) > 0 {
		db = filepath.Join(source.ResumeCwd, ".crush", "crush.db")
	}
	return providerLockHeld(filepath.Join(filepath.Dir(db), "crush.lock")) || writableTranscriptOwner(ctx, db)
}
