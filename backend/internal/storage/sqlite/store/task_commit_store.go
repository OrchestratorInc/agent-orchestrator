package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// BeginTaskCommit 在同一事务中重新核对 Gate、Review、Artifact 和执行任期，再记录唯一副作用意图。
func (s *Store) BeginTaskCommit(ctx context.Context, c domain.TaskCommit, now time.Time) (domain.TaskCommit, bool, error) {
	if c.ID == "" || !domain.ValidGitSHA(c.ParentSHA) || !domain.ValidGitSHA(c.TreeSHA) || !domain.ValidGitSHA(c.CommitSHA) || !domain.ValidGitSHA(c.TargetSHA) {
		return c, false, domain.ErrArtifactInvalid
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	created := false
	err := s.inSchedulerTx(ctx, "begin task commit", func(q *gen.Queries) error {
		a, err := taskCommitEvidence(ctx, q, &c, now)
		if err != nil {
			return err
		}
		old, err := q.GetTaskCommit(ctx, c.ID)
		if err == nil {
			if !sameTaskCommitIntent(taskCommitFromRow(old), c) {
				return domain.ErrArtifactStale
			}
			c = taskCommitFromRow(old)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if existing, err := q.GetArtifactTaskCommit(ctx, a.ID); err == nil {
			if existing.ID != c.ID {
				return domain.ErrArtifactStale
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		pending, err := q.HasPendingTaskCommit(ctx, string(a.WorkflowID))
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		if err := q.InsertTaskCommit(ctx, gen.InsertTaskCommitParams{ID: c.ID, ArtifactID: c.ArtifactID, ArtifactFingerprint: c.ArtifactFingerprint, ArtifactRevision: c.ArtifactRevision, PolicyDigest: c.PolicyDigest, GateID: c.GateID, ReviewID: c.ReviewID, ParentSha: c.ParentSHA, TreeSha: c.TreeSHA, CommitSha: c.CommitSHA, TargetSha: c.TargetSHA, OwnerID: c.Fence.OwnerID, Epoch: c.Fence.Epoch, CreatedAt: now.UTC()}); err != nil {
			return err
		}
		c.State, c.Revision, c.CreatedAt = "requested", 1, now.UTC()
		created = true
		return nil
	})
	return c, created, err
}

// ValidateTaskCommit 在外部 Git CAS 前重新核验原意图，不能借长期缓存跨越任期、代际或证据变化。
func (s *Store) ValidateTaskCommit(ctx context.Context, c domain.TaskCommit, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "validate task commit", func(q *gen.Queries) error {
		return validateTaskCommit(ctx, q, c, now)
	})
}

// WithTaskCommitWrite 将最终受限 ref CAS 与换主/换代写互斥；准备对象与持久 requested 必须提前完成。
// SQLite 和 Git 不构成原子事务，进程中断后的 requested 仍只能只读对账。
func (s *Store) WithTaskCommitWrite(ctx context.Context, c domain.TaskCommit, clock func() time.Time, write func(context.Context) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "fence task ref write", func(q *gen.Queries) error {
		if err := validateTaskCommit(ctx, q, c, clock()); err != nil {
			return err
		}
		leader, err := q.GetSchedulerLeader(ctx)
		if err != nil {
			return err
		}
		row, err := q.GetArtifact(ctx, c.ArtifactID)
		if err != nil {
			return err
		}
		permit, err := loadExecutionPermit(ctx, q, domain.WorkflowAttemptID(row.AttemptID))
		if err != nil {
			return err
		}
		session, err := q.GetSession(ctx, permit.SessionID)
		if err != nil {
			return err
		}
		gate, err := q.GetGateRun(ctx, c.GateID)
		if err != nil {
			return err
		}
		now := clock()
		limit := min(5*time.Second, leader.LeaseUntil.Sub(now), gate.ExpiresAt.Sub(now))
		if !session.IsTerminated {
			limit = min(limit, permit.LeaseUntil.Sub(now))
		}
		if limit <= 0 {
			return domain.ErrSchedulerFence
		}
		bounded, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		return write(bounded)
	})
}

// validateTaskCommit 不创建意图；只有当前不可变输入与有效证据完全一致才允许原请求执行。
func validateTaskCommit(ctx context.Context, q *gen.Queries, c domain.TaskCommit, now time.Time) error {
	row, err := q.GetTaskCommit(ctx, c.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrArtifactStale
	}
	if err != nil {
		return err
	}
	old := taskCommitFromRow(row)
	if old.State != "requested" || old.Revision != c.Revision || !sameTaskCommitIntent(old, c) {
		return domain.ErrArtifactStale
	}
	if _, err := taskCommitEvidence(ctx, q, &c, now); err != nil {
		return err
	}
	if !sameTaskCommitIntent(old, c) {
		return domain.ErrArtifactStale
	}
	return nil
}

// taskCommitEvidence 从当前持久事实派生证据，调用方不能自报 Gate/Review 身份。
func taskCommitEvidence(ctx context.Context, q *gen.Queries, c *domain.TaskCommit, now time.Time) (domain.ArtifactSnapshot, error) {
	row, err := q.GetArtifact(ctx, c.ArtifactID)
	if err != nil {
		return domain.ArtifactSnapshot{}, err
	}
	a, err := artifactFromRow(row)
	if err != nil {
		return a, err
	}
	if a.State != "verified" || a.Fingerprint != c.ArtifactFingerprint || a.ArtifactRevision != c.ArtifactRevision || a.Git.HeadSHA != c.ParentSHA || a.Git.TreeSHA != c.TreeSHA {
		return a, domain.ErrArtifactStale
	}
	permit, err := loadExecutionPermit(ctx, q, a.AttemptID)
	if err != nil {
		return a, err
	}
	if err := validateArtifactIdentity(ports.WithExecutionPermit(ctx, permit), q, a, c.Fence, now); err != nil {
		return a, err
	}
	policy, err := q.GetWorkflowQualityPolicy(ctx, string(a.WorkflowID))
	if err != nil {
		return a, err
	}
	c.PolicyDigest = policy.PolicyDigest
	gate, err := q.LatestGateRun(ctx, a.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	if err != nil || gate.State != "PASS" || gate.ArtifactFingerprint != a.Fingerprint || gate.PolicyDigest != c.PolicyDigest || !gate.ExpiresAt.After(now) {
		return a, domain.ErrDeliveryBlocked
	}
	review, err := q.LatestQualityReview(ctx, a.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	if err != nil || review.State != "approved" || review.ArtifactFingerprint != a.Fingerprint || review.PolicyDigest != c.PolicyDigest || review.TargetSha != c.TargetSHA {
		return a, domain.ErrDeliveryBlocked
	}
	c.GateID, c.ReviewID = gate.ID, review.ID
	return a, nil
}

// sameTaskCommitIntent 比较副作用全部输入；状态、回执 revision 和创建时间不产生新写入权。
func sameTaskCommitIntent(a, b domain.TaskCommit) bool {
	a.State, b.State = "", ""
	a.Revision, b.Revision = 0, 0
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	return a == b
}

// ReconcileTaskCommit 允许新 recovered Leader 记录只读 Git 对账；不接管或再次执行旧意图。
func (s *Store) ReconcileTaskCommit(ctx context.Context, id string, revision int64, state string, fence domain.SchedulerFence, now time.Time) (domain.TaskCommit, error) {
	if state != "committed" && state != "unknown" {
		return domain.TaskCommit{}, domain.ErrDeliveryBlocked
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.TaskCommit
	err := s.inSchedulerTx(ctx, "reconcile task commit", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, fence, now, true); err != nil {
			return err
		}
		row, err := q.GetTaskCommit(ctx, id)
		if err != nil {
			return err
		}
		c := taskCommitFromRow(row)
		if c.Revision != revision {
			return domain.ErrArtifactStale
		}
		if c.State == state {
			result = c
			return nil
		}
		n, err := q.FinishTaskCommit(ctx, gen.FinishTaskCommitParams{State: state, ID: id, ExpectedRevision: revision, OwnerID: c.Fence.OwnerID, Epoch: c.Fence.Epoch})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrArtifactStale
		}
		c.State, c.Revision = state, revision+1
		result = c
		return nil
	})
	return result, err
}

// FinishTaskCommit 只接受原意图的任期和 revision；失败默认 UNKNOWN，调用方必须先对账。
func (s *Store) FinishTaskCommit(ctx context.Context, id string, revision int64, state string, fence domain.SchedulerFence, now time.Time) (domain.TaskCommit, error) {
	if state != "committed" && state != "unknown" && state != "blocked" {
		return domain.TaskCommit{}, domain.ErrDeliveryBlocked
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.TaskCommit
	err := s.inSchedulerTx(ctx, "finish task commit", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, fence, now, true); err != nil {
			return err
		}
		row, err := q.GetTaskCommit(ctx, id)
		if err != nil {
			return err
		}
		c := taskCommitFromRow(row)
		if c.Fence != fence || c.Revision != revision || c.State != "requested" && c.State != "unknown" {
			return domain.ErrArtifactStale
		}
		n, err := q.FinishTaskCommit(ctx, gen.FinishTaskCommitParams{State: state, ID: id, ExpectedRevision: revision, OwnerID: fence.OwnerID, Epoch: fence.Epoch})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrArtifactStale
		}
		c.State, c.Revision = state, revision+1
		result = c
		return nil
	})
	return result, err
}

