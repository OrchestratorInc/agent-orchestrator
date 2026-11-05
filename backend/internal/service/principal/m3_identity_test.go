package principal_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestSessionCapabilityDerivesAttempt 删除从 Session binding 派生 Attempt 的赋值会红。
func TestSessionCapabilityDerivesAttempt(t *testing.T) {
	s := identityFixture()
	req := loopbackRequest()
	req.Header.Set("X-AO-Session-Id", string(s.session.ID))
	req.Header.Set(principal.HeaderAgentCapability, agentToken)
	caller, err := principal.StoreResolver(s, s, testAuthority{}).Resolve(context.Background(), principal.RequestFacts{
		Request: req, WorkflowID: "workflow-1", AttemptID: "attempt-1",
	})
	if err != nil || !caller.LeaseProven || caller.AttemptID != s.attempt.ID {
		t.Fatalf("服务端身份未派生: caller=%+v err=%v", caller, err)
	}
	permit := caller.ExecutionPermit
	if permit == nil || permit.Generation != s.binding.ControllerGeneration || permit.Fence != s.leader.Fence ||
		permit.LeaseToken != s.attempt.LeaseToken || permit.BindingRevision != s.binding.Revision || !permit.AttemptUpdatedAt.Equal(s.attempt.UpdatedAt) {
		t.Fatal("身份校验丢失了写事务栅栏快照")
	}
	wire, err := json.Marshal(caller)
	if err != nil || strings.Contains(string(wire), s.attempt.LeaseToken) {
		t.Fatal("内部执行许可泄漏到 JSON")
	}
}

// TestRawLeaseRejectedBeforeApproval 删除 Resolve 的旧 lease header 拒绝分支会红。
func TestRawLeaseRejectedBeforeApproval(t *testing.T) {
	for _, header := range []string{principal.HeaderAttemptLease, "X-AO-Workflow-Lease"} {
		t.Run(header, func(t *testing.T) {
			f := newApprovalFixture(t)
			digest := principal.PayloadDigest(nil)
			a, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "")
			if err != nil {
				t.Fatal(err)
			}
			approveAndConfirm(t, f, a.ID, a.Code, digest)
			req := approvalRequest(a.ID, http.MethodPost, approvalBindPath, "")
			req.Header.Set(header, "")
			_, err = principal.NewResolver(nil, nil, f.trust).Resolve(context.Background(), principal.RequestFacts{Request: req})
			if !errors.Is(err, principal.ErrSecurityUpgradeRequired) {
				t.Fatal("旧 lease header 获得 Operator")
			}
			if state, _, _ := f.store.State(a.ID); state != principal.ApprovalStateConfirmed {
				t.Fatalf("拒绝前消费了审批: %s", state)
			}
			s := identityFixture()
			req.Header.Set(header, "old-secret")
			req.Header.Set(principal.HeaderSessionID, string(s.session.ID))
			req.Header.Set(principal.HeaderAgentCapability, agentToken)
			_, err = principal.StoreResolver(s, s, testAuthority{}).Resolve(context.Background(), principal.RequestFacts{Request: req})
			if !errors.Is(err, principal.ErrSecurityUpgradeRequired) || s.bindingReads != 0 {
				t.Fatal("新旧凭据混用没有立即拒绝")
			}
		})
	}
}

// TestSessionIdentityRejectsStaleFacts 删除对应身份一致性或白名单判断会让该场景获权。
func TestSessionIdentityRejectsStaleFacts(t *testing.T) {
	for name, mutate := range map[string]func(*identityStore){
		"binding_missing":    func(s *identityStore) { s.binding = domain.WorkflowAttemptBinding{} },
		"binding_uncertain":  func(s *identityStore) { s.binding.State = domain.WorkflowBindingStateRecoveryUncertain },
		"binding_session":    func(s *identityStore) { id := domain.SessionID("other"); s.binding.SessionID = &id },
		"generation":         func(s *identityStore) { s.session.Metadata.ControllerGeneration = "new-generation" },
		"attempt_owner":      func(s *identityStore) { s.attempt.TaskID = "other" },
		"attempt_session":    func(s *identityStore) { id := domain.SessionID("other"); s.attempt.SessionID = &id },
		"attempt_unknown":    func(s *identityStore) { s.attempt.State = domain.WorkflowAttemptStateUnknown },
		"admission_missing":  func(s *identityStore) { s.admissions = nil },
		"admission_released": func(s *identityStore) { s.admissions[0].State = domain.AdmissionReleased },
		"admission_owner":    func(s *identityStore) { s.admissions[0].TaskID = "other" },
		"old_epoch":          func(s *identityStore) { s.leader.Fence.Epoch++ },
		"leader_unrecovered": func(s *identityStore) { s.leader.Recovered = false },
		"leader_expired":     func(s *identityStore) { s.leader.LeaseUntil = time.Now().Add(-time.Minute) },
		"read_failure":       func(s *identityStore) { s.err = errors.New("read failed") },
	} {
		t.Run(name, func(t *testing.T) {
			s := identityFixture()
			mutate(s)
			req := loopbackRequest()
			req.Header.Set("X-AO-Session-Id", string(s.session.ID))
			req.Header.Set(principal.HeaderAgentCapability, agentToken)
			_, err := principal.StoreResolver(s, s, testAuthority{}).Resolve(context.Background(), principal.RequestFacts{Request: req, AgentSessionID: s.session.ID, WorkflowID: "workflow-1", AttemptID: "attempt-1"})
			if err == nil {
				t.Fatal("不可信持久事实获得 Attempt 权限")
			}
		})
	}
}

