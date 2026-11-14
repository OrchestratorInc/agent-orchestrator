package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const AccountSwitchedEvent = "account.switched"

type activeTransitionReader interface {
	GetActiveSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
}

func (s *Service) accountMoveAwaitingTarget(ctx context.Context, id domain.SessionID) bool {
	reader, ok := s.store.(activeTransitionReader)
	if !ok {
		return false
	}
	move, active, err := reader.GetActiveSessionInterfaceTransition(ctx, id)
	if err != nil {
		s.log.Warn("chat: read account move", "session", id, "error", err)
		return false
	}
	return active && move.AccountMoveAwaitsTarget()
}

func (s *Service) MarkAccountSwitch(ctx context.Context, id domain.SessionID, accountID string) error {
	s.mu.Lock()
	providerTurnID := s.handoffInterrupted[id]
	delete(s.handoffInterrupted, id)
	s.mu.Unlock()
	if providerTurnID == "" {
		return nil
	}
	reader, ok := s.store.(interface {
		TurnByProviderID(context.Context, string, string) (domain.ConversationTurn, error)
	})
	if !ok {
		return nil
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return nil
	}
	if err != nil {
		return err
	}
	turn, err := reader.TurnByProviderID(ctx, conversation.ID, providerTurnID)
	if errors.Is(err, domain.ErrNoConversationTurn) {
		return nil
	}
	if err != nil {
		return err
	}
	if turn.State != domain.TurnStateFailed && turn.State != domain.TurnStateInterrupted {
		return nil
	}
	now := s.now()
	if err := s.store.SettleTurnByID(ctx, turn.ID, domain.TurnStateInterrupted, "", now); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]string{"event": AccountSwitchedEvent, "accountId": accountID})
	if err != nil {
		return fmt.Errorf("encode account switch: %w", err)
	}
	return s.store.UpsertActivity(ctx, conversation.ID, turn.ProviderTurnID, domain.ConversationActivity{
		ID:             s.newID(),
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "Switched account",
		Detail:         detail,
		ProviderItemID: "account-switch:" + turn.ID,
	}, now)
}
