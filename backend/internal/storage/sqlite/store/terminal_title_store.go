package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// UpsertSessionTerminalTitle stores the title a session's terminal program last
// set. An unchanged title writes nothing and emits no change event.
func (s *Store) UpsertSessionTerminalTitle(ctx context.Context, id domain.SessionID, title string, updatedAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertSessionTerminalTitle(ctx, gen.UpsertSessionTerminalTitleParams{SessionID: string(id), Title: title, UpdatedAt: updatedAt}); err != nil {
		return fmt.Errorf("upsert terminal title for session %s: %w", id, err)
	}
	return nil
}

// ListSessionTerminalTitles returns the stored titles of the given sessions.
func (s *Store) ListSessionTerminalTitles(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]string, error) {
	out := make(map[domain.SessionID]string)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListSessionTerminalTitles(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list session terminal titles: %w", err)
	}
	for _, row := range rows {
		out[domain.SessionID(row.SessionID)] = row.Title
	}
	return out, nil
}

// UpdateShellTerminalAppTitle stores the title a shell's foreground program last
// set. It leaves the shell's own name alone.
func (s *Store) UpdateShellTerminalAppTitle(ctx context.Context, handleID, title string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.UpdateShellTerminalAppTitle(ctx, gen.UpdateShellTerminalAppTitleParams{AppTitle: title, HandleID: handleID}); err != nil {
		return fmt.Errorf("update app title for shell terminal %s: %w", handleID, err)
	}
	return nil
}
