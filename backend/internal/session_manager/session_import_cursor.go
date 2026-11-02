package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func cursorImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	return source != nil && writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
}

func cursorAdoptedImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil || source.ResumeCwd == "" {
		return false
	}
	path, err := cursorstore.SessionPath(source.ConfigDir, source.ResumeCwd, rec.Metadata.ProviderConversationID)
	return err == nil && writableTranscriptOwner(ctx, path)
}
