package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM4ReviewIdentityStalenessAndBoundedFix 删除独立身份、回执绑定或计数事务将破坏拒绝/重放/两轮封顶断言。
func TestM4ReviewIdentityStalenessAndBoundedFix(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	a := x.artifacts[0]
	if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	actor := domain.QualityReviewActor{OperatorRef: "verified-test-operator"}
	policy := domain.GatePolicy{ID: "test", Revision: 1, ValidFor: time.Hour, MaxFixes: 2, Rules: []domain.GateRule{{ID: "unit", Required: true}}}
	if _, err := x.s.SetWorkflowQualityPolicy(ctx, a.WorkflowID, policy, 0, actor.OperatorRef, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	r := domain.QualityReview{ID: "review-0", ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyRevision: 1, TargetSHA: a.Repo.BaseSHA, Actor: actor, Fence: x.ad.Fence}
	r.PolicyDigest = policy.Digest()
	self := r
	self.Actor = domain.QualityReviewActor{AttemptID: a.AttemptID, SessionID: a.SessionID, Generation: a.Generation}
	if _, err := x.s.BeginQualityReview(ctx, self, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("self review: %v", err)
	}
	findings := []domain.QualityFinding{{Path: "src/problem.go", Line: 1, Priority: 1, Reason: "必须修复的测试问题"}}
	for i := 0; i < 3; i++ {
		if i > 0 {
			a.ID = fmt.Sprintf("artifact-round-%d", i)
			a.ArtifactRevision++
			a.Git.WorkspaceDigest = fmt.Sprintf("%064d", i)
			a.Fingerprint = a.Digest()
			if _, _, err := x.s.SaveArtifact(x.context(t), a, x.ad.Fence, x.now); err != nil {
				t.Fatal(err)
			}
			r.ArtifactID, r.ArtifactFingerprint = a.ID, a.Fingerprint
		}
		r.ID = fmt.Sprintf("review-%d", i)
		started, err := x.s.BeginQualityReview(ctx, r, x.now)
		if err != nil {
			t.Fatal(err)
		}
		result, err := x.s.FinishQualityReview(ctx, r.ID, started.Revision, actor, domain.VerdictChangesRequested, findings, 2, x.ad.Fence, x.now)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if result.State != "changes_requested" || result.FixRequestID == "" {
				t.Fatalf("fix=%+v", result)
			}
			req, found, err := x.s.GetWorkflowTaskRequest(ctx, a.WorkflowID, result.FixRequestID)
			if err != nil || !found || req.State != domain.WorkflowTaskRequestStatePending || req.RequesterTaskID != a.TaskID {
				t.Fatalf("pending fix=%+v %v", req, err)
			}
		} else if result.State != "blocked" || result.FixRequestID != "" {
			t.Fatalf("exceeded bound=%+v", result)
		}
		replay, err := x.s.FinishQualityReview(ctx, r.ID, started.Revision, actor, domain.VerdictChangesRequested, findings, 2, x.ad.Fence, x.now)
		if err != nil || replay.Revision != result.Revision || replay.FixRequestID != result.FixRequestID {
			t.Fatalf("replay=%+v %v", replay, err)
		}
		if i == 0 {
			duplicate := r
			duplicate.ID = "duplicate-fix"
			if _, err := x.s.BeginQualityReview(ctx, duplicate, x.now); err != nil {
				t.Fatal(err)
			}
			got, err := x.s.FinishQualityReview(ctx, duplicate.ID, 1, actor, domain.VerdictChangesRequested, findings, 2, x.ad.Fence, x.now)
			if err != nil || got.State != "blocked" || got.FixRequestID != "" {
				t.Fatalf("same revision repeated fix: %+v %v", got, err)
			}
		}
	}
	if got := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_requests`); got != 2 {
		t.Fatalf("fixes=%d", got)
	}
	if got := qualityCount(t, x.db, `SELECT count(*) FROM workflow_tasks`); got != 1 {
		t.Fatalf("review materialized %d tasks", got)
	}
	r.ID = "stale-review"
	if _, err := x.s.BeginQualityReview(ctx, r, x.now); err != nil {
		t.Fatal(err)
	}
	changed := a
	changed.ID = "new-version"
	changed.ArtifactRevision++
	changed.Git.WorkspaceDigest = strings.Repeat("e", 64)
	changed.Fingerprint = changed.Digest()
	if _, _, err := x.s.SaveArtifact(x.context(t), changed, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.FinishQualityReview(ctx, r.ID, 1, actor, domain.VerdictApproved, nil, 2, x.ad.Fence, x.now); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("stale approval: %v", err)
	}
}
