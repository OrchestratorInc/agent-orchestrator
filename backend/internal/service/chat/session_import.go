package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (s *Service) importedSnapshot(ctx context.Context, rec domain.SessionRecord, before, limit int64) (Snapshot, error) {
	reader, ok := s.store.(interface {
		LoadSessionImportMessages(context.Context, domain.SessionID, int64, int64) ([]domain.ConversationMessage, bool, error)
	})
	if !ok {
		return Snapshot{}, errors.New("imported history reader is unavailable")
	}
	messages, more, err := reader.LoadSessionImportMessages(ctx, rec.ID, before, limit)
	if err != nil {
		return Snapshot{}, err
	}
	oldest := int64(0)
	if len(messages) > 0 {
		oldest = messages[0].Sequence
	}
	return Snapshot{SessionID: rec.ID, Harness: rec.Harness, Mode: domain.SessionModeChat, Controller: ports.ChatControllerStopped, Conversation: domain.ConversationRecord{ID: "import:" + string(rec.ID), SessionID: rec.ID, ProjectID: rec.ProjectID, Scope: domain.ConversationScopeSession}, Messages: messages, HasMoreBefore: more, OldestSequence: oldest}, nil
}