// GetArtifactTaskCommit 按唯一工件恢复意图，换请求 ID 也不能重复执行 Git 副作用。
func (s *Store) GetArtifactTaskCommit(ctx context.Context, artifactID string) (domain.TaskCommit, bool, error) {
	r, err := s.qr.GetArtifactTaskCommit(ctx, artifactID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TaskCommit{}, false, nil
	}
	if err != nil {
		return domain.TaskCommit{}, false, err
	}
	return taskCommitFromRow(r), true, nil
}

// ListTaskCommits 返回持久操作事实供协调摘要和 Integration 选择。
func (s *Store) ListTaskCommits(ctx context.Context, w domain.WorkflowID) ([]domain.TaskCommit, error) {
	rows, err := s.qr.ListTaskCommits(ctx, string(w))
	if err != nil {
		return nil, err
	}
	result := make([]domain.TaskCommit, 0, len(rows))
	for _, row := range rows {
		result = append(result, taskCommitFromRow(row))
	}
	return result, nil
}

// GetTaskCommit 在重启后恢复唯一提交意图，不把 requested/unknown 推断为已提交。
func (s *Store) GetTaskCommit(ctx context.Context, id string) (domain.TaskCommit, bool, error) {
	r, err := s.qr.GetTaskCommit(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TaskCommit{}, false, nil
	}
	if err != nil {
		return domain.TaskCommit{}, false, err
	}
	return taskCommitFromRow(r), true, nil
}

func taskCommitFromRow(r gen.WorkflowTaskCommit) domain.TaskCommit {
	return domain.TaskCommit{ID: r.ID, ArtifactID: r.ArtifactID, ArtifactFingerprint: r.ArtifactFingerprint, ArtifactRevision: r.ArtifactRevision, PolicyDigest: r.PolicyDigest, GateID: r.GateID, ReviewID: r.ReviewID, ParentSHA: r.ParentSha, TreeSHA: r.TreeSha, CommitSHA: r.CommitSha, TargetSHA: r.TargetSha, State: r.State, Fence: domain.SchedulerFence{OwnerID: r.OwnerID, Epoch: r.Epoch}, Revision: r.Revision, CreatedAt: r.CreatedAt}
}
