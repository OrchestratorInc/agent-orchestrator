package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// OperationStore 将批准、逐仓领取、最终写锁和只读恢复接入已有 SQLite writer。
type OperationStore interface {
	Store
	GetIntegrationOperation(context.Context, string) (domain.IntegrationOperation, bool, error)
	BeginIntegrationOperation(context.Context, domain.IntegrationOperation, domain.IntegrationApplyRequest, time.Time, func() (string, error)) (domain.IntegrationOperation, bool, error)
	RequestIntegrationRepo(context.Context, domain.IntegrationOperation, domain.WorkflowRepoKey, time.Time) error
	WithIntegrationWrite(context.Context, domain.IntegrationOperation, domain.IntegrationRepoPreview, func() time.Time, func(context.Context) error) error
	ReconcileIntegrationOperation(context.Context, string, int64, domain.SchedulerFence, func() time.Time, func(context.Context, domain.WorkflowRepoKey) (string, error)) (domain.IntegrationOperation, error)
}

// Apply 精确绑定收到的正文与完整计划，先预检全部仓，再消费一次批准并按 RepoKey 顺序执行。
// 同 ID 重放仅返回原记录，恢复是独立的只读动作，不能让重放请求抢走仍有效的执行。
func (s *Service) Apply(ctx context.Context, workflowID domain.WorkflowID, planID string, raw []byte, approvalID string, fence domain.SchedulerFence) (domain.IntegrationOperation, error) {
	store, ok := s.Store.(OperationStore)
	if !ok || s.Clock == nil || s.Gates == nil || len(raw) > principal.MaxApprovalPayloadBytes {
		return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
	}
	var request domain.IntegrationApplyRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return domain.IntegrationOperation{}, domain.ErrArtifactInvalid
	}
	if decoder.Decode(new(any)) != io.EOF || request.IdempotencyKey == "" || len(request.PushTargets) != 0 {
		return domain.IntegrationOperation{}, domain.ErrArtifactInvalid
	}
	digest := principal.PayloadDigest(raw)
	old, found, err := store.GetIntegrationOperation(ctx, request.IdempotencyKey)
	if err != nil {
		return old, err
	}
	if found {
		if old.WorkflowID != workflowID || old.PlanID != planID || old.Action != "apply" || old.RequestDigest != digest {
			return old, domain.ErrArtifactStale
		}
		return old, nil
	}
	plan, found, err := store.GetIntegrationPlan(ctx, planID)
	if err != nil {
		return domain.IntegrationOperation{}, err
	}
	if !found || plan.WorkflowID != workflowID || plan.Status != "clean" || request.ExpectedRevision != plan.Revision || request.PlanDigest != plan.PlanDigest || plan.Digest(request.Entries) != plan.PlanDigest {
		return domain.IntegrationOperation{}, domain.ErrArtifactStale
	}
	artifacts := make(map[domain.WorkflowRepoKey]domain.ArtifactSnapshot, len(request.Entries))
	ids := make([]string, 0, len(request.Entries))
	for _, entry := range request.Entries {
		a, found, err := store.GetArtifact(ctx, entry.ArtifactID)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if !found {
			return domain.IntegrationOperation{}, domain.ErrArtifactStale
		}
		commit, found, err := store.GetArtifactTaskCommit(ctx, a.ID)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if !found {
			return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
		}
		gate, found, err := store.GetGateRun(ctx, commit.GateID)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if !found {
			return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
		}
		if err := s.Gates.RevalidatePolicy(ctx, a, gate); err != nil {
			return domain.IntegrationOperation{}, err
		}
		if err := gitworktree.CheckIntegrationTarget(ctx, a, entry); err != nil {
			return domain.IntegrationOperation{}, err
		}
		artifacts[entry.RepoKey] = a
		ids = append(ids, a.ID)
	}
	if err := store.ValidateIntegrationArtifacts(ctx, workflowID, ids, fence, s.Clock()); err != nil {
		return domain.IntegrationOperation{}, err
	}
	op := domain.IntegrationOperation{ID: request.IdempotencyKey, PlanID: plan.ID, WorkflowID: workflowID, Action: "apply", RequestDigest: digest, Fence: fence}
	op, created, err := store.BeginIntegrationOperation(ctx, op, request, s.Clock(), func() (string, error) {
		return s.Approvals.Consume(approvalID, "POST", Path(workflowID, planID, "apply"), digest)
	})
	if err != nil || !created {
		return op, err
	}
	for _, entry := range request.Entries {
		if err = store.RequestIntegrationRepo(ctx, op, entry.RepoKey, s.Clock()); err != nil {
			break
		}
		err = gitworktree.ApplyIntegrationTarget(ctx, artifacts[entry.RepoKey], entry, func(ctx context.Context, write func(context.Context) error) error {
			return store.WithIntegrationWrite(ctx, op, entry, s.Clock, write)
		})
		if err != nil {
			break
		}
	}
	result, recoveryErr := s.reconcileCurrent(ctx, workflowID, op.ID, fence)
	return result, errors.Join(err, recoveryErr)
}

