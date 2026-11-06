// Package taskcommit 只在可信 Gate/Review 后写入任务分支；重启或重复请求仅核对已有提交意图。
package taskcommit

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
)

// Store 的持久意图与 CAS 是唯一写入权，服务进程内不会缓存可重用的 Git 授权。
type Store interface {
	GetArtifact(context.Context, string) (domain.ArtifactSnapshot, bool, error)
	GetTaskCommit(context.Context, string) (domain.TaskCommit, bool, error)
	GetArtifactTaskCommit(context.Context, string) (domain.TaskCommit, bool, error)
	GetGateRun(context.Context, string) (domain.GateRun, bool, error)
	BeginTaskCommit(context.Context, domain.TaskCommit, time.Time) (domain.TaskCommit, bool, error)
	FinishTaskCommit(context.Context, string, int64, string, domain.SchedulerFence, time.Time) (domain.TaskCommit, error)
	ValidateTaskCommit(context.Context, domain.TaskCommit, time.Time) error
	WithTaskCommitWrite(context.Context, domain.TaskCommit, func() time.Time, func(context.Context) error) error
	ReconcileTaskCommit(context.Context, string, int64, string, domain.SchedulerFence, time.Time) (domain.TaskCommit, error)
}

// Service 由 daemon 装配可信 Artifact/Gate 服务，外部不能传入路径、SHA 或提交内容。
type Service struct {
	Store     Store
	Artifacts *artifact.Service
	Gates     *qualitygate.Service
	Clock     func() time.Time
}

// Commit 先记录精确意图，再执行一次任务分支 CAS；不确定结果保留 UNKNOWN，禁止盲目重试。
func (s *Service) Commit(ctx context.Context, id, artifactID string, revision int64, fence domain.SchedulerFence) (domain.TaskCommit, error) {
	old, found, err := s.Store.GetTaskCommit(ctx, id)
	if err != nil {
		return old, err
	}
	if found {
		if old.ArtifactID != artifactID || old.ArtifactRevision != revision {
			return old, domain.ErrArtifactStale
		}
		return s.observe(ctx, old, fence)
	}
	old, found, err = s.Store.GetArtifactTaskCommit(ctx, artifactID)
	if err != nil {
		return old, err
	}
	if found {
		if old.ID != id || old.ArtifactRevision != revision {
			return old, domain.ErrArtifactStale
		}
		return s.observe(ctx, old, fence)
	}
	a, found, err := s.Store.GetArtifact(ctx, artifactID)
	if err != nil {
		return domain.TaskCommit{}, err
	}
	if !found || a.State != "verified" || a.ArtifactRevision != revision {
		return domain.TaskCommit{}, domain.ErrArtifactStale
	}
	if err := s.Artifacts.Revalidate(ctx, a); err != nil {
		return domain.TaskCommit{}, err
	}
	target, err := gitworktree.DeliveryTargetSHA(ctx, a)
	if err != nil {
		return domain.TaskCommit{}, err
	}
	sha, err := gitworktree.PrepareDeliveryCommit(ctx, a)
	if err != nil {
		return domain.TaskCommit{}, err
	}
	c, created, err := s.Store.BeginTaskCommit(ctx, domain.TaskCommit{ID: id, ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, ArtifactRevision: revision, ParentSHA: a.Git.HeadSHA, TreeSHA: a.Git.TreeSHA, CommitSHA: sha, TargetSHA: target, Fence: fence}, s.Clock())
	if err != nil {
		return c, err
	}
	if !created {
		return s.observe(ctx, c, fence)
	}
	gate, found, err := s.Store.GetGateRun(ctx, c.GateID)
	if err == nil && !found {
		err = domain.ErrDeliveryBlocked
	}
	if err == nil {
		err = s.Gates.RevalidateEvidence(ctx, a, gate)
	}
	if err == nil {
		err = s.Store.ValidateTaskCommit(ctx, c, s.Clock())
	}
	if err != nil {
		blocked, saveErr := s.Store.FinishTaskCommit(ctx, c.ID, c.Revision, "blocked", fence, s.Clock())
		return blocked, errors.Join(err, saveErr)
	}
	applyErr := gitworktree.ApplyDeliveryCommit(ctx, a, sha, target, s.Artifacts.Scratch, func(ctx context.Context, write func(context.Context) error) error {
		return s.Store.WithTaskCommitWrite(ctx, c, s.Clock, write)
	})
	state, observationErr := gitworktree.ObserveDeliveryCommit(ctx, a, sha)
	if observationErr != nil {
		state = "unknown"
	}
	result, saveErr := s.Store.FinishTaskCommit(ctx, c.ID, c.Revision, state, fence, s.Clock())
	if state != "committed" {
		return result, errors.Join(domain.ErrDeliveryBlocked, applyErr, observationErr, saveErr)
	}
	return result, saveErr
}

// observe 只读核对相同对象，不执行第二次 update-ref；未知旧意图不能获得新执行权。
func (s *Service) observe(ctx context.Context, c domain.TaskCommit, fence domain.SchedulerFence) (domain.TaskCommit, error) {
	if c.State == "blocked" {
		return c, domain.ErrDeliveryBlocked
	}
	a, found, err := s.Store.GetArtifact(ctx, c.ArtifactID)
	if err != nil {
		return c, err
	}
	if !found || a.Fingerprint != c.ArtifactFingerprint {
		return c, domain.ErrArtifactStale
	}
	state, err := gitworktree.ObserveDeliveryCommit(ctx, a, c.CommitSHA)
	if err != nil || state != "committed" {
		return c, errors.Join(domain.ErrDeliveryBlocked, err)
	}
	return s.Store.ReconcileTaskCommit(ctx, c.ID, c.Revision, state, fence, s.Clock())
}
