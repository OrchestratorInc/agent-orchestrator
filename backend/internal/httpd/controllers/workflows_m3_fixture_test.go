package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	budgets "github.com/aoagents/agent-orchestrator/backend/internal/service/budget"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
)

// jsonInt 为审批正文输出精确 revision 数字。
func jsonInt(v int64) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func newControlServer(t *testing.T, deps httpd.APIDeps) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// approvedWorkflowDeps 对真实 HTTP 审批注入测试 OS 验证器，不使用 token 或回环自动授权。
func approvedWorkflowDeps(deps httpd.APIDeps) httpd.APIDeps {
	approvals := principal.NewApprovalStore(principal.ApprovalStoreOptions{})
	deps.OperatorApprovals, deps.OperatorConsent = approvals, &recordingConsent{}
	deps.WorkflowPrincipals = principal.NewResolver(nil, nil, principal.ApprovalOperatorTrust{Approvals: approvals})
	return deps
}

// doWorkflowRequest 为每次测试操作独立申请并确认精确正文批准，随后发送原始字节。
func doWorkflowRequest(t *testing.T, srv *httptest.Server, method, path, body string) ([]byte, int, http.Header) {
	t.Helper()
	header := ""
	if method != http.MethodGet && principal.IsOperatorOperation(method, path) {
		raw, err := json.Marshal(map[string]string{"operationMethod": method, "operationPath": path, "payloadDigest": principal.PayloadDigest([]byte(body)), "payload": body})
		if err != nil {
			t.Fatal(err)
		}
		status, response := doJSONAsAnAnonymousPeer(t, srv, http.MethodPost, "/api/v1/operator/approvals", string(raw))
		if status != http.StatusCreated {
			t.Fatalf("create approval=%d %s", status, response)
		}
		var approval approvalDTO
		mustJSON(t, response, &approval)
		(&approvalEnv{srv: srv}).confirmApproval(t, approval, principal.PayloadDigest([]byte(body)))
		header = approval.Approval.ApprovalID
	}
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set(principal.HeaderOperatorApproval, header)
	}
	response, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw, response.StatusCode, response.Header
}

func (f *fakeWorkflowService) GetSchedulerLeader(context.Context) (domain.SchedulerLeader, error) {
	return domain.SchedulerLeader{Fence: domain.SchedulerFence{OwnerID: "test", Epoch: 1}, Recovered: true, LeaseUntil: time.Now().Add(time.Hour)}, nil
}

// LoadExecutionPermit 保留测试服务实际提供的内部租约，HTTP 请求无法更换它。
func (f *fakeWorkflowService) LoadExecutionPermit(_ context.Context, id domain.WorkflowAttemptID, fence domain.SchedulerFence, _ time.Time) (domain.ExecutionPermit, error) {
	a := f.attempts[0]
	return domain.ExecutionPermit{AttemptID: id, WorkflowID: a.WorkflowID, TaskID: a.TaskID, Fence: fence, LeaseToken: a.LeaseToken}, nil
}

func (f *fakeWorkflowService) RecoverExpiredWorkflowsAuthorized(context.Context, domain.PolicyChange) (int64, bool, error) {
	return f.recovered, true, nil
}

// newM3ControlEnv 在真实 SQLite 和服务上启用控制路由；不启动调度循环、Provider 或原生验证。
func newM3ControlEnv(t *testing.T) *approvalEnv {
	t.Helper()
	e := newApprovalEnv(t)
	e.deps.Scheduler = scheduler.New(scheduler.Deps{Store: e.store, Tasks: e.store, Clock: e.clock.Now})
	e.deps.Budget = budgets.New(e.store, e.clock.Now)
	e.deps.WorkflowMaterializer = e.deps.Workflows.(controllers.TaskRequestMaterializer)
	e.deps.WorkflowMaterializations = e.store
	e.srv = newControlServer(t, e.deps)
	return e
}

// boundAgentServer 用已准入的实际 Session 证明 Agent；只替换 capability 验证器字段。
func boundAgentServer(t *testing.T, e *approvalEnv) (*httptest.Server, map[string]string) {
	t.Helper()
	if status, raw := e.approvedAction(t, http.MethodPost, e.bindPath(), `{"idempotencyKey":"bind-m3"}`); status != http.StatusOK {
		t.Fatalf("bind: %d %s", status, raw)
	}
	b, found, err := e.store.GetWorkflowAttemptBinding(context.Background(), domain.WorkflowAttemptID(e.attemptID))
	if err != nil || !found || b.SessionID == nil {
		t.Fatalf("bound session: %v %v", found, err)
	}
	const token = "m3-test-agent"
	sessions := verifierOverride{Sessions: e.store, verifier: approvalVerifier(*b.SessionID, token)}
	resolver := principal.NewResolver(sessions, principal.CapabilityVerifier{Sessions: sessions, Authority: browserAuthorityFixture{}}, principal.ApprovalOperatorTrust{Approvals: e.approvals}).WithSessionAttemptProver(principal.StoreSessionAttemptProver{Store: e.store, Clock: e.clock.Now})
	e.deps.WorkflowPrincipals = resolver
	return e.serverWith(t, resolver), map[string]string{principal.HeaderAgentCapability: token, principal.HeaderSessionID: string(*b.SessionID)}
}
