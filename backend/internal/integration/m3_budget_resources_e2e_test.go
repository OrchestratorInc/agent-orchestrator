package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
)

// TestM3ResourceAndThreeLevelBudgetGates 删除资源闭锁或任一级预算余额检查，会在解除门禁之前产生真实 Session。
func TestM3ResourceAndThreeLevelBudgetGates(t *testing.T) {
	f := newM3Fixture(t)
	ctx := context.Background()
	wid := m3Create(t, f, "budget", 1)
	tasks, err := f.store.ListWorkflowTasks(ctx, wid, "", 100, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks: %v", err)
	}
	task := tasks[0]
	claim := func(key string) m2ffResp {
		return f.asOperator(http.MethodPost, "/workflows/"+string(wid)+"/tasks/"+string(task.ID)+"/claim", fmt.Sprintf(`{"expectedRevision":%d,"workerId":"manual","idempotencyKey":%q}`, task.Revision, key))
	}
	f.m3.resources.load(90, nil)
	if r := claim("cpu-block"); r.Status != http.StatusConflict || f.text(r.Body, "code") != "WORKFLOW_ADMISSION_DENIED" {
		t.Fatalf("CPU: %d %s", r.Status, r.Raw)
	}
	f.m3.clock.advance(scheduler.SampleInterval)
	unavailable := errors.New("offline resource sample")
	f.m3.resources.load(20, unavailable)
	if _, err := f.m3.scheduler.Tick(ctx); !errors.Is(err, unavailable) {
		t.Fatalf("sampler failure: %v", err)
	}
	if len(f.sessionsFor()) != 0 {
		t.Fatal("resource failure started a session")
	}
	f.m3.resources.load(20, nil)
	for i := 0; i < 3; i++ {
		f.m3.clock.advance(scheduler.SampleInterval)
		if _, err := f.m3.scheduler.MaintainLeadership(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.m3.scheduler.RefreshResources(ctx); err != nil {
			t.Fatal(err)
		}
	}
	f.m3.resources.memory(512 << 20)
	f.m3.clock.advance(scheduler.SampleInterval)
	if r := claim("ram-block"); r.Status != http.StatusConflict || f.text(r.Body, "code") != "WORKFLOW_ADMISSION_DENIED" {
		t.Fatalf("RAM: %d %s", r.Status, r.Raw)
	}
	f.m3.resources.memory(12 << 30)
	for i := 0; i < 3; i++ {
		f.m3.clock.advance(scheduler.SampleInterval)
		if _, err := f.m3.scheduler.MaintainLeadership(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.m3.scheduler.RefreshResources(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []struct{ id, path string }{
		{"global", "/scheduler/budget"},
		{"workflow", "/workflows/" + string(wid) + "/budget"},
		{"task", "/workflows/" + string(wid) + "/tasks/" + string(task.ID) + "/budget"},
	} {
		body := m3BudgetBody(t, scope.id, domain.BudgetHardAdmission, 0)
		policy := f.asOperator(http.MethodPut, scope.path, body)
		if policy.Status != http.StatusOK {
			t.Fatalf("%s policy: %d %s", scope.id, policy.Status, policy.Raw)
		}
		if r := claim(scope.id + "-blocked"); r.Status != http.StatusConflict || f.text(r.Body, "code") != "WORKFLOW_ADMISSION_DENIED" {
			t.Fatalf("%s gate: %d %s", scope.id, r.Status, r.Raw)
		}
		if len(f.sessionsFor()) != 0 {
			t.Fatalf("%s gate allowed spawn", scope.id)
		}
		policy = f.asOperator(http.MethodPut, scope.path, m3BudgetBody(t, scope.id, domain.BudgetObserve, f.number(policy.Body, "policy", "revision")))
		if policy.Status != http.StatusOK {
			t.Fatalf("observe policy: %d %s", policy.Status, policy.Raw)
		}
	}
	dispatched := m3Dispatch(t, f)
	if len(dispatched) != 1 {
		t.Fatalf("recovered resources: dispatches=%d", len(dispatched))
	}
	sid := *f.binding(dispatched[0].AttemptID).SessionID
	agent := f.asAgent(f.issueAgentCapability(sid), "", dispatched[0].AttemptID)
	if r := f.do(http.MethodPut, "/scheduler/budget", m3BudgetBody(t, "global", domain.BudgetObserve, 2), agent); r.Status != http.StatusForbidden {
		t.Fatalf("agent changed budget: %d %s", r.Status, r.Raw)
	}
	m3AssertUnknownUsageReplay(t, f, sid)
}

// m3BudgetBody 用明确批准的预留金额触发余额门禁，不把未知预估偷换成零。
func m3BudgetBody(t *testing.T, id, mode string, revision int64) string {
	amount := int64(10)
	return m3JSON(t, controllers.PutBudgetPolicyRequest{
		OperatorChangeRequest: controllers.OperatorChangeRequest{ExpectedRevision: revision, IdempotencyKey: fmt.Sprintf("%s-%s-%d", id, mode, revision), Reason: "TEST_BUDGET"},
		ID:                    id, Currency: "USD", Mode: mode, Period: domain.BudgetLifetime, LimitNanos: 5,
		ReservationNanos: &amount, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "offline-human-bound",
	})
}

// m3AssertUnknownUsageReplay 删除 canonical event 去重或 nil 费用传播，会使事件翻倍或 UNKNOWN 变零。
func m3AssertUnknownUsageReplay(t *testing.T, f *m2ffFixture, sid domain.SessionID) {
	t.Helper()
	ctx := context.Background()
	now := f.m3.clock.Now()
	binding, err := f.store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{SessionID: sid, Harness: domain.HarnessCodex, NativeRootID: "offline-root", InitialModelID: "offline-model", State: domain.UsageBindingActive, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.store.InsertUsageSource(ctx, domain.UsageSourceRecord{BindingID: binding.ID, Kind: domain.UsageSourceCodexRollout, NativeSessionID: "offline-root", ArtifactPath: filepath.Join(f.dataDir, "fake-usage.jsonl"), FileIdentity: "offline-file", State: domain.UsageSourcePending, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	input, output := int64(5), int64(3)
	event := domain.ModelUsageEvent{ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "openai", BillingProviderSource: domain.UsageBillingProviderObserved, ModelID: "offline-model", MeasurementKind: domain.UsageMeasurementNativeReported, SourceEventKey: "same-event", CreatedAt: now, Tokens: domain.UsageTokenMetrics{InputTokens: &input, OutputTokens: &output}, ProviderUsageJSON: `{"last_token_usage":{}}`}
	for i := int64(0); i < 2; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		expected := now
		if i > 0 {
			expected = now.Add(time.Duration(i-1) * time.Second)
		}
		if err := f.store.ApplyUsageChunk(ctx, source.ID, i*10, expected, domain.SourceCursorState{ByteOffset: (i + 1) * 10, State: domain.UsageSourceActive, UpdatedAt: at}, []domain.ModelUsageEvent{event}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.m3.budget.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.m3.budget.Snapshot(ctx, "global")
	if err != nil || snapshot.EventCount != 1 || snapshot.CostNanos != nil || snapshot.RemainingNanos != nil || snapshot.Quality != domain.BudgetQualityUnknown {
		t.Fatalf("unknown canonical usage: %+v err=%v", snapshot, err)
	}
	r := f.asOperator(http.MethodGet, "/scheduler/budget", "")
	if r.Status != http.StatusOK {
		t.Fatalf("budget HTTP: %d %s", r.Status, r.Raw)
	}
	snapshots, ok := r.Body["snapshots"].([]any)
	if !ok || len(snapshots) != 1 {
		t.Fatalf("budget snapshot shape: %s", r.Raw)
	}
	row := snapshots[0].(map[string]any)
	if row["costNanos"] != nil || row["quality"] != domain.BudgetQualityUnknown {
		t.Fatalf("HTTP invented zero cost: %s", r.Raw)
	}
}
