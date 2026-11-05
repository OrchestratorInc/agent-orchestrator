package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Before adoption Cursor uses the default open-elsewhere check on its
// store.db; afterwards the writer holds the prepared copy instead.
func cursorAdoptedImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil || source.ResumeCwd == "" {
		return false
	}
	// The prepared copy is in the terminal or ACP layout, by the session's mode.
	if source.ResumeTranscriptPath != "" {
		return writableTranscriptOwner(ctx, source.ResumeTranscriptPath)
	}
	path, err := cursorstore.SessionPath(source.ConfigDir, source.ResumeCwd, rec.Metadata.ProviderConversationID)
	return err == nil && writableTranscriptOwner(ctx, path)
}
