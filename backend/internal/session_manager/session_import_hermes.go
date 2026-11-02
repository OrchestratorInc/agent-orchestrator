package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// The native registry provides positive evidence; missing/old registries cannot
// prove ownership. PID reuse remains a best-effort limitation of this check.
func hermesImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil || ctx.Err() != nil {
		return false
	}
	root, err := os.OpenRoot(source.ConfigDir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(filepath.Join("runtime", "active_sessions.json"))
	if err != nil {
		return false
	}
	var registry struct {
		Entries []struct {
			SessionID string `json:"session_id"`
			PID       int    `json:"pid"`
			Metadata  struct {
				LiveSessionID string `json:"live_session_id"`
			} `json:"metadata"`
		} `json:"entries"`
	}
	if json.Unmarshal(data, &registry) != nil {
		return false
	}
	id := source.NativeID
	if len(rec.Metadata.ProviderConversationID) > 0 {
		id = rec.Metadata.ProviderConversationID
	}
	for _, entry := range registry.Entries {
		if (entry.SessionID == id || entry.Metadata.LiveSessionID == id) && entry.PID > 0 && processalive.Alive(entry.PID) {
			return true
		}
	}
	return false
}

func importedNativeSessionMarker(meta domain.SessionMetadata) string {
	if meta.ImportSource != nil && meta.ImportSource.NativeID == meta.AgentSessionID {
		return "true"
	}
	return ""
}
