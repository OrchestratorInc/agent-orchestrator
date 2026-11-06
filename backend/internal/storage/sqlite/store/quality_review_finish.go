package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// FinishQualityReview 原子写入审查回执、结构化交接和受限修复请求；绝不结算 Attempt 或物化 Task。
func (s *Store) FinishQualityReview(ctx context.Context, id string, revision int64, actor domain.QualityReviewActor, verdict domain.ReviewVerdict, findings []domain.QualityFinding, maxFixes int64, fence domain.SchedulerFence, now time.Time) (domain.QualityReview, error) {
	if maxFixes < 1 || maxFixes > 3 || len(findings) > 100 || (verdict != domain.VerdictNone && verdict != domain.VerdictApproved && verdict != domain.VerdictChangesRequested) {
		return domain.QualityReview{}, domain.ErrDeliveryBlocked
	}
	for _, f := range findings {
		path, ok := domain.CanonicalWorkflowPath(f.Path, false)
		if !ok || path != f.Path || path == "" || f.Line < 1 || f.Priority < 0 || f.Priority > 3 || strings.TrimSpace(f.Reason) == "" || len(f.Reason) > 2048 || (verdict == domain.VerdictApproved && f.Priority <= 1) {
			return domain.QualityReview{}, domain.ErrDeliveryBlocked
		}
	}
	encoded, _ := json.Marshal(struct {
		Verdict  domain.ReviewVerdict
		Findings []domain.QualityFinding
		MaxFixes int64
	}{verdict, findings, maxFixes})
	if len(encoded) > 32768 {
		return domain.QualityReview{}, domain.ErrDeliveryBlocked
	}
	digest := reviewDigest(encoded)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.QualityReview
	err := s.inSchedulerTx(ctx, "finish independent review", func(q *gen.Queries) error {
		row, err := q.GetQualityReview(ctx, id)
		if err != nil {
			return err
		}
		r, err := qualityReviewFromRow(row)
		if err != nil {
			return err
		}
		if r.Actor != actor || r.Fence != fence {
			return domain.ErrDeliveryBlocked
		}
		a, err := reviewArtifact(ctx, q, r, now)
		if err != nil {
			return err
		}
		policyRow, err := q.GetWorkflowQualityPolicy(ctx, string(a.WorkflowID))
		if err != nil {
			return err
		}
		policy, err := qualityPolicyFromRow(policyRow)
		if err != nil {
			return err
		}
		if policy.Policy.MaxFixes != maxFixes {
			return domain.ErrDeliveryBlocked
		}
		if r.State != "running" {
			if row.RequestDigest != digest {
				return domain.ErrArtifactStale
			}
			result = r
			return nil
		}
		if r.Revision != revision {
			return domain.ErrArtifactStale
		}
		pending, err := q.HasPendingTaskCommit(ctx, string(a.WorkflowID))
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		state := "unverified"
		if verdict == domain.VerdictApproved {
			gate, err := q.LatestGateRun(ctx, a.ID)
			if err != nil {
				return domain.ErrDeliveryBlocked
			}
			if gate.State != "PASS" || gate.ArtifactFingerprint != a.Fingerprint || gate.PolicyDigest != r.PolicyDigest || !gate.ExpiresAt.After(now) {
				return domain.ErrDeliveryBlocked
			}
			state = "approved"
		}
		if verdict == domain.VerdictChangesRequested {
			state = "changes_requested"
			count, err := q.CountQualityFixRequests(ctx, row.RootTaskID)
			if err != nil {
				return err
			}
			duplicate, err := q.CountArtifactFixRequests(ctx, gen.CountArtifactFixRequestsParams{AttemptID: string(a.AttemptID), ArtifactRevision: a.ArtifactRevision})
			if err != nil {
				return err
			}
			if count >= maxFixes || duplicate > 0 {
				state = "blocked"
			} else {
				r.FixRequestID = "review-fix-" + r.ID
				if err := insertReviewFixRequest(ctx, q, a, r.FixRequestID, now); err != nil {
					return err
				}
			}
		}
		findingsJSON, _ := json.Marshal(findings)
		n, err := q.FinishQualityReview(ctx, gen.FinishQualityReviewParams{State: state, Verdict: string(verdict), FindingsJson: string(findingsJSON), RequestDigest: digest, FixRequestID: r.FixRequestID, UpdatedAt: now.UTC(), ID: id, ExpectedRevision: revision, OwnerID: fence.OwnerID, Epoch: fence.Epoch})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrArtifactStale
		}
		r.State, r.Verdict, r.Findings, r.Revision = state, verdict, findings, revision+1
		if err := insertReviewHandoff(ctx, q, a, r, now); err != nil {
			return err
		}
		result = r
		return nil
	})
	return result, err
}

