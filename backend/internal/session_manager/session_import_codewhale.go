package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/google/uuid"
)

func codewhaleNativeOwnedElsewhere(ctx context.Context, root, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		return false
	}
	dir := filepath.Join(root, "sessions")
	for _, lock := range []string{filepath.Join(dir, id+".lock"), filepath.Join(dir, ".late-usage", id+".lock"), filepath.Join(dir, ".late-usage", id+".live")} {
		if providerLockHeld(lock) {
			return true
		}
	}
	return writableTranscriptOwner(ctx, filepath.Join(dir, id+".json"))
}

func codewhaleImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	return source != nil && codewhaleNativeOwnedElsewhere(ctx, source.ConfigDir, source.NativeID)
}

func codewhaleAdoptedImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	return source != nil && codewhaleNativeOwnedElsewhere(ctx, source.ConfigDir, rec.Metadata.ProviderConversationID)
}
