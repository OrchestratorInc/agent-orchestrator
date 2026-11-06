package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// BeginQualityReview 在短 writer 事务中固定独立身份和代码版本，重复 run ID 不能换工件或换人。
func (s *Store) BeginQualityReview(ctx context.Context, r domain.QualityReview, now time.Time) (domain.QualityReview, error) {
	if r.ID == "" || r.PolicyRevision < 1 || !domain.ValidGitSHA(r.TargetSHA) {
		return r, domain.ErrArtifactInvalid
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inSchedulerTx(ctx, "begin independent review", func(q *gen.Queries) error {
		a, err := reviewArtifact(ctx, q, r, now)
		if err != nil {
			return err
		}
		actorJSON, _ := json.Marshal(r.Actor)
		actorDigest := reviewDigest(actorJSON)
		old, err := q.GetQualityReview(ctx, r.ID)
		if err == nil {
			if old.ArtifactID != r.ArtifactID || old.ArtifactFingerprint != r.ArtifactFingerprint || old.ActorDigest != actorDigest || old.PolicyRevision != r.PolicyRevision || old.PolicyDigest != r.PolicyDigest || old.TargetSha != r.TargetSHA || old.OwnerID != r.Fence.OwnerID || old.Epoch != r.Fence.Epoch {
				return domain.ErrArtifactStale
			}
			r, err = qualityReviewFromRow(old)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		pending, err := q.HasPendingTaskCommit(ctx, string(a.WorkflowID))
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		root, err := q.QualityReviewRootTask(ctx, string(a.TaskID))
		if err != nil {
			return err
		}
		if err := q.InsertQualityReview(ctx, gen.InsertQualityReviewParams{ID: r.ID, ArtifactID: r.ArtifactID, ArtifactFingerprint: r.ArtifactFingerprint, PolicyRevision: r.PolicyRevision, PolicyDigest: r.PolicyDigest, TargetSha: r.TargetSHA, ActorJson: string(actorJSON), ActorDigest: actorDigest, OwnerID: r.Fence.OwnerID, Epoch: r.Fence.Epoch, RootTaskID: root, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}); err != nil {
			return err
		}
		r.State, r.Revision, r.CreatedAt = "running", 1, now.UTC()
		return nil
	})
	return r, err
}

// reviewArtifact 同事务验证作者持久身份、当前工件和审查者；不把 Reviewer permit 当作者 permit。
func reviewArtifact(ctx context.Context, q *gen.Queries, r domain.QualityReview, now time.Time) (domain.ArtifactSnapshot, error) {
	row, err := q.GetArtifact(ctx, r.ArtifactID)
	if err != nil {
		return domain.ArtifactSnapshot{}, err
	}
	a, err := artifactFromRow(row)
	if err != nil {
		return a, err
	}
	if a.State != "verified" || a.Fingerprint != r.ArtifactFingerprint {
		return a, domain.ErrArtifactStale
	}
	policy, err := q.GetWorkflowQualityPolicy(ctx, string(a.WorkflowID))
	if err != nil {
		return a, err
	}
	pv, err := qualityPolicyFromRow(policy)
	if err != nil {
		return a, err
	}
	if pv.Digest != r.PolicyDigest || pv.Policy.Revision != r.PolicyRevision {
		return a, domain.ErrArtifactStale
	}
	p, err := loadExecutionPermit(ctx, q, a.AttemptID)
	if err != nil {
		return a, err
	}
	if err := validateArtifactIdentity(ports.WithExecutionPermit(ctx, p), q, a, r.Fence, now); err != nil {
		return a, err
	}
	actor := r.Actor
	if actor.OperatorRef != "" {
		if actor.AttemptID != "" || actor.SessionID != "" || actor.Generation != "" {
			return a, domain.ErrDeliveryBlocked
		}
		return a, nil
	}
	if actor.AttemptID == "" || actor.AttemptID == a.AttemptID || actor.SessionID == "" || actor.SessionID == a.SessionID || actor.Generation == "" {
		return a, domain.ErrDeliveryBlocked
	}
	reviewer, err := loadExecutionPermit(ctx, q, actor.AttemptID)
	if err != nil {
		return a, err
	}
	if reviewer.WorkflowID != a.WorkflowID || reviewer.TaskID == a.TaskID || reviewer.SessionID != actor.SessionID || reviewer.Generation != actor.Generation || reviewer.Fence != r.Fence {
		return a, domain.ErrDeliveryBlocked
	}
	if err := executionPermitFence(ports.WithExecutionPermit(ctx, reviewer), q, actor.AttemptID, now, true); err != nil {
		return a, err
	}
	task, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: a.WorkflowID, ID: reviewer.TaskID})
	if err != nil {
		return a, err
	}
	if task.Kind != domain.WorkflowTaskKindReview {
		return a, domain.ErrDeliveryBlocked
	}
	session, err := q.GetSession(ctx, actor.SessionID)
	if err != nil {
		return a, err
	}
	if session.IsTerminated || domain.SessionExecutionGeneration(getSessionRowToRecord(session)) != actor.Generation {
		return a, domain.ErrDeliveryBlocked
	}
	binding, err := q.GetWorkflowAttemptBinding(ctx, actor.AttemptID)
	if err != nil {
		return a, err
	}
	if binding.State != domain.WorkflowBindingStateBoundReady && binding.State != domain.WorkflowBindingStateRunning {
		return a, domain.ErrDeliveryBlocked
	}
	return a, nil
}

// GetQualityReview 按持久 ID 重读，不用进程内缓存推断重启后的结果。
func (s *Store) GetQualityReview(ctx context.Context, id string) (domain.QualityReview, bool, error) {
	r, err := s.qr.GetQualityReview(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.QualityReview{}, false, nil
	}
	if err != nil {
		return domain.QualityReview{}, false, err
	}
	v, err := qualityReviewFromRow(r)
	return v, err == nil, err
}

// ListQualityReviews 提供只读侧车事实，不推导 Attempt 成功或释放占位。
func (s *Store) ListQualityReviews(ctx context.Context, workflowID domain.WorkflowID) ([]domain.QualityReview, error) {
	rows, err := s.qr.ListQualityReviews(ctx, string(workflowID))
	if err != nil {
		return nil, err
	}
	result := make([]domain.QualityReview, 0, len(rows))
	for _, r := range rows {
		v, err := qualityReviewFromRow(r)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

// qualityReviewFromRow 解码服务端身份及不可变 run 字段，损坏载荷不能变成有效结论。
func qualityReviewFromRow(r gen.WorkflowQualityReview) (domain.QualityReview, error) {
	v := domain.QualityReview{ID: r.ID, ArtifactID: r.ArtifactID, ArtifactFingerprint: r.ArtifactFingerprint, PolicyRevision: r.PolicyRevision, PolicyDigest: r.PolicyDigest, TargetSHA: r.TargetSha, Fence: domain.SchedulerFence{OwnerID: r.OwnerID, Epoch: r.Epoch}, State: r.State, Verdict: domain.ReviewVerdict(r.Verdict), FixRequestID: r.FixRequestID, Revision: r.Revision, CreatedAt: r.CreatedAt}
	if err := json.Unmarshal([]byte(r.ActorJson), &v.Actor); err != nil {
		return v, err
	}
	if reviewDigest([]byte(r.ActorJson)) != r.ActorDigest {
		return v, domain.ErrArtifactInvalid
	}
	if err := json.Unmarshal([]byte(r.FindingsJson), &v.Findings); err != nil {
		return v, err
	}
	return v, nil
}

// reviewDigest 只用于精确回执重放，不能替代工件与执行身份检查。
func reviewDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
