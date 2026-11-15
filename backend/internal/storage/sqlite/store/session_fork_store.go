package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (s *Store) RecordSessionFork(ctx context.Context, fork domain.SessionFork) error {
	if fork.ForkSessionID == "" || fork.SourceSessionID == "" || fork.RootSessionID == "" {
		return errors.New("record session fork: fork, source, and root ids are required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `
INSERT INTO session_forks (fork_session_id, source_session_id, root_session_id, source_turn_id, source_title, context_mode, fork_number, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(fork.ForkSessionID), string(fork.SourceSessionID), string(fork.RootSessionID), fork.SourceTurnID,
		fork.SourceTitle, string(fork.ContextMode), fork.ForkNumber, fork.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("record session fork %s: %w", fork.ForkSessionID, err)
	}
	return nil
}

func (s *Store) GetSessionFork(ctx context.Context, forkID domain.SessionID) (domain.SessionFork, bool, error) {
	row := s.readDB.QueryRowContext(ctx, sessionForkSelect+` WHERE fork_session_id = ?`, string(forkID))
	fork, err := scanSessionFork(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionFork{}, false, nil
	}
	if err != nil {
		return domain.SessionFork{}, false, fmt.Errorf("get session fork %s: %w", forkID, err)
	}
	return fork, true, nil
}

func (s *Store) ListSessionForks(ctx context.Context, sourceID domain.SessionID) ([]domain.SessionFork, error) {
	rows, err := s.readDB.QueryContext(ctx, sessionForkSelect+` WHERE source_session_id = ? ORDER BY fork_number, created_at`, string(sourceID))
	if err != nil {
		return nil, fmt.Errorf("list session forks of %s: %w", sourceID, err)
	}
	defer func() { _ = rows.Close() }()
	var forks []domain.SessionFork
	for rows.Next() {
		fork, err := scanSessionFork(rows)
		if err != nil {
			return nil, fmt.Errorf("list session forks of %s: %w", sourceID, err)
		}
		forks = append(forks, fork)
	}
	return forks, rows.Err()
}

func (s *Store) NextSessionForkNumber(ctx context.Context, rootID domain.SessionID) (int, error) {
	var highest int
	err := s.readDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(fork_number), 0) FROM session_forks WHERE root_session_id = ?`, string(rootID)).Scan(&highest)
	if err != nil {
		return 0, fmt.Errorf("next fork number of %s: %w", rootID, err)
	}
	return highest + 1, nil
}

const sessionForkSelect = `SELECT fork_session_id, source_session_id, root_session_id, source_turn_id, source_title, context_mode, fork_number, created_at FROM session_forks`

type rowScanner interface{ Scan(dest ...any) error }

func scanSessionFork(row rowScanner) (domain.SessionFork, error) {
	var (
		fork                     domain.SessionFork
		forkID, sourceID, rootID string
		mode                     string
		created                  time.Time
	)
	if err := row.Scan(&forkID, &sourceID, &rootID, &fork.SourceTurnID, &fork.SourceTitle, &mode, &fork.ForkNumber, &created); err != nil {
		return domain.SessionFork{}, err
	}
	fork.ForkSessionID, fork.SourceSessionID, fork.RootSessionID = domain.SessionID(forkID), domain.SessionID(sourceID), domain.SessionID(rootID)
	fork.ContextMode = domain.SessionForkContextMode(mode)
	fork.CreatedAt = created.UTC()
	return fork, nil
}

func (s *Store) RewriteConversationText(ctx context.Context, conversationID, from, to string) (int, error) {
	if from == "" || from == to {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, `UPDATE conversation_messages
		SET text = replace(text, ?, ?), revision = revision + 1
		WHERE conversation_id = ? AND instr(text, ?) > 0`, from, to, conversationID, from)
	if err != nil {
		return 0, fmt.Errorf("rewrite conversation text: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

var replayPlaceholder = regexp.MustCompile(`\[(?:Image|Audio|Embedded context|File: [^\]]*)\]`)

func (s *Store) CopyForkMessageContent(ctx context.Context, sourceConversationID, forkConversationID string) (int, error) {
	type userMessage struct{ id, text, content string }
	load := func(conversationID string) ([]userMessage, error) {
		rows, err := s.readDB.QueryContext(ctx, `SELECT m.id, m.text, COALESCE(m.delivery_content_json, '') FROM conversation_messages m
			LEFT JOIN conversation_turns t ON t.id = m.turn_id
			WHERE m.conversation_id = ? AND m.role = 'user' AND t.rolled_back_at IS NULL ORDER BY m.sequence`, conversationID)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var out []userMessage
		for rows.Next() {
			var m userMessage
			if err := rows.Scan(&m.id, &m.text, &m.content); err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return out, rows.Err()
	}
	source, err := load(sourceConversationID)
	if err != nil {
		return 0, fmt.Errorf("load original messages: %w", err)
	}
	fork, err := load(forkConversationID)
	if err != nil {
		return 0, fmt.Errorf("load fork messages: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	changed, next := 0, 0
	for _, m := range fork {
		want := strings.TrimSpace(replayPlaceholder.ReplaceAllString(m.text, ""))
		for i := next; i < len(source); i++ {
			if strings.TrimSpace(source[i].text) != want {
				continue
			}
			next = i + 1
			if source[i].content != "" && m.content == "" {
				if _, err := s.writeDB.ExecContext(ctx, `UPDATE conversation_messages
					SET text = ?, delivery_content_json = ?, revision = revision + 1 WHERE id = ?`,
					source[i].text, source[i].content, m.id); err != nil {
					return changed, fmt.Errorf("copy message content: %w", err)
				}
				changed++
			}
			break
		}
	}
	return changed, nil
}

func (s *Store) SetConversationBranchHandoffNote(ctx context.Context, conversationID, branchID, note string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, `UPDATE conversation_branches SET handoff_note = ? WHERE conversation_id = ? AND id = ?`, note, conversationID, branchID)
	if err != nil {
		return fmt.Errorf("set handoff note on branch %s: %w", branchID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("set handoff note: branch %s not found", branchID)
	}
	return nil
}
