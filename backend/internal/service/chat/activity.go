package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ActivityReader reads one full activity within a conversation lineage.
type ActivityReader interface {
	LoadConversationActivity(context.Context, string, string) (domain.ConversationActivity, error)
}

// Activity reads a full activity within the session's active conversation lineage.
func (s *Service) Activity(ctx context.Context, id domain.SessionID, activityID string) (domain.ConversationActivity, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return domain.ConversationActivity{}, err
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return domain.ConversationActivity{}, domain.ErrConversationActivityNotFound
	}
	if err != nil {
		return domain.ConversationActivity{}, err
	}
	if s.activityReader != nil {
		return s.activityReader.LoadConversationActivity(ctx, conversation.ID, activityID)
	}
	// Focused test stores may only implement the snapshot reader.
	snapshot, err := s.reader.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		return domain.ConversationActivity{}, err
	}
	for _, row := range snapshot.Activities {
		if row.ID == activityID {
			return row, nil
		}
	}
	return domain.ConversationActivity{}, domain.ErrConversationActivityNotFound
}
