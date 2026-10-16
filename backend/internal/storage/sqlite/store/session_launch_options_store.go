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
	raw := ""
	if options != (domain.AgentLaunchOptions{}) {
		encoded, _ := json.Marshal(options) // plain fields: cannot fail
		raw = string(encoded)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.SetSessionLaunchOptions(ctx, gen.SetSessionLaunchOptionsParams{LaunchOptions: raw, ID: id})
}
