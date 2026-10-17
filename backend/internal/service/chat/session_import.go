package chat

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type importArchiveReader interface {
	LoadSessionImportMessages(context.Context, domain.SessionID, int64, int64) ([]domain.ConversationMessage, bool, error)
}

// withImportedHistory continues an adopted import's timeline below its first
// AO turn with the archive read from the provider transcript. The provider's own
// replay is partial (Claude resumes from its latest compaction), so the store
// hides it; the archive owns the sequences before it.
func (s *Service) withImportedHistory(ctx context.Context, id domain.SessionID, replayedThrough int64, rows ConversationRows, before, limit int64) (ConversationRows, error) {
	reader, ok := s.store.(importArchiveReader)
	if !ok {
		return rows, nil
	}
	if limit <= 0 {
		limit = 200 // the store's default page
	}
	room := limit - int64(len(rows.Messages)+len(rows.Activities))
	if room <= 0 {
		rows.HasMoreBefore = true // the next page starts in the archive
		return rows, nil
	}
	if before <= 0 || before > replayedThrough+1 {
		before = replayedThrough + 1
	}
	messages, more, err := reader.LoadSessionImportMessages(ctx, id, before, room)
	if err != nil || len(messages) == 0 {
		return rows, err
	}
	rows.Messages = append(messages, rows.Messages...)
	rows.OldestSequence, rows.HasMoreBefore = messages[0].Sequence, more
	return rows, nil
}

func (s *Service) importedSnapshot(ctx context.Context, rec domain.SessionRecord, before, limit int64) (Snapshot, error) {
	reader, ok := s.store.(importArchiveReader)
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
