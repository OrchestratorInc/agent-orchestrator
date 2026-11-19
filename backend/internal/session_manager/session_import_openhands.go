package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// This supplementary probe detects a state descriptor held open. It cannot
// detect an idle original CLI; the S2 preparer isolates the selected history
// into an AO-owned native store before launch to avoid sharing that writer.
func openHandsImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
}
