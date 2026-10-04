package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func decodeStartupCue(raw string) *domain.StartupCueRun {
	if raw == "" {
		return nil
	}
	var run domain.StartupCueRun
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		return nil
	}
	return &run
}

// ClaimStartupCue installs an immutable definition once per session worktree.
func (s *Store) ClaimStartupCue(ctx context.Context, id domain.SessionID, run domain.StartupCueRun) (bool, error) {
	raw, err := json.Marshal(run)
	if err != nil {
		return false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.ClaimStartupCue(ctx, gen.ClaimStartupCueParams{ID: id, StartupCueJson: string(raw), UpdatedAt: time.Now().UTC()})
	return n > 0, err
}

// BeginStartupCue admits exactly one worker to execute the pinned command.
func (s *Store) BeginStartupCue(ctx context.Context, id domain.SessionID, run domain.StartupCueRun) (bool, error) {
	raw, err := json.Marshal(run)
	if err != nil {
		return false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.BeginStartupCue(ctx, gen.BeginStartupCueParams{ID: id, StartupCueJson: string(raw), UpdatedAt: time.Now().UTC()})
	return n > 0, err
}

// FinishStartupCue records the result. Terminal outcomes cannot be overwritten.
func (s *Store) FinishStartupCue(ctx context.Context, id domain.SessionID, run domain.StartupCueRun) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.qw.FinishStartupCue(ctx, gen.FinishStartupCueParams{ID: id, StartupCueJson: string(raw), UpdatedAt: time.Now().UTC()})
	return err
}

// EnqueueStartupCueMessage persists a TUI message only while startup holds delivery.
func (s *Store) EnqueueStartupCueMessage(ctx context.Context, id domain.SessionID, key, message string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.EnqueueStartupCueMessage(ctx, gen.EnqueueStartupCueMessageParams{SessionID: string(id), ClientMessageID: key, Message: message})
	if err != nil || n > 0 {
		return n > 0, err
	}
	row, err := s.qw.GetSession(ctx, id)
	if err != nil {
		return false, err
	}
	return decodeStartupCue(row.StartupCueJson).HoldsInput(), nil
}

// ListStartupCueMessages returns undelivered messages in arrival order.
func (s *Store) ListStartupCueMessages(ctx context.Context, id domain.SessionID) ([]domain.StartupCueMessage, error) {
	rows, err := s.qr.ListStartupCueMessages(ctx, string(id))
	if err != nil {
		return nil, err
	}
	out := make([]domain.StartupCueMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.StartupCueMessage{ID: row.ID, Message: row.Message, ClientMessageID: row.ClientMessageID})
	}
	return out, nil
}

// MarkStartupCueMessageDelivered acknowledges a successfully sent message.
func (s *Store) MarkStartupCueMessageDelivered(ctx context.Context, id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.MarkStartupCueMessageDelivered(ctx, id)
}
