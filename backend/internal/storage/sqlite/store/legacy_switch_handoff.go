package store

import (
	"context"
	"fmt"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/timestamps"
)

// LegacySwitchHandoffPath is the finalized handoff file the agent switching on
// main wrote for the switch that brought harness into the session on nativeID,
// or "" when there is none. That handoff rode the agent's hidden system prompt,
// so a native restore must carry it again.
func (s *Store) LegacySwitchHandoffPath(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, nativeID string) (string, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT CAST(sw.requested_at AS TEXT), sw.final_handoff_path, sw.id FROM agent_switches sw
JOIN agent_native_sessions n ON n.id = sw.target_native_session_ref
WHERE sw.session_id = ? AND sw.target_harness = ? AND sw.final_handoff_path <> ''
  AND sw.state IN ('target_ready', 'delivering_context', 'completed', 'failed')
  AND n.ao_session_id = sw.session_id AND n.harness = sw.target_harness AND n.native_session_id = ?
ORDER BY sw.requested_at DESC, sw.id DESC`, string(sessionID), string(harness), nativeID)
	if err != nil {
		return "", fmt.Errorf("legacy switch handoff for %s: %w", sessionID, err)
	}
	defer func() { _ = rows.Close() }()
	var fallback, path, latestID string
	var latest time.Time
	found := false
	for rows.Next() {
		var requested, candidatePath, id string
		if err := rows.Scan(&requested, &candidatePath, &id); err != nil {
			return "", fmt.Errorf("legacy switch handoff for %s: %w", sessionID, err)
		}
		if fallback == "" {
			fallback = candidatePath
		}
		at, ok := timestamps.ParseLegacy(requested)
		if ok && (!found || at.After(latest) || (at.Equal(latest) && id > latestID)) {
			path, latestID, latest, found = candidatePath, id, at, true
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("legacy switch handoff for %s: %w", sessionID, err)
	}
	// Prefer a known instant while background repair is pending. If none can
	// be parsed, keep the old lexical choice rather than losing the handoff.
	if !found {
		return fallback, nil
	}
	return path, nil
}
