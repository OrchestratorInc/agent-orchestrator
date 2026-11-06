package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// BeginIntegrationOperation 原子冻结完整操作；批准消费失败或任一仓缺证据时不创建任何意图。
// 批准存储与 SQLite 不是分布式事务，提交失败只能烧掉批准，不能重放 Git。
func (s *Store) BeginIntegrationOperation(ctx context.Context, op domain.IntegrationOperation, request domain.IntegrationApplyRequest, now time.Time, authorize func() (string, error)) (domain.IntegrationOperation, bool, error) {
	if op.ID == "" || (op.Action != "apply" && op.Action != "push") || op.RequestDigest == "" || authorize == nil {
		return op, false, domain.ErrDeliveryBlocked
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	created := false
	err := s.inSchedulerTx(ctx, "begin integration operation", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, op.Fence, now, true); err != nil {
			return err
		}
		old, err := q.GetIntegrationOperation(ctx, op.ID)
		if err == nil {
			if old.PlanID != op.PlanID || old.WorkflowID != string(op.WorkflowID) || old.Action != op.Action || old.RequestDigest != op.RequestDigest {
				return domain.ErrArtifactStale
			}
			op = integrationOperationFromRow(old)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		plan, err := q.GetIntegrationPlan(ctx, op.PlanID)
		if err != nil {
			return err
		}
		p := integrationPlanFromRow(plan)
		if p.WorkflowID != op.WorkflowID || p.Status != "clean" || request.ExpectedRevision != p.Revision || request.PlanDigest != p.PlanDigest || p.Digest(request.Entries) != p.PlanDigest {
			return domain.ErrArtifactStale
		}
		if op.Action == "apply" && len(request.PushTargets) != 0 {
			return domain.ErrArtifactInvalid
		}
		if op.Action == "push" {
			applied, err := q.GetPlanIntegrationOperation(ctx, gen.GetPlanIntegrationOperationParams{PlanID: op.PlanID, Action: "apply"})
			if err != nil || applied.State != "applied" {
				return domain.ErrDeliveryBlocked
			}
			if len(request.PushTargets) != len(request.Entries) {
				return domain.ErrArtifactInvalid
			}
			seen := make(map[string]bool)
			for i, target := range request.PushTargets {
				e := request.Entries[i]
				if target.RepoKey != e.RepoKey || target.CommitSHA != e.MergeCommitSHA || target.TargetRef != e.TargetRef || !domain.ValidGitSHA(target.ExpectedRemoteSHA) || target.RemoteKey == "" || len(target.RemoteIdentity) != 64 || seen[target.RemoteIdentity] {
					return domain.ErrArtifactInvalid
				}
				seen[target.RemoteIdentity] = true
			}
		}
		ids := make([]string, 0, len(request.Entries))
		for _, entry := range request.Entries {
			ids = append(ids, entry.ArtifactID)
		}
		if err := integrationArtifacts(ctx, q, op.WorkflowID, ids, op.Fence, now); err != nil {
			return err
		}
		pending, err := q.HasOpenIntegrationOperation(ctx, string(op.WorkflowID))
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		if _, err := q.GetPlanIntegrationOperation(ctx, gen.GetPlanIntegrationOperationParams{PlanID: op.PlanID, Action: op.Action}); err == nil {
			return domain.ErrArtifactStale
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		ref, err := authorize()
		if err != nil {
			return err
		}
		if ref == "" {
			return domain.ErrDeliveryBlocked
		}
		op.ApprovalRef = ref
		requestJSON, err := json.Marshal(request)
		if err != nil {
			return err
		}
		op.RequestJSON = string(requestJSON)
		if err := q.InsertIntegrationOperation(ctx, gen.InsertIntegrationOperationParams{ID: op.ID, PlanID: op.PlanID, WorkflowID: string(op.WorkflowID), Action: op.Action, RequestDigest: op.RequestDigest, RequestJson: op.RequestJSON, ApprovalRef: ref, OwnerID: op.Fence.OwnerID, Epoch: op.Fence.Epoch, CreatedAt: now.UTC()}); err != nil {
			return err
		}
		for _, entry := range request.Entries {
			if err := q.InsertIntegrationRepoOperation(ctx, gen.InsertIntegrationRepoOperationParams{OperationID: op.ID, RepoKey: string(entry.RepoKey)}); err != nil {
				return err
			}
		}
		op.State, op.Revision, op.CreatedAt, created = "requested", 1, now.UTC(), true
		return nil
	})
	return op, created, err
}

// RequestIntegrationRepo 先持久 requested，再允许一个仓执行；只有前序全部成功才可领取后继。
func (s *Store) RequestIntegrationRepo(ctx context.Context, op domain.IntegrationOperation, key domain.WorkflowRepoKey, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "request integration repository", func(q *gen.Queries) error {
		if err := validateIntegrationOperation(ctx, q, op, now); err != nil {
			return err
		}
		rows, err := q.ListIntegrationRepoOperations(ctx, op.ID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.RepoKey == string(key) {
				if row.State != "prepared" {
					return domain.ErrArtifactStale
				}
				return setIntegrationRepo(ctx, q, row, "requested", "")
			}
			if row.State != "applied" {
				return domain.ErrDeliveryBlocked
			}
		}
		return domain.ErrArtifactStale
	})
}

