package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// deferredPlanRetirer 只注入外部停止结果，不修改 Session 或释放任何占位。
type deferredPlanRetirer struct {
	err           error
	calls         int
	failedSession domain.SessionID
}

func (r *deferredPlanRetirer) RetireWorkflowSession(_ context.Context, id domain.SessionID, _ string) error {
	r.calls++
	if id == r.failedSession {
		return r.err
	}
	return nil
}

// TestM5MaintenanceRetainsUnconfirmedResources 运行 daemon 的真实维护入口与持久恢复。
// 删除单项失败隔离或恢复时的终态跳过会阻塞维护；错误释放会使占位断言失败。
func TestM5MaintenanceRetainsUnconfirmedResources(t *testing.T) {
	ctx := context.Background()
	st := openDaemonTestStore(t)
	aid := seedWiringAttempt(t, st, "retirement")
	gateway := execution.NewFakeGateway().SpawnReadiness(ports.SessionReadinessReady).SpawnGeneration("gen-wiring").InterruptResult(ports.InterruptReceipt{Confirmed: true})
	materializeSession(t, st, gateway)
	initial := newM2ServicesWith(m2GatewayDeps{Store: st, Gateway: gateway, ExecutionFence: daemonExecutionFence(t, st)})
	initial.Capabilities.RegisterProbe(domain.HarnessCodex, wiringProbe{grade: domain.CapabilitySourceConfirmedRuntime, features: []domain.CapabilityFeature{domain.CapabilityFeatureStreaming, domain.CapabilityFeatureInterrupt}})
	bound, err := initial.Binder.BindAttempt(ctx, ports.SpawnRequest{WorkflowID: "wiring-workflow", TaskID: "wiring-task", AttemptID: aid, ProjectID: "wiring-proj", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop, _, err := initial.Interventions.Stop(ctx, intervention.StopRequest{WorkflowID: "wiring-workflow", TaskID: "wiring-task", AttemptID: aid, ClientMessageID: "retirement-stop"})
	if err != nil || !stop.Performed {
		t.Fatalf("stop: %+v %v", stop, err)
	}
	wf := newWorkflowService(st)
	def, err := wf.GetDefinition(ctx, "wiring-workflow")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.StartReplan(ctx, workflow.ReplanInput{WorkflowID: "wiring-workflow", StopInterventionID: stop.Intervention.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "maintenance-replan", AgentHarness: domain.HarnessCodex, Model: "offline"}); err != nil {
		t.Fatal(err)
	}
	// 第二个真实持久 job 必须在首项停止失败后仍被遍历，不提高容量或删除原占位。
	now := time.Now().UTC()
	second, _, err := wf.Create(ctx, "second-retirement", workflow.CreateInput{
		Title: "第二个收尾", Goal: "验证失败隔离", Repositories: []workflow.RepositoryInput{{RepoKey: "backend", ProjectID: "wiring-proj"}},
		Tasks: []workflow.TaskInput{{ID: "second-task", Kind: domain.WorkflowTaskKindDevelopment, Title: "第二项", AgentHarness: domain.HarnessCodex}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.Activate(ctx, second.ID, 1, "activate-second"); err != nil {
		t.Fatal(err)
	}
	admission := sqlitetest.NewAdmissionFixture(t, st, func() time.Time { return now })
	until := now.Add(time.Hour)
	secondAttempt, _, err := admission.Claim(ctx, domain.WorkflowAttempt{ID: "zz-retirement", WorkflowID: second.ID, TaskID: "second-task", WorkerID: "worker-second", LeaseToken: "lease-second", LeaseUntil: &until, State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now}, 2, "claim-second", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Binder.BindAttempt(ctx, ports.SpawnRequest{WorkflowID: second.ID, TaskID: secondAttempt.TaskID, AttemptID: secondAttempt.ID, ProjectID: "wiring-proj", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat}, nil); err != nil {
		t.Fatal(err)
	}
	secondStop, _, err := initial.Interventions.Stop(ctx, intervention.StopRequest{WorkflowID: second.ID, TaskID: secondAttempt.TaskID, AttemptID: secondAttempt.ID, ClientMessageID: "second-stop"})
	if err != nil || !secondStop.Performed {
		t.Fatalf("第二项停止: %+v %v", secondStop, err)
	}
	secondDef, err := wf.GetDefinition(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wf.StartReplan(ctx, workflow.ReplanInput{WorkflowID: second.ID, StopInterventionID: secondStop.Intervention.ID, ExpectedRevision: secondDef.Workflow.Revision, IdempotencyKey: "second-replan", AgentHarness: domain.HarnessCodex, Model: "offline"}); err != nil {
		t.Fatal(err)
	}
	retirer := &deferredPlanRetirer{err: errors.New("stop outcome unavailable"), failedSession: *bound.Binding.SessionID}
	old, err := st.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 每轮重建服务及 Leader，旧进程的内存不能提供恢复结果；原数据库与外部执行观察保留。
	for round := 0; round < 2; round++ {
		now := old.LeaseUntil.Add(time.Second)
		m := newM3Services(m2GatewayDeps{Store: st, Gateway: gateway})
		m.Scheduler = scheduler.New(scheduler.Deps{Store: st, Tasks: st, OwnerID: "replacement", Clock: func() time.Time { return now }})
		m.planRetirer = retirer
		m.planArtifacts = &artifact.Service{Store: st, Scratch: t.TempDir(), Clock: time.Now}
		leader, err := m.Scheduler.MaintainLeadership(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.maintain(ctx, leader); err != nil {
			t.Fatalf("单项失败阻断维护/恢复: %v", err)
		}
		old, err = st.GetSchedulerLeader(ctx)
		if err != nil || !old.Recovered {
			t.Fatalf("恢复未完成: %+v %v", old, err)
		}
		for _, attempt := range []domain.WorkflowAttemptID{aid, secondAttempt.ID} {
			ad, found, err := st.GetSchedulerAdmissionForAttempt(ctx, attempt)
			if err != nil || !found || ad.State == domain.AdmissionReleased || ad.Fence != old.Fence {
				t.Fatalf("未终止执行丢失占位: %+v %v", ad, err)
			}
		}
		if gateway.SpawnCalls() != 2 || gateway.InterruptCount() != 2 {
			t.Fatal("维护重新发送或启动")
		}
		// 下一轮只让停止边界返回成功，但真实 Session 仍活跃；缺少真实终止不能释放槽位。
		retirer.err = nil
	}
	if retirer.calls != 4 {
		t.Fatalf("重建后未重新处理持久收尾: %d", retirer.calls)
	}
	rec, _, err := st.GetSession(ctx, *bound.Binding.SessionID)
	if err != nil || rec.IsTerminated {
		t.Fatalf("维护伪造物理终止: %+v %v", rec, err)
	}
}