// Path 同时用于操作批准和实际请求；不同 action 永远不共享批准。
func Path(workflowID domain.WorkflowID, planID, action string) string {
	return "/api/v1/workflows/" + url.PathEscape(string(workflowID)) + "/integrations/" + url.PathEscape(planID) + "/" + action
}

// reconcileCurrent 为 Apply/Push 收尾读取副作用回执后的 revision；外部批准必须调用显式 CAS 的 Reconcile。
func (s *Service) reconcileCurrent(ctx context.Context, workflowID domain.WorkflowID, id string, fence domain.SchedulerFence) (domain.IntegrationOperation, error) {
	store, ok := s.Store.(OperationStore)
	if !ok {
		return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
	}
	op, found, err := store.GetIntegrationOperation(ctx, id)
	if err != nil {
		return op, err
	}
	if !found {
		return op, domain.ErrArtifactStale
	}
	return s.Reconcile(ctx, workflowID, id, op.Revision, fence)
}

// Reconcile 只读取已绑定仓库的目标 ref；批准的 revision 必须在数据库锁内仍有效。
func (s *Service) Reconcile(ctx context.Context, workflowID domain.WorkflowID, id string, expectedRevision int64, fence domain.SchedulerFence) (domain.IntegrationOperation, error) {
	store, ok := s.Store.(OperationStore)
	if !ok || s.Clock == nil {
		return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
	}
	op, found, err := store.GetIntegrationOperation(ctx, id)
	if err != nil {
		return op, err
	}
	if !found || op.Revision != expectedRevision || op.WorkflowID != workflowID || (op.Action != "apply" && op.Action != "push") {
		return op, domain.ErrArtifactStale
	}
	entries, err := store.ListIntegrationRepoPreviews(ctx, op.PlanID)
	if err != nil {
		return op, err
	}
	var request domain.IntegrationApplyRequest
	if err := json.Unmarshal([]byte(op.RequestJSON), &request); err != nil {
		return op, err
	}
	artifacts := make(map[domain.WorkflowRepoKey]domain.ArtifactSnapshot, len(entries))
	for _, entry := range entries {
		a, found, err := store.GetArtifact(ctx, entry.ArtifactID)
		if err != nil {
			return op, err
		}
		if !found {
			return op, domain.ErrArtifactStale
		}
		artifacts[entry.RepoKey] = a
	}
	return store.ReconcileIntegrationOperation(ctx, id, expectedRevision, fence, s.Clock, func(ctx context.Context, key domain.WorkflowRepoKey) (string, error) {
		if op.Action == "push" {
			for _, target := range request.PushTargets {
				if target.RepoKey == key {
					return gitworktree.ObserveLocalPush(ctx, s.BareRemotes[target.RemoteKey], target)
				}
			}
			return "", domain.ErrArtifactStale
		}
		for _, entry := range entries {
			if entry.RepoKey == key {
				return gitworktree.ObserveIntegrationTarget(ctx, artifacts[key], entry)
			}
		}
		return "", domain.ErrArtifactStale
	})
}
