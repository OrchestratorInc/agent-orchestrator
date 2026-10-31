package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LoadConversationActivity reads one full activity in the active branch lineage.
func (s *Store) LoadConversationActivity(ctx context.Context, conversationID, activityID string) (domain.ConversationActivity, error) {
	row, err := s.qr.SelectConversationActivityByID(ctx, gen.SelectConversationActivityByIDParams{ConversationID: conversationID, ActivityID: activityID})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationActivity{}, domain.ErrConversationActivityNotFound
	}
	if err != nil {
		return domain.ConversationActivity{}, err
	}
	conversation, err := s.qr.SelectConversationByID(ctx, conversationID)
	if err != nil {
		return domain.ConversationActivity{}, err
	}
	visibleAfter, err := s.conversationVisibleAfterSequence(ctx, conversation)
	if err != nil {
		return domain.ConversationActivity{}, err
	}
	if row.Sequence <= visibleAfter {
		return domain.ConversationActivity{}, domain.ErrConversationActivityNotFound
	}
	return activityToDomain(row), nil
}
