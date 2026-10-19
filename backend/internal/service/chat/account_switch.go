package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AccountSwitchedEvent marks, on a turn an account move stopped, the account
// the conversation continues on.
const AccountSwitchedEvent = "account.switched"

// MarkAccountSwitch records that the turn the session's account move stopped
// ended for the move, rather than failing. It is the exact turn the handoff's
// interrupt stopped: later rows (a swept queued message, a Codex subagent's own
// turn) are not it. Stopping the old agent races its reply to the cancel, so
// the turn may have settled as failed; it settles as interrupted, and a notice
// names the account the next message continues on. A turn whose completion won
// the race is left alone, and so is a move that stopped no turn.
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
