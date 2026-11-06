package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// m4RequestRepair 删除真实退出码、独立 Review 或 M3 物化审批会破坏失败后有界修复链。
func m4RequestRepair(t *testing.T, f *m2ffFixture, a domain.ArtifactSnapshot) {
	t.Helper()
	base := "/workflows/" + string(a.WorkflowID)
	artifactPath := base + "/artifacts/" + a.ID
	m4CheckPolicyReadback(t, f, a.WorkflowID)
	gate := m4HTTP[controllers.QualityGateResponse](t, f, http.MethodPost, artifactPath+"/gates", controllers.QualityActionRequest{IdempotencyKey: "repair-gate"})
	if gate.State != "FAIL" {
		t.Fatalf("invalid revision passed gate: %+v", gate)
	}
	agent := f.asAgent(f.issueAgentCapability(a.SessionID), "", a.AttemptID)
	if got := f.do(http.MethodPost, artifactPath+"/reviews", `{"idempotencyKey":"self-review-http"}`, agent); got.Status != http.StatusConflict {
		t.Fatalf("author self review: %d %s", got.Status, got.Raw)
	}
	review := m4HTTP[controllers.QualityReviewResponse](t, f, http.MethodPost, artifactPath+"/reviews", controllers.QualityActionRequest{IdempotencyKey: "repair-review"}).Review
	review = m4HTTP[controllers.QualityReviewResponse](t, f, http.MethodPost, base+"/reviews/"+review.ID+"/finish", controllers.QualityReviewFinishRequest{ExpectedRevision: review.Revision, Verdict: domain.VerdictChangesRequested,
		Findings: []domain.QualityFinding{{Path: "delivery.txt", Line: 1, Priority: 1, Reason: "冻结文件未达到交付版本"}}}).Review
	if review.State != "changes_requested" || review.FixRequestID == "" {
		t.Fatalf("missing bounded fix: %+v", review)
	}
	request, found, err := f.store.GetWorkflowTaskRequest(context.Background(), a.WorkflowID, review.FixRequestID)
	if err != nil || !found || request.State != domain.WorkflowTaskRequestStatePending {
		t.Fatalf("fix request=%+v found=%v err=%v", request, found, err)
	}
	path := base + "/task-requests/" + request.ID
	body := fmt.Sprintf(`{"approve":true,"expectedRevision":%d}`, request.Revision)
	if got := f.do(http.MethodPost, path+"/decide", body, agent); got.Status != http.StatusForbidden {
		t.Fatalf("agent approved its own fix: %d %s", got.Status, got.Raw)
	}
	if got := f.asOperator(http.MethodPost, path+"/decide", body); got.Status != http.StatusAccepted {
		t.Fatalf("approve fix: %d %s", got.Status, got.Raw)
	}
	request, _, err = f.store.GetWorkflowTaskRequest(context.Background(), a.WorkflowID, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	body = fmt.Sprintf(`{"requestSha":%q,"expectedRequestRevision":%d,"expectedWorkflowRevision":%d,"idempotencyKey":"materialize-review-fix","reason":"REVIEW_FIX"}`, request.RequestSHA, request.Revision, request.WorkflowRevision)
	created := f.asOperator(http.MethodPost, path+"/materialize", body)
	if created.Status != http.StatusCreated || len(f.sessionsFor()) != 1 {
		t.Fatalf("fix materialization bypassed admission: %d %s", created.Status, created.Raw)
	}
	scopes, err := f.store.ListWorkflowTaskRepositories(context.Background(), a.WorkflowID, domain.WorkflowTaskID(f.text(created.Body, "materialization", "taskId")))
	if err != nil || len(scopes) != 2 {
		t.Fatalf("fix scopes=%+v err=%v", scopes, err)
	}
	for _, scope := range scopes {
		if len(scope.AllowedPaths) != 1 || scope.AllowedPaths[0] != "delivery.txt" {
			t.Fatalf("fix widened scope: %+v", scope)
		}
	}
}
