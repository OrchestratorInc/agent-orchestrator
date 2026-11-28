package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// FX holds an advisory writer lock for its open session: v1 session.lock,
// v2 lock. Merely finding an old lock file is not evidence of a live writer.
// Windows cannot probe flock; the provider still enforces its own writer lock.
func fxImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	if ctx.Err() != nil || rec.Metadata.ImportSource == nil {
		return false
	}
	source := rec.Metadata.ImportSource
	for _, path := range []string{
		filepath.Join(source.ConfigDir, "sessions", "v2", source.NativeID, "lock"),
		filepath.Join(source.ConfigDir, "sessions", source.NativeID, "session.lock"),
	} {
		if providerLockHeld(path) {
			return true
		}
	}
	return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
}
