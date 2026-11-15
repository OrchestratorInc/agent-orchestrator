package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var forkSnapshots atomic.Bool

func EnableForkSnapshots(enabled bool) { forkSnapshots.Store(enabled) }

var turnArtifactSnapshot atomic.Pointer[func(context.Context, domain.SessionID, []string)]

func SetTurnArtifactSnapshot(snapshot func(context.Context, domain.SessionID, []string)) {
	if snapshot == nil {
		turnArtifactSnapshot.Store(nil)
		return
	}
	turnArtifactSnapshot.Store(&snapshot)
}

const fileSnapshotBudget = 10 * time.Second

type RollbackPreview struct {
	Unavailable  string
	Changes      []filesnapshot.Change
	CommitsSince int
	PlanID       string
}

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

func planFileRestore(ctx context.Context, workspace string, session domain.SessionID, turnID string) (filesnapshot.Restore, string, error) {
	if workspace == "" {
		return filesnapshot.Restore{}, "this session has no workspace", nil
	}
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

func (c *Controller) snapshotFiles(ctx context.Context, turnIDs []string) {
	_, rollsBack := c.conv.(ports.ChatRollbacker)
	forForks := forkSnapshots.Load() && forksOlderTurns(c.harness) && c.reviewID == ""
	var artifacts sync.WaitGroup
	if snapshot := turnArtifactSnapshot.Load(); snapshot != nil && forForks {
		artifacts.Add(1)
		go func(ctx context.Context, id domain.SessionID, turnIDs []string) {
			defer artifacts.Done()
			(*snapshot)(ctx, id, turnIDs)
		}(ctx, c.sessionID, turnIDs)
	}
	defer artifacts.Wait()
	if (!rollsBack && !forForks) || c.workspacePath == "" || c.reviewID != "" {
		return
	}
	refs := make([]string, len(turnIDs))
	for i, turnID := range turnIDs {
		refs[i] = filesnapshot.Ref(c.sessionID, turnID)
	}
	captureCtx, cancel := context.WithTimeout(ctx, fileSnapshotBudget)
	defer cancel()
	if err := filesnapshot.Capture(captureCtx, c.workspacePath, refs...); err != nil {
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
