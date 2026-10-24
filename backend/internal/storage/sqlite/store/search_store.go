package store

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SearchMessagesAfter returns chat messages changed after the (updatedAt, id)
// cursor, oldest first, for the search indexer to catch up on.
func (s *Store) SearchMessagesAfter(ctx context.Context, updatedAt time.Time, id string, limit int) ([]domain.SearchMessageChange, error) {
	rows, err := s.qr.ListConversationMessagesForSearchAfter(ctx, gen.ListConversationMessagesForSearchAfterParams{
		AfterUpdatedAt: updatedAt,
		AfterID:        id,
		PageSize:       int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.SearchMessageChange, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.SearchMessageChange{ID: row.ID, Text: row.Text, Streaming: row.Streaming != 0, UpdatedAt: row.UpdatedAt})
	}
	return out, nil
}

// SearchMessageIDsAfter pages through every chat message id in id order, so
// the search index can drop messages the store no longer holds.
func (s *Store) SearchMessageIDsAfter(ctx context.Context, id string, limit int) ([]string, error) {
	return s.qr.ListConversationMessageIDsAfter(ctx, gen.ListConversationMessageIDsAfterParams{AfterID: id, PageSize: int64(limit)})
}

// SearchMessagesByID re-reads matched chat messages with their conversation
// facts. Ids the store no longer holds are simply absent from the result.
func (s *Store) SearchMessagesByID(ctx context.Context, ids []string) ([]domain.SearchMessage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.qr.ListConversationMessagesForSearchByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SearchMessage, 0, len(rows))
	for _, row := range rows {
		msg := domain.SearchMessage{
			ID:                row.ID,
			ConversationID:    row.ConversationID,
			Text:              row.Text,
			Streaming:         row.Streaming != 0,
			CreatedAt:         row.CreatedAt,
			ConversationTitle: row.AppliedTitle,
		}
		if msg.ConversationTitle == "" {
			msg.ConversationTitle = row.ProviderTitle
		}
		switch {
		case row.SessionID != nil:
			msg.SessionID = *row.SessionID
		case row.CurrentSessionID != nil:
			msg.SessionID = *row.CurrentSessionID
		case row.ReviewSessionID != "":
			msg.SessionID = row.ReviewSessionID
		}
		msg.ReviewID = row.ReviewID.String
		if row.ProjectID != nil {
			msg.ProjectID = *row.ProjectID
		}
		out = append(out, msg)
	}
	return out, nil
}

// SearchVisibleMessageIDs reports which of ids are visible in their
// conversation's transcript: on the active branch path and not part of a
// rolled-back, promoted or cancelled turn (the SelectConversationMessages rule).
func (s *Store) SearchVisibleMessageIDs(ctx context.Context, conversationID string, ids []string) (map[string]bool, error) {
	visible := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return visible, nil
	}
	rows, err := s.qr.ListVisibleConversationMessageIDs(ctx, gen.ListVisibleConversationMessageIDsParams{ConversationID: conversationID, Ids: ids})
	if err != nil {
		return nil, err
	}
	for _, id := range rows {
		visible[id] = true
	}
	return visible, nil
}
