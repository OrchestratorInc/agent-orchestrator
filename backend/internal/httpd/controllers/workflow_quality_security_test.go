package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	integrationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualityreview"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/taskcommit"
)

// qualityReplayStore 只有只读重放路径可用；任何额外访问会失败，不能偷偷进入 Git 服务。
type qualityReplayStore struct {
	QualityReader
	operation domain.IntegrationOperation
	reads     int
}

func (s *qualityReplayStore) GetIntegrationOperation(context.Context, string) (domain.IntegrationOperation, bool, error) {
	s.reads++
	return s.operation, true, nil
}
func (s *qualityReplayStore) ListIntegrationRepoOperations(context.Context, string) ([]domain.IntegrationRepoOperation, error) {
	return []domain.IntegrationRepoOperation{}, nil
}

type qualityConsent struct{}

func (qualityConsent) Verify(context.Context, principal.Approval) error { return nil }

// TestQualityReplayLocalExpiryFence 删除运输来源或有效期检查，会让持有已消费 ID 的请求跨边界读取。
func TestQualityReplayLocalExpiryFence(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	approvals := principal.NewApprovalStore(principal.ApprovalStoreOptions{Clock: clock, TTL: time.Minute})
	path := "/api/v1/workflows/w/integrations/p/apply"
	raw := `{"idempotencyKey":"operation","expectedRevision":1,"planDigest":"digest","entries":[]}`
	digest := principal.PayloadDigest([]byte(raw))
	a, err := approvals.CreateWithPayload(http.MethodPost, path, digest, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = approvals.VerifyAndConfirm(context.Background(), a.ID, a.Code, digest, qualityConsent{}); err != nil {
		t.Fatal(err)
	}
	if _, err = approvals.Consume(a.ID, http.MethodPost, path, digest); err != nil {
		t.Fatal(err)
	}
	store := &qualityReplayStore{operation: domain.IntegrationOperation{ID: "operation", WorkflowID: "w", PlanID: "p", Action: "apply", RequestDigest: digest, State: "applied", Revision: 2}}
	c := &WorkflowQualityController{Store: store, Workflows: &WorkflowsController{Now: clock, ExecutionFence: func(context.Context) (domain.SchedulerFence, error) { panic("replay requested execution fence") }}, Artifacts: &artifact.Service{}, Gates: &qualitygate.Service{}, Reviews: &qualityreview.Service{}, Commits: &taskcommit.Service{}, Integrations: &integrationsvc.Service{Approvals: approvals}}
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) { c.Register(r) })
	request := func(peer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(raw))
		r.RemoteAddr = peer
		r.Header.Set(principal.HeaderOperatorApproval, a.ID)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request("192.0.2.1:3200"); w.Code != http.StatusForbidden || store.reads != 0 {
		t.Fatalf("nonlocal replay=%d reads=%d", w.Code, store.reads)
	}
	w := request("127.0.0.1:3200")
	var result IntegrationOperationResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Operation.State != "applied" || store.reads != 1 {
		t.Fatalf("local replay=%d %s", w.Code, w.Body.String())
	}
	now = now.Add(time.Minute)
	if w := request("127.0.0.1:3200"); w.Code != http.StatusForbidden || store.reads != 1 {
		t.Fatalf("expired replay=%d reads=%d", w.Code, store.reads)
	}
}
