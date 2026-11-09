package chat

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// withImportedHistory continues an adopted import's timeline below its first
// AO turn with the archive read from the provider transcript. The provider's own
// replay is partial (Claude resumes from its latest compaction), so the store
// hides it; the archive owns the sequences before it.
func (s *Service) withImportedHistory(ctx context.Context, id domain.SessionID, replayedThrough int64, rows ConversationRows, before, limit int64) (ConversationRows, error) {
	if limit <= 0 {
		limit = 200 // the store's default page (DefaultConversationPageSize)
	}
	room := limit - int64(len(rows.Messages)+len(rows.Activities))
	if room <= 0 {
		rows.HasMoreBefore = true // the next page starts in the archive
		return rows, nil
	}
	if before <= 0 || before > replayedThrough+1 {
		before = replayedThrough + 1
	}
	messages, more, err := s.store.LoadSessionImportMessages(ctx, id, before, room)
	if err != nil || len(messages) == 0 {
		return rows, err
	}
	rows.Messages = append(messages, rows.Messages...)
	rows.OldestSequence, rows.HasMoreBefore = messages[0].Sequence, more
	return rows, nil
}

func (s *Service) importedSnapshot(ctx context.Context, rec domain.SessionRecord, before, limit int64) (Snapshot, error) {
	messages, more, err := s.store.LoadSessionImportMessages(ctx, rec.ID, before, limit)
	if err != nil {
		return Snapshot{}, err
	}
	oldest := int64(0)
	if len(messages) > 0 {
		oldest = messages[0].Sequence
	}
	return Snapshot{SessionID: rec.ID, Harness: rec.Harness, Mode: domain.NormalizeSessionMode(rec.Mode), Controller: ports.ChatControllerStopped, Conversation: domain.ConversationRecord{ID: "import:" + string(rec.ID), SessionID: rec.ID, ProjectID: rec.ProjectID, Scope: domain.ConversationScopeSession}, Messages: messages, HasMoreBefore: more, OldestSequence: oldest}, nil
}

// requireReadableConversation permits only a pending import's archive outside
// Chat mode. Every Chat mutation still uses requireChatSession, so displaying a
// Terminal import never starts a Chat controller or accepts a Chat prompt.
func (s *Service) requireReadableConversation(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("read session %s: %w", id, err)
	}
	if !found {
		return domain.SessionRecord{}, ports.ErrSessionNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat && !rec.NeedsImportResume() {
		return domain.SessionRecord{}, ErrNotChatMode
	}
	return rec, nil
}
