package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type legacySwitchHandoffStore interface {
	LegacySwitchHandoffPath(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, nativeID string) (string, error)
}

// withLegacySwitchHandoff adds back the hidden handoff of a switch made by the
// agent switching on main, for a worker resuming the native session that
// switch started. Without it, the agent loses what it was handed.
func (m *Manager) withLegacySwitchHandoff(ctx context.Context, rec domain.SessionRecord, systemPrompt string) string {
	store, ok := m.store.(legacySwitchHandoffStore)
	nativeID := strings.TrimSpace(rec.Metadata.AgentSessionID)
	if !ok || rec.Kind != domain.KindWorker || nativeID == "" || m.dataDir == "" {
		return systemPrompt
	}
	path, err := store.LegacySwitchHandoffPath(ctx, rec.ID, rec.Harness, nativeID)
	// The file is AO's own, under its handoffs folder.
	if err != nil || path == "" || !strings.HasPrefix(filepath.Clean(path), filepath.Join(m.dataDir, "handoffs")+string(filepath.Separator)) {
		return systemPrompt
	}
	body, err := os.ReadFile(path) //nolint:gosec // checked to be under AO's handoffs folder
	if err != nil {
		return systemPrompt
	}
	var artifact struct {
		SessionID     domain.SessionID    `json:"sessionId"`
		TargetHarness domain.AgentHarness `json:"targetAgent"`
		Continuation  string              `json:"continuation"`
	}
	if json.Unmarshal(body, &artifact) != nil || artifact.SessionID != rec.ID || artifact.TargetHarness != rec.Harness {
		return systemPrompt
	}
	continuation := strings.TrimSpace(artifact.Continuation)
	if continuation == "" {
		return systemPrompt
	}
	if strings.TrimSpace(systemPrompt) == "" {
		return continuation
	}
	return strings.TrimSpace(systemPrompt) + "\n\n" + continuation
}
