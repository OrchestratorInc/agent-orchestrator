package sessionmanager

import (
	"context"
	"path/filepath"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var vibeImportID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func vibeImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	if ctx.Err() != nil || rec.Metadata.ImportSource == nil {
		return false
	}
	source := rec.Metadata.ImportSource
	id := source.NativeID
	if rec.Metadata.ProviderConversationID != "" {
		id = rec.Metadata.ProviderConversationID
	}
	if !vibeImportID.MatchString(id) {
		return false
	}
	// SessionLease uses an OS lock; its JSON diagnostic may survive release or
	// a crash, and a recycled diagnostic PID is not evidence of ownership.
	if providerLockHeld(filepath.Join(source.ConfigDir, "logs", "session", "active", id+".lock")) {
		return true
	}
	// Older Vibe releases do not publish leases. An open transcript is positive
	// evidence, but an append-open-close writer can escape this best-effort probe.
	return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
}
