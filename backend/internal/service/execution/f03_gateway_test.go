package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// 旧 Gateway 夹具明确表示无 Workflow 绑定；缺少读取接口不是 Legacy 的证明。
func (f *fakeSessions) GetWorkflowAttemptBindingBySession(context.Context, domain.SessionID) (domain.WorkflowAttemptBinding, bool, error) {
	return domain.WorkflowAttemptBinding{}, false, nil
}

func (f *fakeSessions) GetWorkflowAttemptBindingBySpawnRequest(context.Context, string) (domain.WorkflowAttemptBinding, bool, error) {
	return domain.WorkflowAttemptBinding{}, false, nil
}

// TestIndependentM2SingleRepoWorktreeRequiresLiveVerification 固定第五轮原始反例；
// 删除 Gateway 的实时观察门禁后，旧 CreationSHA 会让不存在的单仓工作区错误 READY。
func TestIndependentM2SingleRepoWorktreeRequiresLiveVerification(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "f03-single", "gen-1")
	f.sessions.worktrees = []domain.SessionWorktreeRecord{{
		SessionID: rec.ID, RepoName: "main", ProjectID: rec.ProjectID,
		RepoPath: "C:/missing-repository", WorktreePath: "C:/missing-worktree",
		CreationSHA: "0123456789012345678901234567890123456789",
	}}
	observation, err := f.gateway.InspectSession(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Readiness == ports.SessionReadinessReady {
		t.Fatal("single-repo observation claimed READY solely from persisted row, with no live Git verification")
	}
	for _, row := range observation.Worktrees {
		if row.HeadSHA != "" {
			t.Fatalf("未验证工作区仍报告 HEAD：%s", row.HeadSHA)
		}
	}
}

// TestM2F03MissingExpectedRepoClosesGate 删除显式绑定门禁后，缺失单仓记录会被放行。
func TestM2F03MissingExpectedRepoClosesGate(t *testing.T) {
	states, gate := repoGateDecision([]domain.WorkflowAttemptRepoBinding{{RepoKey: "main", ProjectID: "project"}}, nil)
	if !gate || blockedRepoCode(states, gate) != RepoWorktreeMissingCode {
		t.Fatalf("显式单仓记录缺失仍放行：gate=%v states=%v", gate, states)
	}
}

// f03BoundRecords 保留真实读取形状，模拟 worktree rows 丢失但 Spawn 意图仍存在。
type f03BoundRecords struct{ *fakeSessions }

func (f *f03BoundRecords) GetWorkflowAttemptBindingBySpawnRequest(context.Context, string) (domain.WorkflowAttemptBinding, bool, error) {
	return domain.WorkflowAttemptBinding{AttemptID: "attempt"}, true, nil
}

// TestM2F03MissingOwnershipEvidenceIsUnknown 删除缺能力分支或 BySpawnRequest 查询即变红。
func TestM2F03MissingOwnershipEvidenceIsUnknown(t *testing.T) {
	for _, scenario := range []string{"persisted_spawn_intent", "missing_worktree_reader", "missing_binding_reader"} {
		t.Run(scenario, func(t *testing.T) {
			f := newGatewayFixture()
			rec := readyChatSession(f, "f03-missing", "gen-1")
			rec.ClientRequestID = "ordinary-client-request"
			f.sessions.put(rec)
			switch scenario {
			case "persisted_spawn_intent":
				f.gateway.records = &f03BoundRecords{f.sessions}
			case "missing_worktree_reader":
				f.gateway.worktrees = nil
			case "missing_binding_reader":
				f.gateway.records = struct{ SessionReader }{f.sessions}
			}
			observation, err := f.gateway.InspectSession(context.Background(), rec.ID)
			if err != nil || observation.Readiness != ports.SessionReadinessUnknown {
				t.Fatalf("缺乏实时证据仍放行：readiness=%s err=%v", observation.Readiness, err)
			}
		})
	}
}

// f03ObservedOwner 切换观察结果，证明前次成功不能被当作下一次执行的永久凭证。
type f03ObservedOwner struct {
	fakeSpawner
	fail  bool
	calls int
}

func (o *f03ObservedOwner) ObserveSessionWorktrees(context.Context, domain.SessionID) ([]ports.RepoWorktreeObservation, error) {
	o.calls++
	rows := []ports.RepoWorktreeObservation{{RepoKey: "main", HeadSHA: "live-head"}}
	if o.fail {
		return rows, errors.New("workspace no longer verifiable")
	}
	return rows, nil
}

// TestM2F03RechecksBeforeSideEffects 删除 Send/Steer/Interrupt/Resume 的当前观察即变红；
// 以边界调用次数断言没有副作用，不复制 Git 层的异常矩阵。
func TestM2F03RechecksBeforeSideEffects(t *testing.T) {
	f := newGatewayFixture()
	rec := readyChatSession(f, "f03-changed", "gen-1")
	rec.ClientRequestID = "spawn-key"
	f.sessions.put(rec)
	owner := &f03ObservedOwner{}
	f.gateway.sessions = owner
	f.gateway.WithSteerPinned(func(context.Context, domain.SessionID, string, string, ports.ChatUserMessage) (chatsvc.SteerResult, error) {
		t.Fatal("工作区失效后仍调用 Steer")
		return chatsvc.SteerResult{}, nil
	})
	ctx := context.Background()
	observation, err := f.gateway.InspectSession(ctx, rec.ID)
	if err != nil || observation.Readiness != ports.SessionReadinessReady || observation.Worktrees[0].HeadSHA != "live-head" {
		t.Fatalf("初次观察：%v %v", observation.Readiness, err)
	}
	owner.fail = true
	observation, err = f.gateway.InspectSession(ctx, rec.ID)
	if err != nil || observation.Readiness != ports.SessionReadinessUnknown || observation.Worktrees[0].HeadSHA != "" {
		t.Fatalf("失败观察泄漏旧HEAD：%v %v", observation, err)
	}
	message := ports.MessageRequest{SessionID: rec.ID, ClientMessageID: "message", Text: "work", Delivery: ports.MessageDeliverySend, ControllerGeneration: "gen-1"}
	if _, err := f.gateway.Send(ctx, message); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("Send = %v", err)
	}
	message.Delivery, message.TargetProviderTurnID = ports.MessageDeliverySteer, "provider-turn"
	if _, err := f.gateway.Steer(ctx, message); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("Steer = %v", err)
	}
	if _, err := f.gateway.Interrupt(ctx, ports.InterruptRequest{SessionID: rec.ID, ControllerGeneration: "gen-1"}); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("Interrupt = %v", err)
	}
	rec.IsTerminated = true
	f.sessions.put(rec)
	if _, err := f.gateway.Resume(ctx, ports.ResumeRequest{SessionID: rec.ID, SpawnKey: rec.ClientRequestID}); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("Resume = %v", err)
	}
	sends, interrupts := f.chats.pinnedCounts()
	restores, resumes := f.resumer.counts()
	if sends+interrupts+restores+resumes != 0 || owner.calls != 6 {
		t.Fatalf("失效后仍有副作用或缺重验：send=%d interrupt=%d restore=%d resume=%d observations=%d", sends, interrupts, restores, resumes, owner.calls)
	}
}