// WithIntegrationWrite 以 writer 事务隔离换主、代际变化和恢复；副作用结果与该仓回执同事务记录。
// Git 可能先于 SQLite 成功，事务回滚或断电后只允许只读对账。
func (s *Store) WithIntegrationWrite(ctx context.Context, op domain.IntegrationOperation, entry domain.IntegrationRepoPreview, clock func() time.Time, write func(context.Context) error) error {
	if clock == nil || write == nil {
		return domain.ErrDeliveryBlocked
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var writeErr error
	err := s.inSchedulerTx(ctx, "apply integration repository", func(q *gen.Queries) error {
		now := clock()
		if err := validateIntegrationOperation(ctx, q, op, now); err != nil {
			return err
		}
		row, err := q.GetIntegrationRepoOperation(ctx, gen.GetIntegrationRepoOperationParams{OperationID: op.ID, RepoKey: string(entry.RepoKey)})
		if err != nil {
			return err
		}
		if row.State != "requested" {
			return domain.ErrArtifactStale
		}
		entries, err := q.GetIntegrationRepoPreviews(ctx, op.PlanID)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(entries))
		matched := false
		for _, e := range entries {
			ids = append(ids, e.ArtifactID)
			if e.RepoKey == string(entry.RepoKey) && e.PreviewDigest == entry.Digest() && entry.PreviewDigest == e.PreviewDigest {
				matched = true
			}
		}
		if !matched {
			return domain.ErrArtifactStale
		}
		if err := integrationArtifacts(ctx, q, op.WorkflowID, ids, op.Fence, now); err != nil {
			return err
		}
		leader, err := q.GetSchedulerLeader(ctx)
		if err != nil {
			return err
		}
		limit := min(5*time.Second, leader.LeaseUntil.Sub(now))
		for _, id := range ids {
			a, err := q.GetArtifact(ctx, id)
			if err != nil {
				return err
			}
			permit, err := loadExecutionPermit(ctx, q, domain.WorkflowAttemptID(a.AttemptID))
			if err != nil {
				return err
			}
			session, err := q.GetSession(ctx, permit.SessionID)
			if err != nil {
				return err
			}
			gate, err := q.LatestGateRun(ctx, id)
			if err != nil {
				return err
			}
			limit = min(limit, gate.ExpiresAt.Sub(now))
			if !session.IsTerminated {
				limit = min(limit, permit.LeaseUntil.Sub(now))
			}
		}
		if limit <= 0 {
			return domain.ErrSchedulerFence
		}
		bounded, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		writeErr = write(bounded)
		state, sha := "applied", entry.MergeCommitSHA
		if writeErr != nil {
			state, sha = "unknown", ""
		}
		if err := setIntegrationRepo(ctx, q, row, state, sha); err != nil {
			return err
		}
		if writeErr != nil {
			return finishIntegrationOperation(ctx, q, op)
		}
		return nil
	})
	return errors.Join(err, writeErr)
}

// ReconcileIntegrationOperation 废止旧执行 revision 后在同一锁内只读检查 refs；不再发出 Git 写操作。
// prepared 从未获得执行权；requested/unknown 只有目标精确命中候选或原 SHA 才可确定结果。
func (s *Store) ReconcileIntegrationOperation(ctx context.Context, id string, expectedRevision int64, fence domain.SchedulerFence, clock func() time.Time, observe func(context.Context, domain.WorkflowRepoKey) (string, error)) (domain.IntegrationOperation, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var op domain.IntegrationOperation
	err := s.inSchedulerTx(ctx, "reconcile integration operation", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, fence, clock(), true); err != nil {
			return err
		}
		row, err := q.GetIntegrationOperation(ctx, id)
		if err != nil {
			return err
		}
		op = integrationOperationFromRow(row)
		if op.Revision != expectedRevision {
			return domain.ErrArtifactStale
		}
		if op.State == "applied" || op.State == "rejected" || op.State == "partial" {
			return nil
		}
		entries, err := q.GetIntegrationRepoPreviews(ctx, op.PlanID)
		if err != nil {
			return err
		}
		var request domain.IntegrationApplyRequest
		if err := json.Unmarshal([]byte(op.RequestJSON), &request); err != nil {
			return err
		}
		rows, err := q.ListIntegrationRepoOperations(ctx, id)
		if err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		for _, repo := range rows {
			if repo.State == "applied" || repo.State == "rejected" {
				continue
			}
			state, sha := "rejected", ""
			if repo.State != "prepared" {
				state = "unknown"
				if observed, err := observe(bounded, domain.WorkflowRepoKey(repo.RepoKey)); err == nil && domain.ValidGitSHA(observed) {
					sha = observed
					for _, e := range entries {
						if e.RepoKey == repo.RepoKey {
							expected := e.ExpectedTargetSha
							if op.Action == "push" {
								for _, target := range request.PushTargets {
									if string(target.RepoKey) == repo.RepoKey {
										expected = target.ExpectedRemoteSHA
									}
								}
							}
							if sha == e.MergeCommitSha {
								state = "applied"
							} else if sha == expected {
								state = "rejected"
							}
						}
					}
				}
			}
			if err := setIntegrationRepo(ctx, q, repo, state, sha); err != nil {
				return err
			}
		}
		if err := finishIntegrationOperation(ctx, q, op); err != nil {
			return err
		}
		updated, err := q.GetIntegrationOperation(ctx, id)
		op = integrationOperationFromRow(updated)
		return err
	})
	return op, err
}