// TestCapabilityMustPrecedeBinding 删除 capability 拒绝分支会读取绑定；不能用外部 Attempt 选择受害 Session。
func TestCapabilityMustPrecedeBinding(t *testing.T) {
	s := identityFixture()
	req := loopbackRequest()
	req.Header.Set("X-AO-Session-Id", string(s.session.ID))
	req.Header.Set(principal.HeaderAgentCapability, "other-session-capability")
	_, err := principal.StoreResolver(s, s, testAuthority{}).Resolve(context.Background(), principal.RequestFacts{Request: req, AgentSessionID: s.session.ID, AttemptID: "attempt-1"})
	if !errors.Is(err, principal.ErrCapabilityInvalid) || s.bindingReads != 0 {
		t.Fatalf("capability 拒绝次序: %v, binding reads=%d", err, s.bindingReads)
	}
}

// TestCapabilityRotationCannotMixGenerations 删除验证前后 Session 快照一致性判断会红。
func TestCapabilityRotationCannotMixGenerations(t *testing.T) {
	s := identityFixture()
	before := s.session
	s.firstSession = &before
	s.session.Metadata.ControllerGeneration = "generation-2"
	s.session.Metadata.BrowserCapabilityVerifier = browserStyleVerifier(s.session.ID, "rotated-token")
	req := loopbackRequest()
	req.Header.Set(principal.HeaderSessionID, string(s.session.ID))
	req.Header.Set(principal.HeaderAgentCapability, "rotated-token")
	_, err := principal.StoreResolver(s, s, testAuthority{}).Resolve(context.Background(), principal.RequestFacts{Request: req, AttemptID: "attempt-1"})
	if !errors.Is(err, principal.ErrCapabilityInvalid) || s.bindingReads != 0 {
		t.Fatalf("capability 轮换混用了旧 generation: %v, binding reads=%d", err, s.bindingReads)
	}
}

// TestM3OperatorOperationsRequireOneExactApproval 删除任何新增路由或正文/重放检查会红。
func TestM3OperatorOperationsRequireOneExactApproval(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/workflows"},
		{http.MethodPost, "/api/v1/workflows/recover-expired"},
		{http.MethodPost, "/api/v1/workflows/w/activate"},
		{http.MethodPost, "/api/v1/workflows/w/tasks/t/claim"},
		{http.MethodPost, "/api/v1/workflows/w/tasks/t/retry"},
		{http.MethodPost, "/api/v1/workflows/w/attempts/a/start"},
		{http.MethodPost, "/api/v1/workflows/w/attempts/a/settle"},
		{http.MethodPut, "/api/v1/scheduler/policy"},
		{http.MethodPut, "/api/v1/scheduler/budget"},
		{http.MethodPut, "/api/v1/workflows/w/budget"},
		{http.MethodPut, "/api/v1/workflows/w/tasks/t/budget"},
		{http.MethodPost, "/api/v1/workflows/w/pause"},
		{http.MethodPost, "/api/v1/workflows/w/resume-scheduling"},
		{http.MethodPost, "/api/v1/workflows/w/task-requests/r/materialize"},
	} {
		t.Run(route.path, func(t *testing.T) {
			f := newApprovalFixture(t)
			body := `{"expectedRevision":1}`
			digest := principal.PayloadDigest([]byte(body))
			a, err := f.store.CreateWithPayload(route.method, route.path, digest, body)
			if err != nil {
				t.Fatal(err)
			}
			approveAndConfirm(t, f, a.ID, a.Code, digest)
			resolver := principal.NewResolver(nil, nil, f.trust)
			wrong := approvalRequest(a.ID, route.method, route.path, body+" ")
			if _, err = resolver.FromRequest(wrong, ""); err == nil {
				t.Fatal("不同正文获权")
			}
			// 正文不匹配不能消费原批准，原始字节仍只允许成功一次。
			req := approvalRequest(a.ID, route.method, route.path, body)
			if caller, err := resolver.FromRequest(req, ""); err != nil || !caller.IsOperator() {
				t.Fatalf("审批未授权: %v", err)
			}
			if _, err = resolver.FromRequest(req, ""); err == nil {
				t.Fatal("审批可重放")
			}
		})
	}
}