// insertReviewFixRequest 从持久原 Task scope 生成请求；只产生 pending，独立 Operator 仍须批准和物化。
func insertReviewFixRequest(ctx context.Context, q *gen.Queries, a domain.ArtifactSnapshot, id string, now time.Time) error {
	w, err := q.GetWorkflow(ctx, a.WorkflowID)
	if err != nil {
		return err
	}
	if w.State != domain.WorkflowStateActive && w.State != domain.WorkflowStatePaused {
		return domain.ErrDeliveryBlocked
	}
	scopes, err := q.ListWorkflowTaskRepositories(ctx, gen.ListWorkflowTaskRepositoriesParams{WorkflowID: a.WorkflowID, TaskID: a.TaskID})
	if err != nil {
		return err
	}
	repos := make([]map[string]any, 0, len(scopes))
	for _, s := range scopes {
		var paths []string
		if err := json.Unmarshal([]byte(s.AllowedPathsJson), &paths); err != nil {
			return err
		}
		repos = append(repos, map[string]any{"repoKey": s.RepoKey, "allowedPaths": paths})
	}
	payload, _ := json.Marshal(map[string]any{"task": map[string]any{"kind": domain.WorkflowTaskKindDevelopment, "title": "修复独立审查发现", "brief": "依据审查工件 " + a.ID + " 完成范围内修复；重新生成工件并执行 Gate/Review。", "maxAttempts": 1}, "repositories": repos})
	if len(payload) > domain.MaxWorkflowTaskRequestJSONBytes {
		return domain.ErrDeliveryBlocked
	}
	n, err := q.InsertWorkflowTaskRequest(ctx, gen.InsertWorkflowTaskRequestParams{ID: id, WorkflowID: a.WorkflowID, RequesterTaskID: a.TaskID, RequestKind: domain.WorkflowTaskRequestKindCreateTask, RequestJson: string(payload), RequestSha: reviewDigest(payload), State: domain.WorkflowTaskRequestStatePending, WorkflowRevision: w.Revision, Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()})
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrWorkflowTaskRequestConflict
	}
	return nil
}

// insertReviewHandoff 复用 M2 的 result/question/handoff，不把模型判断发布成 Gate 退出码。
func insertReviewHandoff(ctx context.Context, q *gen.Queries, a domain.ArtifactSnapshot, r domain.QualityReview, now time.Time) error {
	count, err := q.CountOpenWorkflowMessages(ctx, a.WorkflowID)
	if err != nil {
		return err
	}
	if count >= domain.MaxOpenWorkflowMessages {
		return domain.ErrWorkflowMessageQueueFull
	}
	kind := domain.WorkflowMessageKindResult
	if r.State == "changes_requested" {
		kind = domain.WorkflowMessageKindHandoff
	}
	if r.State == "blocked" || r.State == "unverified" {
		kind = domain.WorkflowMessageKindQuestion
	}
	body, _ := json.Marshal(map[string]string{"reviewId": r.ID, "artifactId": a.ID, "state": r.State, "fixRequestId": r.FixRequestID})
	from, sender := a.TaskID, domain.WorkflowMessageSenderHuman
	var target *domain.WorkflowTaskID
	var session *domain.SessionID
	var senderTask *domain.WorkflowTaskID
	if r.Actor.OperatorRef == "" {
		p, err := loadExecutionPermit(ctx, q, r.Actor.AttemptID)
		if err != nil {
			return err
		}
		from, sender = p.TaskID, domain.WorkflowMessageSenderAgent
		target = &a.TaskID
		session = &p.SessionID
		senderTask = &p.TaskID
	}
	targetID := ""
	if target != nil {
		targetID = string(*target)
	}
	n, err := q.InsertWorkflowMessage(ctx, gen.InsertWorkflowMessageParams{ID: "review-handoff-" + r.ID, WorkflowID: a.WorkflowID, FromTaskID: from, ToTaskID: target, SenderSessionID: session, SenderKind: sender, SenderTaskID: senderTask, Kind: kind, DeliveryState: domain.WorkflowMessageDeliveryPersisted, Body: string(body), PayloadRef: "", ClientMessageID: "review-handoff-" + r.ID, PayloadSha: reviewDigest([]byte(strings.Join([]string{string(kind), string(body), "", targetID}, "\x00"))), Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()})
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrWorkflowMessageConflict
	}
	return nil
}
