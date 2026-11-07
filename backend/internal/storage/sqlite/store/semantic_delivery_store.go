package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// RegisterSemanticDelivery reserves a report delivery for the session's
// current live runtime launch. Repeating the same reservation is idempotent.
func (s *Store) RegisterSemanticDelivery(ctx context.Context, id domain.SessionID, runtimeLaunchID, deliveryID string) (bool, error) {
	if runtimeLaunchID == "" || deliveryID == "" {
		return false, fmt.Errorf("semantic delivery launch and id are required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RegisterSemanticDelivery(ctx, gen.RegisterSemanticDeliveryParams{
		SessionID:       id,
		RuntimeLaunchID: runtimeLaunchID,
		DeliveryID:      deliveryID,
	})
	if err != nil {
		return false, fmt.Errorf("register semantic delivery %s for session %s: %w", deliveryID, id, err)
	}
	return rows > 0, nil
}

// SemanticDeliveryAccepted checks the durable receipt for one exact
// session/launch/delivery tuple.
func (s *Store) SemanticDeliveryAccepted(ctx context.Context, id domain.SessionID, runtimeLaunchID, deliveryID string) (bool, error) {
	if runtimeLaunchID == "" || deliveryID == "" {
		return false, nil
	}
	accepted, err := s.qr.SemanticDeliveryAccepted(ctx, gen.SemanticDeliveryAcceptedParams{
		SessionID:       id,
		RuntimeLaunchID: runtimeLaunchID,
		DeliveryID:      deliveryID,
	})
	if err != nil {
		return false, fmt.Errorf("read semantic delivery %s for session %s: %w", deliveryID, id, err)
	}
	return accepted, nil
}
