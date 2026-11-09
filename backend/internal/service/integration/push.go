package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Push 只支持可信宿主显式登记的本地 bare 模拟仓库；无登记即闭锁，不能借批准传入任意路径。
// 对象传递和唯一 ref CAS 复用同一逐仓台账与恢复协议，apply 的批准永远不能消费于此。
func (s *Service) Push(ctx context.Context, w domain.WorkflowID, planID string, raw []byte, approval string, fence domain.SchedulerFence) (domain.IntegrationOperation, error) {
	store, ok := s.Store.(OperationStore)
	if !ok || s.Clock == nil || s.Gates == nil || len(raw) > principal.MaxApprovalPayloadBytes {
		return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
	}
	var req domain.IntegrationApplyRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.IdempotencyKey == "" {
		return domain.IntegrationOperation{}, domain.ErrArtifactInvalid
	}
	digest := principal.PayloadDigest(raw)
	old, found, err := store.GetIntegrationOperation(ctx, req.IdempotencyKey)
	if err != nil {
		return old, err
	}
	if found {
		if old.Action != "push" || old.WorkflowID != w || old.PlanID != planID || old.RequestDigest != digest {
			return old, domain.ErrArtifactStale
		}
		return old, nil
	}
	plan, found, err := store.GetIntegrationPlan(ctx, planID)
	if err != nil {
		return domain.IntegrationOperation{}, err
	}
	if !found || plan.WorkflowID != w || plan.Status != "clean" || plan.Revision != req.ExpectedRevision || plan.PlanDigest != req.PlanDigest || plan.Digest(req.Entries) != plan.PlanDigest || len(req.Entries) == 0 || len(req.PushTargets) != len(req.Entries) {
		return domain.IntegrationOperation{}, domain.ErrArtifactStale
	}
	artifacts := make([]domain.ArtifactSnapshot, 0, len(req.Entries))
	seen := make(map[string]bool)
	for i, entry := range req.Entries {
		target := req.PushTargets[i]
		if target.RepoKey != entry.RepoKey || target.TargetRef != entry.TargetRef || target.CommitSHA != entry.MergeCommitSHA || seen[target.RemoteIdentity] {
			return domain.IntegrationOperation{}, domain.ErrArtifactStale
		}
		seen[target.RemoteIdentity] = true
		root, ok := s.BareRemotes[target.RemoteKey]
		if !ok {
			return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
		}
		sha, err := gitworktree.ObserveLocalPush(ctx, root, target)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if sha != target.ExpectedRemoteSHA {
			return domain.IntegrationOperation{}, domain.ErrArtifactStale
		}
		a, found, err := store.GetArtifact(ctx, entry.ArtifactID)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if !found {
			return domain.IntegrationOperation{}, domain.ErrArtifactStale
		}
		current, err := gitworktree.ObserveIntegrationTarget(ctx, a, entry)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if current != entry.MergeCommitSHA {
			return domain.IntegrationOperation{}, domain.ErrArtifactStale
		}
		c, found, err := store.GetArtifactTaskCommit(ctx, a.ID)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if !found {
			return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
		}
		g, found, err := store.GetGateRun(ctx, c.GateID)
		if err != nil {
			return domain.IntegrationOperation{}, err
		}
		if !found {
			return domain.IntegrationOperation{}, domain.ErrDeliveryBlocked
		}
		if err := s.Gates.RevalidatePolicy(ctx, a, g); err != nil {
			return domain.IntegrationOperation{}, err
		}
		artifacts = append(artifacts, a)
	}
	op := domain.IntegrationOperation{ID: req.IdempotencyKey, PlanID: planID, WorkflowID: w, Action: "push", RequestDigest: digest, Fence: fence}
	op, created, err := store.BeginIntegrationOperation(ctx, op, req, s.Clock(), func() (string, error) {
		return s.Approvals.Consume(approval, "POST", Path(w, planID, "push"), digest)
	})
	if err != nil || !created {
		return op, err
	}
	for i, entry := range req.Entries {
		if err = store.RequestIntegrationRepo(ctx, op, entry.RepoKey, s.Clock()); err != nil {
			break
		}
		target := req.PushTargets[i]
		err = gitworktree.ApplyLocalPush(ctx, artifacts[i], s.BareRemotes[target.RemoteKey], target, func(ctx context.Context, write func(context.Context) error) error {
			return store.WithIntegrationWrite(ctx, op, entry, s.Clock, write)
		})
		if err != nil {
			break
		}
	}
	result, recoveryErr := s.reconcileCurrent(ctx, w, op.ID, fence)
	return result, errors.Join(err, recoveryErr)
}
