package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ReleasePathReservations 在物理槽已释放后单独收尾；缺工件与 UNKNOWN 保留全部仓库占位。
func (s *Store) ReleasePathReservations(ctx context.Context, attemptID domain.WorkflowAttemptID, f domain.SchedulerFence, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var released bool
	err := s.inSchedulerTx(ctx, "release frozen artifact paths", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, f, now, true); err != nil {
			return err
		}
		var err error
		released, err = releaseFrozenPaths(ctx, q, attemptID, f, now)
		return err
	})
	return released && err == nil, err
}

// releaseFrozenPaths 先验证所有 Repo 的最新冻结版本与可信停止，再整体释放；TTL 从不参与判断。
func releaseFrozenPaths(ctx context.Context, q *gen.Queries, attemptID domain.WorkflowAttemptID, f domain.SchedulerFence, now time.Time) (bool, error) {
	rows, err := q.ListAttemptPathReservations(ctx, string(attemptID))
	if err != nil || len(rows) == 0 {
		return false, err
	}
	checked := map[string]bool{}
	active := false
	for _, r := range rows {
		if r.ReleasedAt.Valid {
			continue
		}
		active = true
		if r.OwnerID != f.OwnerID || r.Epoch != f.Epoch {
			return false, domain.ErrSchedulerFence
		}
		if r.SessionID == "" || r.Generation == "" {
			return false, nil
		}
		if checked[r.RepoKey] {
			continue
		}
		stored, err := q.GetLatestArtifact(ctx, gen.GetLatestArtifactParams{AttemptID: string(attemptID), RepoKey: r.RepoKey})
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		a, err := artifactFromRow(stored)
		if err != nil {
			return false, err
		}
		if a.State != "verified" || string(a.SessionID) != r.SessionID || a.Generation != r.Generation {
			return false, nil
		}
		session, err := q.GetSession(ctx, a.SessionID)
		if err != nil {
			return false, err
		}
		if !session.IsTerminated {
			return false, nil
		}
		if err := validateArtifactIdentity(ctx, q, a, f, now); err != nil {
			if errors.Is(err, domain.ErrDeliveryBlocked) || errors.Is(err, domain.ErrArtifactInvalid) {
				return false, nil
			}
			return false, err
		}
		checked[r.RepoKey] = true
	}
	if !active {
		return false, nil
	}
	attempt, err := q.GetWorkflowAttempt(ctx, attemptID)
	if err != nil {
		return false, err
	}
	repos, err := q.ListWorkflowTaskRepositories(ctx, gen.ListWorkflowTaskRepositoriesParams{WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID})
	if err != nil {
		return false, err
	}
	for _, repo := range repos {
		if !checked[string(repo.RepoKey)] {
			return false, nil
		}
	}
	for _, r := range rows {
		if r.ReleasedAt.Valid {
			continue
		}
		n, err := q.ReleasePathReservation(ctx, gen.ReleasePathReservationParams{ID: r.ID, Revision: r.Revision, OwnerID: f.OwnerID, Epoch: f.Epoch,
			SessionID: r.SessionID, Generation: r.Generation, Now: sql.NullTime{Time: now.UTC(), Valid: true}})
		if err != nil {
			return false, err
		}
		if n != 1 {
			return false, domain.ErrSchedulerFence
		}
	}
	return true, nil
}
