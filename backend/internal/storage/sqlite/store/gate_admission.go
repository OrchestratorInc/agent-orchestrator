package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// checkPredecessorQuality 复用 M3 Claim writer，启用质量策略时不能仅靠 M1 succeeded 绕过交付证据。
func checkPredecessorQuality(ctx context.Context, q *gen.Queries, w domain.WorkflowID, task domain.WorkflowTaskID, kind domain.WorkflowTaskKind, now time.Time) error {
	policyRow, err := q.GetWorkflowQualityPolicy(ctx, string(w))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := qualityPolicyFromRow(policyRow)
	if err != nil {
		return err
	}
	edges, err := q.ListWorkflowEdges(ctx, w)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		if edge.ToTaskID != task {
			continue
		}
		scopes, err := q.ListWorkflowTaskRepositories(ctx, gen.ListWorkflowTaskRepositoriesParams{WorkflowID: w, TaskID: edge.FromTaskID})
		if err != nil {
			return err
		}
		for _, scope := range scopes {
			a, err := q.LatestTaskRepoArtifact(ctx, gen.LatestTaskRepoArtifactParams{WorkflowID: w, TaskID: edge.FromTaskID, RepoKey: string(scope.RepoKey)})
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrDeliveryBlocked
			}
			if err != nil {
				return err
			}
			if a.State != "verified" {
				return domain.ErrDeliveryBlocked
			}
			gate, err := q.LatestGateRun(ctx, a.ID)
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrDeliveryBlocked
			}
			if err != nil {
				return err
			}
			if gate.State != "PASS" || gate.ArtifactFingerprint != a.Fingerprint || gate.PolicyDigest != p.Digest || !gate.ExpiresAt.After(now) {
				return domain.ErrDeliveryBlocked
			}
			// Review 消费 Gate 后的工件，不能等待自身尚未产生的批准。
			if kind == domain.WorkflowTaskKindReview {
				continue
			}
			approved, err := q.HasApprovedQualityReview(ctx, gen.HasApprovedQualityReviewParams{ArtifactID: a.ID, PolicyDigest: p.Digest})
			if err != nil {
				return err
			}
			if !approved {
				return domain.ErrDeliveryBlocked
			}
		}
	}
	return nil
}
