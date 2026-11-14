package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ConversationFindSource reads messages and their visibility facts from one
// read transaction. Search never reconciles state or acquires a provider lease.
func (s *Store) ConversationFindSource(ctx context.Context, id string) (domain.ConversationRecord, []domain.ConversationMessage, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.ConversationRecord{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := gen.New(tx)
	conv, err := q.SelectConversationByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationRecord{}, nil, ErrConversationNotFound
	}
	if err != nil {
		return domain.ConversationRecord{}, nil, err
	}
	view := *s
	view.qr = q
	floor, err := view.conversationVisibleAfterSequence(ctx, conv)
	if err != nil {
		return domain.ConversationRecord{}, nil, err
	}
	rows, err := q.SelectConversationMessages(ctx, id)
	if err != nil {
		return domain.ConversationRecord{}, nil, err
	}
	turns, err := q.SelectConversationTurns(ctx, id)
	if err != nil {
		return domain.ConversationRecord{}, nil, err
	}
	queued := map[string]bool{}
	for _, turn := range turns {
		if turn.State == domain.TurnStateQueued {
			queued[turn.ID] = true
		}
	}
	messages := make([]domain.ConversationMessage, 0, len(rows))
	for _, row := range rows {
		message := messageToDomain(row)
		if message.Sequence > floor && !queued[message.TurnID] {
			messages = append(messages, message)
		}
	}
	return conversationToDomain(conv), messages, nil
}

// ConversationFindWindow reads bounded context without reconciliation writes.
func (s *Store) ConversationFindWindow(ctx context.Context, id string, sequence int64) (domain.ConversationFindWindow, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.ConversationFindWindow{}, err
	}
	defer func() { _ = tx.Rollback() }()
	view := *s
	view.qr = gen.New(tx)
	before := sequence
	if before > (1<<63-1)-51 {
		before = 1<<63 - 1
	} else {
		before += 51
	}
	page, err := view.loadConversationSnapshotRange(ctx, id, before, 0, 100, false)
	if err != nil {
		return domain.ConversationFindWindow{}, err
	}
	return domain.ConversationFindWindow{Conversation: page.Conversation, ActiveBranch: page.ActiveBranch, Messages: page.Messages, Activities: page.Activities, Turns: page.Turns, OldestSequence: page.OldestSequence, HasMoreBefore: page.HasMoreBefore, BranchPoints: page.BranchPoints}, nil
}
