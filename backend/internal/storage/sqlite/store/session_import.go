package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateImportedSession registers identity and its archive in one transaction. A retry returns the original row.
func (s *Store) CreateImportedSession(ctx context.Context, rec domain.SessionRecord, messages []sessionimport.Message) (domain.SessionRecord, bool, error) {
	source := rec.Metadata.ImportSource
	if source == nil || source.NativeID == "" || source.ConfigDir == "" || len(messages) == 0 {
		return rec, false, errors.New("readable native history is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var created domain.SessionRecord
	fresh := false
	err := s.inTx(ctx, "import session", func(q *gen.Queries) error {
		id, err := q.FindImportedSession(ctx, gen.FindImportedSessionParams{ConfigDir: source.ConfigDir, NativeID: source.NativeID})
		if err == nil {
			row, err := q.GetSession(ctx, id)
			if err != nil {
				return err
			}
			created = rowToRecord(row)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		created, fresh, err = s.createSessionLocked(ctx, q, rec)
		if err != nil {
			return err
		}
		for i, msg := range messages {
			if err := q.InsertSessionImportMessage(ctx, gen.InsertSessionImportMessageParams{SessionID: string(created.ID), Sequence: int64(i + 1), Role: string(msg.Role), Text: msg.Text, CreatedAt: msg.At}); err != nil {
				return err
			}
		}
		return nil
	})
	return created, fresh, err
}

// ReplaceImportedMessages swaps in a fresher read of the source until the
// session's conversation holds a row of its own; from then on live rows follow
// the archive. A first open that failed leaves only a conversation whose
// counter still ends at the archive, and that counter moves with the refresh.
func (s *Store) ReplaceImportedMessages(ctx context.Context, id domain.SessionID, messages []sessionimport.Message) error {
	if len(messages) == 0 {
		return errors.New("readable native history is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "refresh import archive", func(q *gen.Queries) error {
		conversation, err := q.SelectConversationBySession(ctx, &id)
		opened := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if opened {
			archived, err := q.MaxSessionImportSequence(ctx, string(id))
			if err != nil {
				return err
			}
			if conversation.LatestSequence > archived {
				return nil
			}
		}
		if err := q.DeleteSessionImportMessages(ctx, string(id)); err != nil {
			return err
		}
		for i, msg := range messages {
			if err := q.InsertSessionImportMessage(ctx, gen.InsertSessionImportMessageParams{SessionID: string(id), Sequence: int64(i + 1), Role: string(msg.Role), Text: msg.Text, CreatedAt: msg.At}); err != nil {
				return err
			}
		}
		if !opened {
			return nil
		}
		_, err = q.SeedImportedConversationSequence(ctx, gen.SeedImportedConversationSequenceParams{SessionID: string(id), ID: conversation.ID})
		return err
	})
}

// LoadSessionImportMessages pages the archive. It remains after native adoption.
func (s *Store) LoadSessionImportMessages(ctx context.Context, id domain.SessionID, before, limit int64) ([]domain.ConversationMessage, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if before == 0 {
		before = math.MaxInt64
	}
	rows, err := s.qr.ListSessionImportMessages(ctx, gen.ListSessionImportMessagesParams{SessionID: string(id), BeforeSequence: before, PageLimit: limit + 1})
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > int(limit)
	if more {
		rows = rows[:limit]
	}
	slices.Reverse(rows)
	messages := make([]domain.ConversationMessage, 0, len(rows))
	for _, row := range rows {
		origin := domain.MessageOriginHuman
		if row.Role == "assistant" {
			origin = domain.MessageOriginProvider
		}
		messages = append(messages, domain.ConversationMessage{ID: fmt.Sprintf("import:%s:%d", id, row.Sequence), ConversationID: "import:" + string(id), Sequence: row.Sequence, Revision: 1, Role: domain.MessageRole(row.Role), Origin: origin, Text: row.Text, CreatedAt: row.CreatedAt, UpdatedAt: row.CreatedAt})
	}
	return messages, more, nil
}

// SetSessionImportWorkspace changes only adoption facts, preserving concurrent
// session edits. It compares decoded values, so rows written before a field was
// added still match.
func (s *Store) SetSessionImportWorkspace(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, branch, path, repo string, now time.Time) (bool, error) {
	if expected == nil || next == nil {
		return false, errors.New("import source is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	updated := false
	err := s.inTx(ctx, "set import workspace", func(q *gen.Queries) error {
		raw, err := q.GetSessionImportSource(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if current := unmarshalImportSource(raw); current == nil || *current != *expected {
			return nil
		}
		rows, err := q.SetSessionImportWorkspace(ctx, gen.SetSessionImportWorkspaceParams{ID: id, NewSource: marshalImportSource(next), Branch: branch, WorkspacePath: path, WorkspaceRepoPath: repo, UpdatedAt: now})
		updated = rows > 0
		return err
	})
	return updated, err
}
