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
		id, err := q.FindImportedSession(ctx, gen.FindImportedSessionParams{Harness: rec.Harness, ConfigDir: source.ConfigDir, NativeID: source.NativeID})
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

// LoadSessionImportMessages pages the immutable archive. It remains after native adoption.
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

// SetSessionImportWorkspace changes only adoption facts, preserving concurrent session edits.
func (s *Store) SetSessionImportWorkspace(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, branch, path, repo string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionImportWorkspace(ctx, gen.SetSessionImportWorkspaceParams{ID: id, ExpectedSource: marshalImportSource(expected), NewSource: marshalImportSource(next), Branch: branch, WorkspacePath: path, WorkspaceRepoPath: repo, UpdatedAt: now})
	return rows > 0, err
}
