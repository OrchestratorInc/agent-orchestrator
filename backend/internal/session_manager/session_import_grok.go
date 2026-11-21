package sessionmanager

import (
	"context"
	"encoding/json"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// Registry evidence is best effort: old Grok releases may omit the registry,
// and PID reuse is not authoritative proof of the original process identity.
func grokImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil || ctx.Err() != nil {
		return false
	}
	r, err := os.OpenRoot(source.ConfigDir)
	if err != nil {
		return false
	}
	defer func() { _ = r.Close() }()
	data, err := r.ReadFile("active_sessions.json")
	if err != nil {
		return false
	}
	var entries []struct {
		SessionID string `json:"session_id"`
		PID       int    `json:"pid"`
	}
	if json.Unmarshal(data, &entries) != nil {
		return false
	}
	id := source.NativeID
	if rec.Metadata.ProviderConversationID != "" {
		id = rec.Metadata.ProviderConversationID
	}
	for _, entry := range entries {
		if entry.SessionID == id && entry.PID > 0 && processalive.Alive(entry.PID) {
			return true
		}
	}
	return false
}
