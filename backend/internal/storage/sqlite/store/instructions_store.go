package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// instructionRevisionsKept bounds the history of one set of instructions.
// Undo needs only the previous revision; the rest shows dreaming how the text
// evolved and which lines people removed.
const instructionRevisionsKept = 100

// ListInstructionRevisions returns up to limit revisions of one set of
// standing instructions, newest first. An empty projectID selects the user's
// own.
func (s *Store) ListInstructionRevisions(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.InstructionsRevision, error) {
	rows, err := s.qr.ListInstructionRevisions(ctx, gen.ListInstructionRevisionsParams{ProjectKey: &projectID, RowLimit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list instruction revisions %q: %w", projectID, err)
	}
	out := make([]domain.InstructionsRevision, 0, len(rows))
	for _, row := range rows {
		out = append(out, instructionRevisionFromGen(row))
	}
	return out, nil
}

// CurrentInstructions returns the latest text of one set of instructions, or
// "" when none was ever saved.
func (s *Store) CurrentInstructions(ctx context.Context, projectID domain.ProjectID) (string, error) {
	revisions, err := s.ListInstructionRevisions(ctx, projectID, 1)
	if err != nil || len(revisions) == 0 {
		return "", err
	}
	return revisions[0].Body, nil
}

// SaveInstructionRevision appends rev, unless its body already matches the
// latest revision. A non-nil expected must still be the latest revision id
// (0 when none was saved yet), else it returns domain.ErrInstructionsChanged.
// It reports whether it added a revision.
func (s *Store) SaveInstructionRevision(ctx context.Context, rev domain.InstructionsRevision, expected *int64) (domain.InstructionsRevision, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	key := rev.ProjectID
	saved, created := rev, false
	err := s.inTx(ctx, "save instructions", func(q *gen.Queries) error {
		latest, err := q.ListInstructionRevisions(ctx, gen.ListInstructionRevisionsParams{ProjectKey: &key, RowLimit: 1})
		if err != nil {
			return err
		}
		var latestID int64
		if len(latest) > 0 {
			latestID = latest[0].ID
		}
		if expected != nil && *expected != latestID {
			return domain.ErrInstructionsChanged
		}
		if len(latest) > 0 && latest[0].Body == rev.Body {
			saved = instructionRevisionFromGen(latest[0])
			return nil
		}
		var projectID *domain.ProjectID
		if rev.ProjectID != "" {
			projectID = &rev.ProjectID
		}
		id, err := q.InsertInstructionRevision(ctx, gen.InsertInstructionRevisionParams{
			ProjectID: projectID, Body: rev.Body, SessionID: rev.SessionID,
			Dreamed: rev.Dreamed, CreatedAt: rev.CreatedAt, UserLines: strings.Join(rev.UserLines, "\n"),
		})
		if err != nil {
			return err
		}
		if err := q.PruneInstructionRevisions(ctx, gen.PruneInstructionRevisionsParams{ProjectKey: &key, KeepCount: instructionRevisionsKept}); err != nil {
			return err
		}
		saved.ID, created = id, true
		return nil
	})
	return saved, created, err
}

// HumanMessagesForSession returns what the user typed in a session's own chat
// conversations, oldest first, and whether the session had a conversation at
// all. Terminal-only sessions have none.
func (s *Store) HumanMessagesForSession(ctx context.Context, id domain.SessionID) ([]string, bool, error) {
	count, err := s.qr.CountSessionConversations(ctx, &id)
	if err != nil {
		return nil, false, fmt.Errorf("count conversations for %s: %w", id, err)
	}
	if count == 0 {
		return nil, false, nil
	}
	texts, err := s.qr.ListHumanMessagesForSession(ctx, &id)
	if err != nil {
		return nil, true, fmt.Errorf("list user messages for %s: %w", id, err)
	}
	return texts, true, nil
}

// SessionTurnStates returns the states of a session's own conversation turns,
// oldest first. AO closes a dream once its turns have settled.
func (s *Store) SessionTurnStates(ctx context.Context, id domain.SessionID) ([]domain.TurnState, error) {
	states, err := s.qr.ListSessionTurnStates(ctx, &id)
	if err != nil {
		return nil, fmt.Errorf("list turn states for %s: %w", id, err)
	}
	return states, nil
}

// SessionCommands returns up to limit of the newest commands the agent ran in
// a session's own conversations that passed or failed, newest first.
func (s *Store) SessionCommands(ctx context.Context, id domain.SessionID, limit int) ([]domain.SessionCommand, error) {
	rows, err := s.qr.ListSessionCommands(ctx, gen.ListSessionCommandsParams{SessionID: &id, RowLimit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list commands for %s: %w", id, err)
	}
	out := make([]domain.SessionCommand, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.SessionCommand{Command: row.Summary, Failed: row.Status == domain.ActivityStatusFailed})
	}
	return out, nil
}

func instructionRevisionFromGen(row gen.InstructionRevision) domain.InstructionsRevision {
	rev := domain.InstructionsRevision{
		ID: row.ID, Body: row.Body, SessionID: row.SessionID,
		Dreamed: row.Dreamed, CreatedAt: row.CreatedAt,
	}
	for line := range strings.SplitSeq(row.UserLines, "\n") {
		if line != "" {
			rev.UserLines = append(rev.UserLines, line)
		}
	}
	if row.ProjectID != nil {
		rev.ProjectID = *row.ProjectID
	}
	return rev
}
