package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestM2OperatorApprovalBindCreatesTheBinding is the official-client bind path: request, confirm,
// submit, and the durable row exists afterwards.
func TestM2OperatorApprovalBindCreatesTheBinding(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	body := `{"idempotencyKey":"bind-approval-1"}`
	status, raw := env.approvedAction(t, http.MethodPost, env.bindPath(), body)
	if status != http.StatusOK {
		t.Fatalf("bind = %d, want 200; body=%s", status, raw)
	}
	binding, found, err := env.store.GetWorkflowAttemptBinding(
		context.Background(), domain.WorkflowAttemptID(env.attemptID))
	if err != nil || !found {
		t.Fatalf("binding row: found=%v err=%v", found, err)
	}
	if binding.SessionID == nil {
		t.Fatalf("binding has no session: %+v", binding)
	}
	if env.gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want exactly 1", env.gateway.SpawnCalls())
	}
}

// TestM2OperatorApprovalResolve is the resolve verb through the same proof.
func TestM2OperatorApprovalResolve(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	// Move the attempt to UNKNOWN, which is the only state Resolve may leave.
	if _, err := env.store.RecoverExpiredWorkflowAttempts(
		context.Background(), time.Now().UTC().Add(2*time.Hour)); err != nil {
		t.Fatalf("recover expired: %v", err)
	}
	task, found, err := env.store.GetWorkflowTask(
		context.Background(), domain.WorkflowID(env.workflowID), domain.WorkflowTaskID(env.taskID))
	if err != nil || !found {
		t.Fatalf("reload task: found=%v err=%v", found, err)
	}
	body := `{"outcome":"succeeded","expectedTaskRevision":` +
		jsonInt(task.Revision) + `,"idempotencyKey":"resolve-approval-1"}`
	status, raw := env.approvedAction(t, http.MethodPost, env.resolvePath(), body)
	if status != http.StatusOK {
		t.Fatalf("resolve = %d, want 200; body=%s", status, raw)
	}
	attempt, found, err := env.store.GetWorkflowAttempt(
		context.Background(), domain.WorkflowAttemptID(env.attemptID))
	if err != nil || !found {
		t.Fatalf("reload attempt: found=%v err=%v", found, err)
	}
	if attempt.State != domain.WorkflowAttemptStateSucceeded {
		t.Fatalf("attempt state = %q, want succeeded", attempt.State)
	}
}

// TestM2OperatorApprovalReconcileAndResume covers the two recovery verbs.
func TestM2OperatorApprovalReconcileAndResume(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	if status, raw := env.approvedAction(t, http.MethodPost, env.bindPath(),
		`{"idempotencyKey":"bind-for-recovery"}`); status != http.StatusOK {
		t.Fatalf("bind = %d; body=%s", status, raw)
	}
	for _, verb := range []string{"reconcile", "resume"} {
		path := "/api/v1/workflows/" + env.workflowID + "/attempts/" + env.attemptID + "/" + verb
		status, raw := env.approvedAction(t, http.MethodPost, path, "")
		if status != http.StatusOK {
			t.Fatalf("%s = %d, want 200; body=%s", verb, status, raw)
		}
	}
	if env.gateway.ResumeCalls() != 1 {
		t.Fatalf("resume calls = %d, want 1", env.gateway.ResumeCalls())
	}
	if env.gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1: recovery must never spawn again", env.gateway.SpawnCalls())
	}
}

// TestM2OperatorApprovalDecideWritesExactlyOneAuditRow proves the decide verb works through the
// approval proof AND that the audit trail records the confirming credential's fingerprint only.
func TestM2OperatorApprovalDecideWritesExactlyOneAuditRow(t *testing.T) {
	t.Parallel()
	env := newApprovalEnv(t)
	ctx := context.Background()
	// A pending request raised from a bound attempt.
	if status, raw := env.approvedAction(t, http.MethodPost, env.bindPath(),
		`{"idempotencyKey":"bind-for-decide"}`); status != http.StatusOK {
		t.Fatalf("bind = %d; body=%s", status, raw)
	}
	bound, found, err := env.store.GetWorkflowAttemptBinding(ctx, domain.WorkflowAttemptID(env.attemptID))
	if err != nil || !found || bound.SessionID == nil {
		t.Fatalf("binding: found=%v err=%v", found, err)
	}
	leader, err := env.store.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	permit, err := env.store.LoadExecutionPermit(ctx, domain.WorkflowAttemptID(env.attemptID), leader.Fence, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	raised, _, err := env.store.CreateWorkflowTaskRequest(ports.WithExecutionPermit(ctx, permit), domain.WorkflowTaskRequest{
		ID: "req-1", WorkflowID: domain.WorkflowID(env.workflowID), RequesterTaskID: domain.WorkflowTaskID(env.taskID),
		RequestKind: domain.WorkflowTaskRequestKindIncreaseBudget,
		RequestJSON: `{"credits":10}`, RequestSHA: collaboration.RequestSHA(`{"credits":10}`),
		WorkflowRevision: 2,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("seed task request: %v", err)
	}
	body := `{"approve":true,"expectedRevision":` + jsonInt(raised.Revision) + `}`
	path := "/api/v1/workflows/" + env.workflowID + "/task-requests/req-1/decide"
	status, raw := env.approvedAction(t, http.MethodPost, path, body)
	if status != http.StatusAccepted && status != http.StatusOK {
		t.Fatalf("decide = %d, want 200/202; body=%s", status, raw)
	}

	rows, err := env.store.ListWorkflowAudits(ctx, env.workflowID, 100, 0)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Decision != "decide_task_request" {
		t.Fatalf("decision = %q", row.Decision)
	}
	if row.ActorKind != string(principal.KindOperator) {
		t.Fatalf("actor kind = %q, want operator", row.ActorKind)
	}
	// The recorded credential is the CONFIRMING token's fingerprint — the human gesture — and it
	// is a fingerprint, never the token.
	if row.TokenFingerprint != principal.NativeConsentIdentity {
		t.Fatalf("fingerprint = %q, want the confirming credential's", row.TokenFingerprint)
	}
	if row.TokenFingerprint == env.token || strings.Contains(row.TokenFingerprint, env.token) {
		t.Fatal("the audit row carries the operator token")
	}
}
