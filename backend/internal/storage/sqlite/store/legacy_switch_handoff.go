package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LegacySwitchHandoffPath is the finalized handoff file the agent switching on
// main wrote for the switch that brought harness into the session on nativeID,
// or "" when there is none. That handoff rode the agent's hidden system prompt,
// so a native restore must carry it again.
func (s *Store) LegacySwitchHandoffPath(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, nativeID string) (string, error) {
	var path string
	err := s.readDB.QueryRowContext(ctx, `
SELECT sw.final_handoff_path FROM agent_switches sw
JOIN agent_native_sessions n ON n.id = sw.target_native_session_ref
WHERE sw.session_id = ? AND sw.target_harness = ? AND sw.final_handoff_path <> ''
  AND sw.state IN ('target_ready', 'delivering_context', 'completed', 'failed')
  AND n.ao_session_id = sw.session_id AND n.harness = sw.target_harness AND n.native_session_id = ?
ORDER BY sw.requested_at DESC LIMIT 1`, string(sessionID), string(harness), nativeID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("legacy switch handoff for %s: %w", sessionID, err)
	}
	return path, nil
}
