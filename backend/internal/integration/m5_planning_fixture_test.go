package integration

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// m5Driver 只记录真实 Manager 交给 Driver 的设置，输出仍通过 Scripted 事件进入 Chat。
type m5Driver struct {
	*scripted.Driver
	mu      sync.Mutex
	starts  []ports.ChatStartConfig
	prompts []string
}

func (d *m5Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.mu.Lock()
	d.starts = append(d.starts, cfg)
	d.mu.Unlock()
	c, err := d.Driver.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &m5Conversation{ChatConversation: c, driver: d}, nil
}

// m5Conversation 保留底层停止/事件行为，仅记录实际送达的目标。
type m5Conversation struct {
	ports.ChatConversation
	driver *m5Driver
}

func (c *m5Conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.driver.mu.Lock()
	c.driver.prompts = append(c.driver.prompts, msg.Text)
	c.driver.mu.Unlock()
	return c.ChatConversation.SendTurn(ctx, msg)
}

// newM5Fixture 使用真实审批、Scheduler、Manager、Git 与质量服务，容量固定为 1。
func newM5Fixture(t *testing.T) (*m2ffFixture, *m5Driver) {
	t.Helper()
	data := t.TempDir()
	st := sqlitetest.MustOpenAt(t, data)
	c := newM3Control(t, st, &m3Clock{now: time.Now().UTC().Add(time.Hour)}, "m5-boot")
	c.quality = &m4Services{}
	d := &m5Driver{Driver: scripted.NewDriver(scripted.Config{Harness: domain.HarnessCodex})}
	f := newM2FFFixture(t, m2ffPlant{store: st, dataDir: data, managedDir: filepath.Join(data, "worktrees"), repoA: m2smGitRepo(t), repoB: m2smGitRepo(t), m3: c, driver: d})
	m3Policy(t, f, "", 1)
	return f, d
}

// m5Restart 关闭原 HTTP/Chat/数据库并重新打开同一隔离数据，新实例必须恢复原占位而不再次 Spawn。
func m5Restart(t *testing.T, f *m2ffFixture, d *m5Driver) *m2ffFixture {
	t.Helper()
	f.chat.StopAll(context.Background())
	f.srv.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.m3.clock.advance(time.Minute)
	st, err := sqlite.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	c := newM3Control(t, st, f.m3.clock, "m5-restart")
	c.quality = &m4Services{}
	restarted := newM2FFFixture(t, m2ffPlant{store: st, dataDir: f.dataDir, managedDir: f.managedDir, repoA: f.repoA, repoB: f.repoB, m3: c, driver: d})
	if len(d.Conversations()) != 1 {
		t.Fatal("重启再次启动 Planner")
	}
	ads, err := st.ListSchedulerAdmissions(context.Background(), true)
	if err != nil || len(ads) != 1 {
		t.Fatalf("重启丢失未收尾占位: %+v %v", ads, err)
	}
	return restarted
}

// m5Dispatch 使用正式 HTTP Start/Bind；Prompt 来自持久任务，不能直接向数据库注入输出。
func m5Dispatch(t *testing.T, f *m2ffFixture) domain.WorkflowAttempt {
	t.Helper()
	ctx := context.Background()
	f.m3.clock.advance(time.Second)
	if _, err := f.m3.scheduler.MaintainLeadership(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := f.m3.scheduler.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Dispatches) != 1 {
		t.Fatalf("需要恰好一次准入，got %+v", out)
	}
	d := out.Dispatches[0]
	task, found, err := f.store.GetWorkflowTask(ctx, d.WorkflowID, d.TaskID)
	if err != nil || !found {
		t.Fatalf("task: %v %v", found, err)
	}
	m4HTTP[controllers.WorkflowAttemptEnvelope](t, f, http.MethodPost, f.attemptPath(d.WorkflowID, d.AttemptID, "start"), map[string]any{"idempotencyKey": "start-" + string(d.AttemptID)})
	r := f.asOperator(http.MethodPost, f.bindPath(d.WorkflowID, d.AttemptID), m3JSON(t, map[string]any{"harness": "codex", "mode": "chat", "prompt": task.Title + "\n" + task.Brief, "idempotencyKey": "bind-" + string(d.AttemptID)}))
	if r.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", r.Status, r.Raw)
	}
	a, _, err := f.store.GetWorkflowAttempt(ctx, d.AttemptID)
	if err != nil || a.SessionID == nil {
		t.Fatalf("attempt: %+v %v", a, err)
	}
	return a
}

