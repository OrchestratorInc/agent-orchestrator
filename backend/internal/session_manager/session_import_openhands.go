package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// OpenHands has no per-conversation writer registry. Its event files are
// append-open-close, so this only detects writers holding the state file open.
func openHandsImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
}
