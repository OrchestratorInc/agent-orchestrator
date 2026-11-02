package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SessionAccountPinned reports the session's "Stay on this account" choice.
func (s *Store) SessionAccountPinned(ctx context.Context, id domain.SessionID) (bool, error) {
	n, err := s.qr.GetSessionAccountPin(ctx, string(id))
	if err != nil {
		return false, fmt.Errorf("read session account pin %s: %w", id, err)
	}
	return n > 0, nil
}

// SetSessionAccountPinned records or clears "Stay on this account".
func (s *Store) SetSessionAccountPinned(ctx context.Context, id domain.SessionID, pinned bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var err error
	if pinned {
		err = s.qw.PinSessionAccount(ctx, gen.PinSessionAccountParams{SessionID: string(id), PinnedAt: now})
	} else {
		err = s.qw.UnpinSessionAccount(ctx, string(id))
	}
	if err != nil {
		return fmt.Errorf("set session account pin %s: %w", id, err)
	}
	return nil
}

// AccountHandoffCandidateTurns lists chat turns that failed since the time and
// have neither a retry nor a handoff yet.
func (s *Store) AccountHandoffCandidateTurns(ctx context.Context, since time.Time) ([]domain.ConversationTurn, error) {
	rows, err := s.qr.SelectAccountHandoffCandidateTurns(ctx, timeToNullTime(since))
	if err != nil {
		return nil, fmt.Errorf("list account handoff candidates: %w", err)
	}
	turns := make([]domain.ConversationTurn, 0, len(rows))
	for _, row := range rows {
		turns = append(turns, turnToDomain(row))
	}
	return turns, nil
}

// ClaimChatAccountHandoff inserts the handoff in the selecting phase. Only the
// first claim for a turn returns true.
func (s *Store) ClaimChatAccountHandoff(ctx context.Context, rec domain.ChatAccountHandoff) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.InsertChatAccountHandoff(ctx, gen.InsertChatAccountHandoffParams{
		SourceTurnID: rec.SourceTurnID, SessionID: string(rec.SessionID), ConversationID: rec.ConversationID,
		SourceAccountID: rec.SourceAccountID, SourceResetsAt: ptrTimeToNullTime(rec.SourceResetsAt), CreatedAt: rec.CreatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("claim account handoff %s: %w", rec.SourceTurnID, err)
	}
	return n == 1, nil
}

// ChatAccountHandoff reads one turn's handoff.
func (s *Store) ChatAccountHandoff(ctx context.Context, sourceTurnID string) (domain.ChatAccountHandoff, bool, error) {
	row, err := s.qr.GetChatAccountHandoff(ctx, sourceTurnID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatAccountHandoff{}, false, nil
	}
	if err != nil {
		return domain.ChatAccountHandoff{}, false, fmt.Errorf("read account handoff %s: %w", sourceTurnID, err)
	}
	return chatAccountHandoffFromGen(row), true, nil
}

// ActiveChatAccountHandoffs lists handoffs a restart or a later tick resumes.
func (s *Store) ActiveChatAccountHandoffs(ctx context.Context) ([]domain.ChatAccountHandoff, error) {
	rows, err := s.qr.ListActiveChatAccountHandoffs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active account handoffs: %w", err)
	}
	out := make([]domain.ChatAccountHandoff, 0, len(rows))
	for _, row := range rows {
		out = append(out, chatAccountHandoffFromGen(row))
	}
	return out, nil
}

// AdvanceChatAccountHandoff moves rec to rec.Phase only from expected.
func (s *Store) AdvanceChatAccountHandoff(ctx context.Context, rec domain.ChatAccountHandoff, expected domain.ChatAccountHandoffPhase) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.AdvanceChatAccountHandoff(ctx, gen.AdvanceChatAccountHandoffParams{
		NextPhase: string(rec.Phase), TargetAccountID: rec.TargetAccountID, TransitionID: rec.TransitionID,
		Detail: rec.Detail, UpdatedAt: rec.UpdatedAt, SourceTurnID: rec.SourceTurnID, ExpectedPhase: string(expected),
	})
	if err != nil {
		return false, fmt.Errorf("advance account handoff %s: %w", rec.SourceTurnID, err)
	}
	return n == 1, nil
}

// RecentlyLeftAccounts lists accounts the session moved off for a usage limit
// that has not reset yet, or, with no known reset, since cooldownSince.
func (s *Store) RecentlyLeftAccounts(ctx context.Context, id domain.SessionID, now, cooldownSince time.Time) ([]string, error) {
	ids, err := s.qr.ListRecentlyLeftAccounts(ctx, gen.ListRecentlyLeftAccountsParams{
		SessionID: string(id), Now: timeToNullTime(now), CooldownSince: cooldownSince,
	})
	if err != nil {
		return nil, fmt.Errorf("list recently left accounts %s: %w", id, err)
	}
	return ids, nil
}

func chatAccountHandoffFromGen(row gen.ChatAccountHandoff) domain.ChatAccountHandoff {
	return domain.ChatAccountHandoff{
		SourceTurnID: row.SourceTurnID, SessionID: domain.SessionID(row.SessionID), ConversationID: row.ConversationID,
		SourceAccountID: row.SourceAccountID, TargetAccountID: row.TargetAccountID, TransitionID: row.TransitionID,
		Phase: domain.ChatAccountHandoffPhase(row.Phase), Detail: row.Detail, SourceResetsAt: nullTimePtr(row.SourceResetsAt),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