// m5Reply 等待真实 Chat 接受 turn，再让 Scripted 输出并完成；只轮询持久完成事实。
func m5Reply(t *testing.T, f *m2ffFixture, d *m5Driver, a domain.WorkflowAttempt, p workflow.PlanDefinition) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	convs := d.Conversations()
	c := convs[len(convs)-1]
	for c.LastTurnID() == "" {
		if time.Now().After(deadline) {
			t.Fatal("Driver 未收到目标")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.ReplyAndComplete(c.LastTurnID(), m3JSON(t, p))
	for {
		conv, err := f.store.ConversationForSession(context.Background(), *a.SessionID)
		if err == nil {
			turn, e := f.store.LatestConversationTurn(context.Background(), conv.ID)
			if e == nil && turn.State == domain.TurnStateCompleted {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("Chat 没有持久化完成输出")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// m5Retire 运行正式收尾服务，不能通过改库伪造终止、容量或路径释放。
func m5Retire(t *testing.T, f *m2ffFixture) {
	t.Helper()
	ctx := context.Background()
	fence, err := f.m3.scheduler.CurrentFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := f.store.WorkflowPlanRetirements(ctx, fence, f.m3.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if err := f.m3.manager.RetireWorkflowSession(ctx, job.SessionID, job.Generation); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.m3.binder.RefreshLeases(ctx, fence); err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if _, err := f.m3.quality.artifacts.Snapshot(ctx, job.AttemptID, job.ArtifactRevision, fence); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.m3.budget.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	remaining, err := f.store.WorkflowPlanRetirements(ctx, fence, f.m3.clock.Now())
	if err != nil || len(remaining) != 0 {
		t.Fatalf("资源未收尾: %+v %v", remaining, err)
	}
}

// m5Resume 取独立 Scheduler revision；确认计划不能隐式恢复开发任务。
func m5Resume(t *testing.T, f *m2ffFixture, w domain.WorkflowID) {
	t.Helper()
	s, err := f.m3.scheduler.WorkflowStatus(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	m4HTTP[controllers.WorkflowSchedulerResponse](t, f, http.MethodPost, "/workflows/"+string(w)+"/resume-scheduling", map[string]any{"expectedRevision": s.Revision, "idempotencyKey": "resume-" + string(w) + "-" + time.Now().Format("150405.000000000"), "reason": "TEST_PLAN_CONFIRM"})
}

// m5Patch 使用用户选定的真实仓库 pin，任务运行设置需完整传到 Driver。
func m5Patch(f *m2ffFixture, id string, retire ...domain.WorkflowTaskID) workflow.PlanDefinition {
	sha := strings.TrimSpace(m2smGit(f.t, f.repoA, "rev-parse", "HEAD"))
	return workflow.PlanDefinition{Tasks: []workflow.TaskInput{{ID: domain.WorkflowTaskID(id), Kind: domain.WorkflowTaskKindDevelopment, Title: id, Brief: "受控开发", AgentHarness: domain.HarnessCodex, Model: "worker-model", Effort: "high"}},
		TaskRepositories: []workflow.TaskRepositoryInput{{TaskID: domain.WorkflowTaskID(id), RepoKey: "repo-a", BaseSHA: sha}}, RetireTaskIDs: retire}
}
