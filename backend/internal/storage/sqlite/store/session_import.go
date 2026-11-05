package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateImportedSession registers identity and its archive in one transaction. A retry returns the original row.
func (s *Store) CreateImportedSession(ctx context.Context, rec domain.SessionRecord, messages []domain.ImportedMessage) (domain.SessionRecord, bool, error) {
	source := rec.Metadata.ImportSource
	if source == nil || source.NativeID == "" || source.ConfigDir == "" {
		return rec, false, errors.New("readable native history is required")
	}
	if len(messages) == 0 && (rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != source.NativeID || rec.Metadata.ProviderConversationID != source.NativeID) {
		return rec, false, errors.New("native Terminal identity is required without a readable transcript")
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
		return insertImportMessages(ctx, q, created.ID, messages)
	})
	return created, fresh, err
}

// insertImportMessages writes messages as the archive's sequences 1..N.
func insertImportMessages(ctx context.Context, q *gen.Queries, id domain.SessionID, messages []domain.ImportedMessage) error {
	for i, msg := range messages {
		if err := q.InsertSessionImportMessage(ctx, gen.InsertSessionImportMessageParams{SessionID: string(id), Sequence: int64(i + 1), Role: string(msg.Role), Text: msg.Text, CreatedAt: msg.At}); err != nil {
			return err
		}
	}
	return nil
}

// importWorkspaceParams binds an adoption write of source and workspace facts.
func importWorkspaceParams(id domain.SessionID, source *domain.SessionImportSource, workspace domain.SessionMetadata, now time.Time) gen.SetSessionImportWorkspaceParams {
	return gen.SetSessionImportWorkspaceParams{
		ID: id, NewSource: marshalImportSource(source), Branch: workspace.Branch, WorkspacePath: workspace.WorkspacePath,
		WorkspaceRepoPath: workspace.WorkspaceRepoPath, DiffBaseSha: workspace.DiffBaseSHA, DiffBaseRef: workspace.DiffBaseRef, UpdatedAt: now,
	}
}

// ReplaceImportedMessages swaps in a fresher read of the source until the
// session's conversation holds a row of its own; from then on live rows follow
// the archive. A first open that failed leaves only a conversation whose
// counter still ends at the archive, and that counter moves with the refresh.
func (s *Store) ReplaceImportedMessages(ctx context.Context, id domain.SessionID, messages []domain.ImportedMessage) error {
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
		// Every open of a pending import refreshes; an unchanged read writes nothing.
		stored, err := q.ListSessionImportArchive(ctx, string(id))
		if err != nil {
			return err
		}
		if slices.EqualFunc(stored, messages, func(row gen.ListSessionImportArchiveRow, msg domain.ImportedMessage) bool {
			return row.Role == string(msg.Role) && row.Text == msg.Text && row.CreatedAt.Equal(msg.At)
		}) {
			return nil
		}
		if err := q.DeleteSessionImportMessages(ctx, string(id)); err != nil {
			return err
		}
		if err := insertImportMessages(ctx, q, id, messages); err != nil {
			return err
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
	// The archive pages like the live conversation it precedes.
	if limit <= 0 {
		limit = DefaultConversationPageSize
	}
	limit = min(limit, 500)
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
// session edits: the import source and workspace's branch, paths and diff base.
func (s *Store) SetSessionImportWorkspace(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, workspace domain.SessionMetadata, now time.Time) (bool, error) {
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
		rows, err := q.SetSessionImportWorkspace(ctx, importWorkspaceParams(id, next, workspace, now))
		updated = rows > 0
		return err
	})
	return updated, err
}

// errPreparedImportLost rolls back a prepared identity whose workspace write
// lost its ownership proof after the session handles moved.
var errPreparedImportLost = errors.New("prepared import lost ownership")

// SetPreparedImportIdentity records a preparer's new native copy before the
// first launch. The session's agent and provider handles move from the source
// identity to the copy in the same transaction, so a chat conversation created
// for the launch binds the copy rather than the untouched original.
func (s *Store) SetPreparedImportIdentity(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, workspace domain.SessionMetadata, now time.Time) (bool, error) {
	if expected == nil || next == nil || next.ResumeNativeID == "" {
		return false, errors.New("prepared import identity is required")
	}
	previous := cmp.Or(expected.ResumeNativeID, expected.NativeID)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	updated := false
	err := s.inTxDB(ctx, "set prepared import identity", func(q *gen.Queries, db gen.DBTX) error {
		row, err := q.GetSession(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		rec := rowToRecord(row)
		if current := rec.Metadata.ImportSource; rec.IsTerminated || current == nil || *current != *expected {
			return nil
		}
		if next.ResumeNativeID != previous && rec.Metadata.ProviderConversationID == previous &&
			(rec.Metadata.AgentSessionID == previous || rec.Metadata.AgentSessionID == "") {
			rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = next.ResumeNativeID, next.ResumeNativeID
			rec.Metadata.AgentSessionIDLaunchID = ""
			rec.UpdatedAt = now
			if err := q.UpdateSession(ctx, recordToUpdate(rec)); err != nil {
				return err
			}
			if db == nil {
				return errors.New("prepared import identity requires its own transaction")
			}
			// A conversation opened before preparation still names the source.
			// Its root moves too while no turn of its own has used that handle.
			if _, err := db.ExecContext(ctx, `
UPDATE conversation_branches SET provider_conversation_id = ?
WHERE parent_branch_id IS NULL AND provider_conversation_id = ?
  AND id = (SELECT active_branch_id FROM conversations WHERE session_id = ? AND current_session_id = ?)
  AND NOT EXISTS (
      SELECT 1 FROM conversation_turns AS t JOIN conversations AS c ON c.id = t.conversation_id
      WHERE c.session_id = ?
  )`, next.ResumeNativeID, previous, id, id, id); err != nil {
				return fmt.Errorf("rebind imported conversation: %w", err)
			}
		}
		rows, err := q.SetSessionImportWorkspace(ctx, importWorkspaceParams(id, next, workspace, now))
		if err != nil {
			return err
		}
		if rows != 1 {
			return errPreparedImportLost
		}
		updated = true
		return nil
	})
	if errors.Is(err, errPreparedImportLost) {
		return false, nil
	}
	return updated, err
}
