package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ResumeWithNormalSettings records an explicit recovery preference without
// replacing the native conversation, workspace, model, or any timeline facts.
// The session revision fences a concurrent lifecycle change. The caller must
// additionally hold its restore/resume operation lock and reject live agents.
func (s *Store) ResumeWithNormalSettings(ctx context.Context, id domain.SessionID, expectedRevision int64) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET speed_mode = 'normal' WHERE id = ? AND revision = ?`, id, expectedRevision)
	if err != nil {
		return false, fmt.Errorf("select normal recovery speed: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET speed_mode = 'normal' WHERE current_session_id = ?`, id); err != nil {
		return false, fmt.Errorf("synchronize recovery conversation speed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
