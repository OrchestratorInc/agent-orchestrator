package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SessionLaunchOptions returns the launch choices saved for a terminal session.
func (s *Store) SessionLaunchOptions(ctx context.Context, id domain.SessionID) (domain.AgentLaunchOptions, error) {
	raw, err := s.qr.GetSessionLaunchOptions(ctx, id)
	if err != nil || raw == "" {
		return domain.AgentLaunchOptions{}, err
	}
	var options domain.AgentLaunchOptions
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return domain.AgentLaunchOptions{}, fmt.Errorf("decode launch options for %s: %w", id, err)
	}
	return options, nil
}

// SetSessionLaunchOptions saves the launch choices for a terminal session.
func (s *Store) SetSessionLaunchOptions(ctx context.Context, id domain.SessionID, options domain.AgentLaunchOptions) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.SetSessionLaunchOptions(ctx, gen.SetSessionLaunchOptionsParams{LaunchOptions: encodeLaunchOptions(options), ID: id})
}

// UpdateSessionWithLaunchOptions writes a session row and its launch choices in
// one transaction: a terminal switch moves both to the other agent, and a stop
// between two writes would start one agent with the other's choices.
func (s *Store) UpdateSessionWithLaunchOptions(ctx context.Context, rec domain.SessionRecord, options domain.AgentLaunchOptions) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "update session with launch options", func(q *gen.Queries) error {
		if err := q.UpdateSession(ctx, recordToUpdate(rec)); err != nil {
			return err
		}
		return q.SetSessionLaunchOptions(ctx, gen.SetSessionLaunchOptionsParams{LaunchOptions: encodeLaunchOptions(options), ID: rec.ID})
	})
}

func encodeLaunchOptions(options domain.AgentLaunchOptions) string {
	if options == (domain.AgentLaunchOptions{}) {
		return ""
	}
	encoded, _ := json.Marshal(options) // plain fields: cannot fail
	return string(encoded)
}
