package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// InterfaceBoundaryEvent marks, in a session's Chat timeline, where a degraded
// interface handoff left native history behind.
const InterfaceBoundaryEvent = "context.boundary"

// RecordInterfaceTransitionBoundary adds a degraded handoff's boundary notice
// to the session's Chat timeline as a system row. It is never sent to the
// agent: a prompt would cost a model turn and put AO's bookkeeping into the
// provider's own history. The row is keyed by the transition's boundary
// message id, so a redelivery from the outbox settles the same row. A session
// that has no Chat conversation has no timeline to mark.
func (s *Service) RecordInterfaceTransitionBoundary(
	ctx context.Context,
	id domain.SessionID,
	transitionID, summary string,
) error {
	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return nil
	}
	if err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]string{
		"event":  InterfaceBoundaryEvent,
		"reason": "interface_handoff_identity_reset",
	})
	if err != nil {
		return fmt.Errorf("encode interface boundary: %w", err)
	}
	return s.store.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
		ID:             s.newID(),
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        summary,
		Detail:         detail,
		ProviderItemID: domain.SessionInterfaceTransitionBoundaryMessageID(transitionID),
	}, s.now())
}