// validateIntegrationOperation 旧调用即使仍持有批准，也不能跨越恢复后的 revision 或 scheduler 任期。
func validateIntegrationOperation(ctx context.Context, q *gen.Queries, op domain.IntegrationOperation, now time.Time) error {
	if err := schedulerFence(ctx, q, op.Fence, now, true); err != nil {
		return err
	}
	row, err := q.GetIntegrationOperation(ctx, op.ID)
	if err != nil {
		return err
	}
	current := integrationOperationFromRow(row)
	if current != op || current.State != "requested" {
		return domain.ErrArtifactStale
	}
	return nil
}

// setIntegrationRepo 所有逐仓状态迁移都比较旧状态和 revision。
func setIntegrationRepo(ctx context.Context, q *gen.Queries, row gen.WorkflowIntegrationRepoOperation, state, sha string) error {
	n, err := q.SetIntegrationRepoOperation(ctx, gen.SetIntegrationRepoOperationParams{OperationID: row.OperationID, RepoKey: row.RepoKey, ExpectedRevision: row.Revision, ExpectedState: row.State, State: state, ObservedSha: sha})
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrArtifactStale
	}
	return nil
}

// finishIntegrationOperation 由逐仓事实派生汇总；任何未确定副作用优先显示 unknown。
func finishIntegrationOperation(ctx context.Context, q *gen.Queries, op domain.IntegrationOperation) error {
	rows, err := q.ListIntegrationRepoOperations(ctx, op.ID)
	if err != nil {
		return err
	}
	applied, unknown := 0, false
	for _, r := range rows {
		if r.State == "applied" {
			applied++
		}
		if r.State == "requested" || r.State == "unknown" {
			unknown = true
		}
	}
	state := "rejected"
	if applied == len(rows) && len(rows) > 0 {
		state = "applied"
	} else if applied > 0 {
		state = "partial"
	}
	if unknown {
		state = "unknown"
	}
	n, err := q.SetIntegrationOperation(ctx, gen.SetIntegrationOperationParams{ID: op.ID, ExpectedRevision: op.Revision, State: state})
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrArtifactStale
	}
	return nil
}

// GetIntegrationOperation 返回持久结果，不能凭读取结果重新获得执行权。
func (s *Store) GetIntegrationOperation(ctx context.Context, id string) (domain.IntegrationOperation, bool, error) {
	r, err := s.qr.GetIntegrationOperation(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IntegrationOperation{}, false, nil
	}
	return integrationOperationFromRow(r), err == nil, err
}

// ListIntegrationRepoOperations 返回逐仓事实用于恢复和界面展示。
func (s *Store) ListIntegrationRepoOperations(ctx context.Context, id string) ([]domain.IntegrationRepoOperation, error) {
	rows, err := s.qr.ListIntegrationRepoOperations(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]domain.IntegrationRepoOperation, 0, len(rows))
	for _, r := range rows {
		result = append(result, domain.IntegrationRepoOperation{OperationID: r.OperationID, RepoKey: domain.WorkflowRepoKey(r.RepoKey), State: r.State, ObservedSHA: r.ObservedSha, Revision: r.Revision})
	}
	return result, nil
}

func integrationOperationFromRow(r gen.WorkflowIntegrationOperation) domain.IntegrationOperation {
	return domain.IntegrationOperation{ID: r.ID, PlanID: r.PlanID, WorkflowID: domain.WorkflowID(r.WorkflowID), Action: r.Action, RequestDigest: r.RequestDigest, RequestJSON: r.RequestJson, ApprovalRef: r.ApprovalRef, Fence: domain.SchedulerFence{OwnerID: r.OwnerID, Epoch: r.Epoch}, State: r.State, Revision: r.Revision, CreatedAt: r.CreatedAt}
}
