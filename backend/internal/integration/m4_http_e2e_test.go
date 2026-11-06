package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// m4CheckHTTPRefusals 删除精确 URI/正文或混合身份检查，会让拒绝请求提前消费同一批准。
func m4CheckHTTPRefusals(t *testing.T, f *m2ffFixture, p domain.IntegrationPlan, req domain.IntegrationApplyRequest, raw []byte, approval string) {
	t.Helper()
	base := "/workflows/" + string(p.WorkflowID)
	path := base + "/integrations/" + p.ID + "/apply"
	for _, suffix := range []string{"apply", "push"} {
		r := f.do(http.MethodPost, base+"/integrations/"+p.ID+"/"+suffix, string(raw), nil)
		if r.Status != http.StatusForbidden {
			t.Fatalf("unapproved %s: %d %s", suffix, r.Status, r.Raw)
		}
	}
	headers := map[string]string{principal.HeaderOperatorApproval: approval, principal.HeaderAgentCapability: ""}
	r := f.do(http.MethodPost, path, string(raw), headers)
	if r.Status != http.StatusForbidden {
		t.Fatalf("mixed empty capability: %d %s", r.Status, r.Raw)
	}
	delete(headers, principal.HeaderAgentCapability)
	for _, change := range []struct{ path, body string }{{path + "?retry=1", string(raw)}, {path, string(raw) + " "}, {base + "/integrations/" + p.ID + "/push", string(raw)}} {
		r = f.do(http.MethodPost, change.path, change.body, headers)
		if r.Status != http.StatusForbidden {
			t.Fatalf("changed approval target: %d %s", r.Status, r.Raw)
		}
	}
	if a, ok := f.approvals.Get(approval); !ok || a.ConsumedAt != nil {
		t.Fatal("rejected request consumed valid approval")
	}
	other := m3Create(t, f, "m4-other", 1)
	r = f.asOperator(http.MethodGet, "/workflows/"+string(other)+"/integrations/"+p.ID, "")
	if r.Status != http.StatusNotFound {
		t.Fatalf("cross-workflow plan: %d %s", r.Status, r.Raw)
	}
}

// m4CheckHTTPProjection 证明 HTTP 重放无第二次 Git 意图，并检查摘要来源和隐藏字段。
func m4CheckHTTPProjection(t *testing.T, f *m2ffFixture, p domain.IntegrationPlan, req domain.IntegrationApplyRequest, raw []byte, approval string) {
	t.Helper()
	base := "/workflows/" + string(p.WorkflowID)
	path := base + "/integrations/" + p.ID + "/apply"
	r := f.do(http.MethodPost, path, string(raw), map[string]string{principal.HeaderOperatorApproval: approval})
	if r.Status != http.StatusOK || f.text(r.Body, "operation", "state") != "applied" {
		t.Fatalf("HTTP replay: %d %s", r.Status, r.Raw)
	}
	operation := m4HTTP[controllers.IntegrationOperationResponse](t, f, http.MethodGet, base+"/integration-operations/"+req.IdempotencyKey, nil)
	if len(operation.Repositories) != 2 || operation.Operation.State != "applied" {
		t.Fatalf("operation projection=%+v", operation)
	}
	summary := m4HTTP[controllers.WorkflowCoordinationResponse](t, f, http.MethodGet, base+"/coordination-summary", nil)
	if len(summary.Coordination.Tasks) != 2 || len(summary.Quality.Commits) != 2 || len(summary.Quality.Artifacts) != 4 || len(summary.Quality.Operations) != 1 {
		t.Fatalf("incomplete summary=%+v", summary)
	}
	var task domain.CoordinationTaskView
	for _, candidate := range summary.Coordination.Tasks {
		if candidate.SessionID != "" {
			task = candidate
		}
	}
	if task.SessionID == "" || task.SessionStatus == "unknown" || !task.LeaseValid {
		t.Fatalf("missing current session/lease=%+v", task)
	}
	for _, a := range summary.Quality.Artifacts {
		if a.Generation != "" || a.Repo.RepoPath != "" || a.Repo.GitCommonDir != "" {
			t.Fatal("internal artifact identity leaked")
		}
	}
	for _, route := range []string{"quality-summary", "coordination-summary", "quality-policies"} {
		r = f.asOperator(http.MethodGet, base+"/"+route, "")
		if r.Status != http.StatusOK {
			t.Fatalf("summary: %d %s", r.Status, r.Raw)
		}
		for _, hidden := range []string{`"generation"`, `"repoPath"`, `"worktreePath"`, `"gitCommonDir"`, `"approvalRef"`, `"requestJSON"`, `"log"`, `"executable"`, `"args"`, `"env"`} {
			if strings.Contains(r.Raw, hidden) {
				t.Fatalf("%s leaked %s", route, hidden)
			}
		}
	}
	r = f.do(http.MethodGet, base+"/quality-summary", "", nil)
	if r.Status != http.StatusForbidden {
		t.Fatalf("unauthenticated summary: %d", r.Status)
	}
	events, err := f.store.EventsAfter(context.Background(), 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		var payload map[string]any
		if json.Unmarshal(event.Payload, &payload) != nil || payload["workflowId"] != string(p.WorkflowID) {
			continue
		}
		kind, _ := payload["kind"].(string)
		seen[kind] = true
		for _, secret := range []string{`"generation"`, `"leaseToken"`, `"repoPath"`, `"worktreePath"`, `"requestJSON"`, `"log"`, `"executable"`} {
			if strings.Contains(string(event.Payload), secret) {
				t.Fatalf("CDC leaked %s", secret)
			}
		}
	}
	for _, kind := range []string{"artifact", "path_reservation", "quality_gate", "quality_review", "task_commit", "integration_plan", "integration_operation"} {
		if !seen[kind] {
			t.Fatalf("missing CDC invalidation %s", kind)
		}
	}
}
