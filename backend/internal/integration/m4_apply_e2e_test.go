package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	integrationservice "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// m4ConsentStub 仅替代 OS 交互，其余一次性批准验证和消费使用产品实现。
type m4ConsentStub struct{}

func (m4ConsentStub) Verify(context.Context, principal.Approval) error { return nil }

// m4Approve 经过真实正文绑定和一次性批准状态机，仅将原生交互替换为显式测试确认。
func m4Approve(t *testing.T, s *principal.ApprovalStore, path string, raw []byte) string {
	t.Helper()
	digest := principal.PayloadDigest(raw)
	a, err := s.CreateWithPayload("POST", path, digest, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyAndConfirm(context.Background(), a.ID, a.Code, digest, m4ConsentStub{}); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// m4CheckIntegrationApply 删除精确批准或唯一意图会改变目标 ref，删除只读恢复会丢失部分成功或重复执行。
func m4CheckIntegrationApply(t *testing.T, f *m2ffFixture, svc *integrationservice.Service, plan domain.IntegrationPlan, entries []domain.IntegrationRepoPreview, artifacts []domain.ArtifactSnapshot, fence domain.SchedulerFence, partial bool) {
	t.Helper()
	ctx := context.Background()
	svc.Approvals = f.approvals
	req := domain.IntegrationApplyRequest{IdempotencyKey: "real-apply", ExpectedRevision: plan.Revision, PlanDigest: plan.PlanDigest, Entries: entries}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	path := integrationservice.IntegrationPath(plan.WorkflowID, plan.ID, "apply")
	if _, err := svc.Apply(ctx, plan.WorkflowID, plan.ID, raw, "", fence); err == nil {
		t.Fatal("checked-out target accepted")
	}
	for _, a := range artifacts {
		m2smGit(t, a.Repo.RepoPath, "checkout", "--detach")
	}
	if _, err := svc.Apply(ctx, plan.WorkflowID, plan.ID, raw, "", fence); err == nil {
		t.Fatal("missing approval accepted")
	}
	wrong := m4Approve(t, svc.Approvals, path, append(append([]byte(nil), raw...), byte(' ')))
	if _, err := svc.Apply(ctx, plan.WorkflowID, plan.ID, raw, wrong, fence); err == nil {
		t.Fatal("wrong exact body accepted")
	}
	approval := m4Approve(t, svc.Approvals, path, raw)
	// apply 批准不能消费于 push，即使正文完全相同。
	if _, err := svc.Approvals.Consume(approval, "POST", integrationservice.IntegrationPath(plan.WorkflowID, plan.ID, "push"), principal.PayloadDigest(raw)); err == nil {
		t.Fatal("merge approval authorized push")
	}
	if partial {
		original := svc.Store.(integrationservice.OperationStore)
		svc.Store = &m4IntegrationLoss{OperationStore: original, failKey: entries[1].RepoKey}
		if _, err := svc.Apply(ctx, plan.WorkflowID, plan.ID, raw, approval, fence); err == nil {
			t.Fatal("lost operation receipt passed")
		}
		svc.Store = original
		before, found, err := original.GetIntegrationOperation(ctx, req.IdempotencyKey)
		if err != nil || !found || before.State != "requested" {
			t.Fatalf("before restart=%+v %v", before, err)
		}
		svc.Approvals = principal.NewApprovalStore(principal.ApprovalStoreOptions{Clock: f.m3.clock.Now})
		op := m4HTTP[controllers.IntegrationOperationResponse](t, f, http.MethodPost, "/workflows/"+string(plan.WorkflowID)+"/integration-operations/"+req.IdempotencyKey+"/reconcile", controllers.QualityReconcileRequest{ExpectedRevision: before.Revision}).Operation
		if op.State != "partial" {
			t.Fatalf("partial recovery=%+v", op)
		}
	} else {
		m4CheckHTTPRefusals(t, f, plan, req, raw, approval)
		var wg sync.WaitGroup
		results := make([]domain.IntegrationOperation, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := f.do(http.MethodPost, strings.TrimPrefix(path, "/api/v1"), string(raw), map[string]string{principal.HeaderOperatorApproval: approval})
				if r.Status == http.StatusOK {
					var out controllers.IntegrationOperationResponse
					errs[i] = json.Unmarshal([]byte(r.Raw), &out)
					results[i] = out.Operation
				} else if r.Status == http.StatusConflict {
					errs[i] = domain.ErrDeliveryBlocked
				} else {
					errs[i] = errors.New(r.Raw)
				}
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil && !errors.Is(err, domain.ErrArtifactStale) && !errors.Is(err, domain.ErrDeliveryBlocked) {
				t.Fatal(err)
			}
		}
		if results[0].State != "applied" && results[1].State != "applied" {
			t.Fatalf("double operator=%+v", results)
		}
	}
	for i, a := range artifacts {
		want := entries[i].MergeCommitSHA
		if partial && i == 1 {
			want = entries[i].ExpectedTargetSHA
		}
		if got := m2smGit(t, a.Repo.WorktreePath, "rev-parse", a.Repo.TargetRef); got != want+"\n" {
			t.Fatalf("target[%d]=%q want=%s", i, got, want)
		}
	}
	replay, err := svc.Apply(ctx, plan.WorkflowID, plan.ID, raw, "", fence)
	wantState := "applied"
	if partial {
		wantState = "partial"
	}
	if err != nil || replay.State != wantState {
		t.Fatalf("read-only replay=%+v %v", replay, err)
	}
	if !partial {
		m4CheckHTTPProjection(t, f, plan, req, raw, approval)
		m4CheckLocalPush(t, f, svc, plan, entries, artifacts, fence)
	}
}

// m4IntegrationLoss 在第二仓写前终止并丢失汇总回执，保留第一仓的真实 ref 与逐仓证据。
type m4IntegrationLoss struct {
	integrationservice.OperationStore
	failKey domain.WorkflowRepoKey
}

// WithIntegrationWrite 只在指定第二仓阻断写入，其余仓仍执行真实事务及 Git。
func (s *m4IntegrationLoss) WithIntegrationWrite(ctx context.Context, op domain.IntegrationOperation, p domain.IntegrationRepoPreview, clock func() time.Time, write func(context.Context) error) error {
	if p.RepoKey == s.failKey {
		return errors.New("injected second repository failure")
	}
	return s.OperationStore.WithIntegrationWrite(ctx, op, p, clock, write)
}
func (s *m4IntegrationLoss) ReconcileIntegrationOperation(context.Context, string, int64, domain.SchedulerFence, func() time.Time, func(context.Context, domain.WorkflowRepoKey) (string, error)) (domain.IntegrationOperation, error) {
	return domain.IntegrationOperation{}, errors.New("injected restart before reconciliation")
}
