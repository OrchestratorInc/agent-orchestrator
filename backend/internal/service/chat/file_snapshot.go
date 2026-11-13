package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fileSnapshotBudget bounds the snapshot taken before each turn. The turn waits
// for it, so a repository too large to snapshot in time costs that turn its file
// restore rather than holding the agent.
const fileSnapshotBudget = 10 * time.Second

// RollbackPreview says what rolling back to a turn would do to the worktree.
type RollbackPreview struct {
	// Unavailable says why the files cannot be restored; empty when they can. A
	// rollback then changes only the conversation.
	Unavailable string
	Changes     []filesnapshot.Change
	// CommitsSince counts commits made on the branch after the turn. A restore
	// leaves them in place, so their changes come back as uncommitted edits.
	CommitsSince int
	// PlanID is what a rollback that restores these files must be given. It stops
	// matching as soon as the worktree changes, so nothing the preview did not
	// list is ever overwritten.
	PlanID string
}

// RollbackPreview reports what restoring the files from before turnID would
// change, without changing anything or waking the session's agent.
func (s *Service) RollbackPreview(ctx context.Context, id domain.SessionID, turnID string) (RollbackPreview, error) {
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return RollbackPreview{}, err
	}
	if !found {
		return RollbackPreview{}, ports.ErrSessionNotFound
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return RollbackPreview{}, err
	}
	turn, err := s.store.TurnByID(ctx, turnID)
	if err != nil {
		return RollbackPreview{}, err
	}
	if turn.ConversationID != conversation.ID {
		return RollbackPreview{}, fmt.Errorf("%w: turn %s is not in this session's conversation",
			domain.ErrNoConversationTurn, turnID)
	}
	restore, unavailable, err := planFileRestore(ctx, record.Metadata.WorkspacePath, id, turnID)
	if err != nil {
		return RollbackPreview{}, err
	}
	return RollbackPreview{Unavailable: unavailable, Changes: restore.Changes, CommitsSince: restore.CommitsSince, PlanID: restore.PlanID}, nil
}

// restoreFiles puts the worktree back to the snapshot taken before turnID, once
// the files are known to still match the preview whose PlanID the user
// confirmed. What it overwrites is saved under a ref first, so it outlives the
// rollback, and a failed write is undone before this returns.
func (c *Controller) restoreFiles(ctx context.Context, turnID, planID string) (filesnapshot.Restore, error) {
	restore, unavailable, err := planFileRestore(ctx, c.workspacePath, c.sessionID, turnID)
	if err != nil {
		return filesnapshot.Restore{}, err
	}
	if unavailable != "" {
		return filesnapshot.Restore{}, fmt.Errorf("%w: %s", ErrFilesNotRestorable, unavailable)
	}
	if restore.PlanID != planID {
		return filesnapshot.Restore{}, ErrFilesChanged
	}
	if len(restore.Changes) == 0 {
		return restore, nil
	}
	if err := restore.Save(ctx, filesnapshot.BeforeRollbackRef(c.sessionID, turnID)); err != nil {
		return filesnapshot.Restore{}, fmt.Errorf("save files before restoring %s: %w", turnID, err)
	}
	if err := restore.Apply(ctx); err != nil {
		return filesnapshot.Restore{}, c.undoFileRestore(ctx, restore, fmt.Errorf("restore files for %s: %w", turnID, err))
	}
	return restore, nil
}

// planFileRestore works out how to put the worktree back to before turnID. A
// non-empty unavailable says, in words a user can act on, why it cannot be.
func planFileRestore(ctx context.Context, workspace string, session domain.SessionID, turnID string) (filesnapshot.Restore, string, error) {
	if workspace == "" {
		return filesnapshot.Restore{}, "this session has no workspace", nil
	}
	// Planning hashes the worktree like the snapshot before a turn does, so it
	// gets the same budget instead of holding the preview or the rollback.
	planCtx, cancel := context.WithTimeout(ctx, fileSnapshotBudget)
	defer cancel()
	restore, err := filesnapshot.Plan(planCtx, workspace, filesnapshot.Ref(session, turnID))
	switch {
	case err != nil && ctx.Err() == nil && errors.Is(planCtx.Err(), context.DeadlineExceeded):
		return restore, "reading the worktree's files took too long", nil
	case errors.Is(err, filesnapshot.ErrNotRepository):
		return restore, "the workspace is not a git repository", nil
	case errors.Is(err, filesnapshot.ErrNotFound):
		return restore, "AO has no snapshot of the files from before this turn", nil
	case errors.Is(err, filesnapshot.ErrNestedRepository):
		return restore, "the workspace contains another git repository, whose files AO cannot restore", nil
	case errors.Is(err, filesnapshot.ErrTooLarge):
		return restore, "the worktree has too many or too large untracked files to restore safely", nil
	case err != nil:
		return restore, "", fmt.Errorf("plan file restore for %s: %w", turnID, err)
	}
	return restore, "", nil
}

// snapshotFiles records the worktree before turns reach the provider, so rolling
// one back can also put its files back. Only a session whose agent can roll back
// gets one, and a failure never holds the turn: it only means that turn's
// rollback cannot restore files.
func (c *Controller) snapshotFiles(ctx context.Context, turnIDs []string) {
	if _, ok := c.conv.(ports.ChatRollbacker); !ok || c.workspacePath == "" || c.reviewID != "" {
		return
	}
	refs := make([]string, len(turnIDs))
	for i, turnID := range turnIDs {
		refs[i] = filesnapshot.Ref(c.sessionID, turnID)
	}
	ctx, cancel := context.WithTimeout(ctx, fileSnapshotBudget)
	defer cancel()
	if err := filesnapshot.Capture(ctx, c.workspacePath, refs...); err != nil {
		level := slog.LevelWarn
		switch {
		case errors.Is(err, filesnapshot.ErrNotRepository):
			level = slog.LevelDebug
		case errors.Is(err, filesnapshot.ErrTooLarge):
			level = slog.LevelInfo
		}
		c.log.Log(ctx, level, "chat file snapshot failed; rolling this turn back will not restore files",
			"session", c.sessionID, "turn", turnIDs[0], "error", err)
	}
}

// undoFileRestore puts back files a rollback restored before the step that
// failed, and returns that failure.
func (c *Controller) undoFileRestore(ctx context.Context, restore filesnapshot.Restore, cause error) error {
	if len(restore.Changes) == 0 {
		return cause
	}
	if err := restore.Undo(context.WithoutCancel(ctx)); err != nil {
		c.log.Error("chat rollback: files were restored but could not be put back after a failure",
			"session", c.sessionID, "error", err, "cause", cause)
		return errors.Join(cause, fmt.Errorf("put restored files back: %w", err))
	}
	return cause
}
