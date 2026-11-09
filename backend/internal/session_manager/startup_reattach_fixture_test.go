package sessionmanager

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// startupHost 保留现存 Provider 与连接关闭的区别，验证失败重附着不能终止 Host。
type startupHost struct {
	*integrationChatConversation
	live               bool
	closed, terminated atomic.Int64
}

func (h *startupHost) ReconnectedLive() bool { return h.live }
func (h *startupHost) Close() error          { h.closed.Add(1); return h.integrationChatConversation.Close() }
func (h *startupHost) Terminate() error      { h.terminated.Add(1); return h.Close() }

// startupReconnectDriver 只提供内存 Host；若产品走 Start/Resume，计数立即揭示越权。
type startupReconnectDriver struct {
	integrationChatDriver
	host                        *startupHost
	before                      func(context.Context) error
	reconnects, starts, resumes atomic.Int64
}

func (d *startupReconnectDriver) Start(context.Context, ports.ChatStartConfig) (ports.ChatConversation, error) {
	d.starts.Add(1)
	return nil, errors.New("startup must not create a provider")
}
func (d *startupReconnectDriver) Resume(context.Context, ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.resumes.Add(1)
	return nil, errors.New("startup must not resume a provider")
}

// Reconnect 使用生产 PrepareEnv 链验证观察许可没有丢失，然后模拟可阻塞的 Host I/O。
func (d *startupReconnectDriver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	d.reconnects.Add(1)
	if cfg.PrepareEnv != nil {
		if _, err := cfg.PrepareEnv(ctx); err != nil {
			return nil, err
		}
	}
	if d.before != nil {
		if err := d.before(ctx); err != nil {
			return nil, err
		}
	}
	return d.host, nil
}

// startupReattachFixture 使用真实 SQLite、Manager、Scheduler 和 Chat Service，时间与 Provider 为隔离替身。
type startupReattachFixture struct {
	s         *sqlite.Store
	m         *Manager
	chat      *chatsvc.Service
	driver    *startupReconnectDriver
	scheduler *scheduler.Service
	clock     atomic.Int64
	rec       domain.SessionRecord
	ad        domain.SchedulerAdmission
	attempt   domain.WorkflowAttempt
	dbPath    string
}

func (f *startupReattachFixture) now() time.Time { return time.Unix(0, f.clock.Load()).UTC() }

// newStartupReattachFixture 可构造旧版普通 Session 或有预算占位的过期 Workflow Host。
func newStartupReattachFixture(t *testing.T, workflow, boot bool) *startupReattachFixture {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	f := &startupReattachFixture{s: sqlitetest.MustOpenAt(t, dataDir), dbPath: filepath.Join(dataDir, "ao.db")}
	f.clock.Store(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).UnixNano())
	root := t.TempDir()
	project := domain.ProjectRecord{ID: "startup-project", Path: root, Config: testRoleAgents(), RegisteredAt: f.now()}
	if err := f.s.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	f.rec = domain.SessionRecord{ProjectID: domain.ProjectID(project.ID), Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: f.now()}, CreatedAt: f.now(), UpdatedAt: f.now(),
		Metadata: domain.SessionMetadata{WorkspacePath: root, Branch: "ao/startup/root", ProviderConversationID: "existing-thread", ControllerGeneration: "existing-generation"}}
	if workflow {
		f.seedWorkflow(t)
	} else {
		var err error
		f.rec, err = f.s.CreateSession(ctx, f.rec)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.driver = &startupReconnectDriver{integrationChatDriver: integrationChatDriver{harness: domain.HarnessCodex}, host: &startupHost{integrationChatConversation: newIntegrationChatConversation("existing-thread"), live: true}}
	f.chat = chatsvc.New(chatsvc.Options{Store: f.s, Sessions: f.s, Drivers: integrationChatRegistry{domain.HarnessCodex: f.driver}, Now: f.now, NewID: uuid.NewString, Log: slog.New(slog.DiscardHandler),
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
			r, err := f.s.LoadConversationSnapshot(ctx, id)
			return chatsvc.ConversationRows{Conversation: r.Conversation, Turns: r.Turns, Messages: r.Messages, Activities: r.Activities}, err
		})})
	t.Cleanup(func() { f.chat.StopAll(context.Background()) })
	f.m = New(Deps{Store: f.s, Runtime: &fakeRuntime{}, Workspace: &fakeWorkspace{path: root}, Agents: fakeAgents{}, Messenger: &fakeMessenger{}, Lifecycle: lifecycle.New(f.s, nil), Chat: integrationChatLauncher{service: f.chat}, BrowserCapabilities: browsersvc.NewAuthority(), Clock: f.now, DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil }})
	f.scheduler = scheduler.New(scheduler.Deps{Store: f.s, Tasks: f.s, Clock: f.now, OwnerID: "startup-instance"})
	f.m.SetSessionAdmissionProvider(f.scheduler)
	if boot {
		f.boot(t)
	}
	return f
}

// newStartupLegacyReattachFixture 直接还原升级前的 M2 行；新版写 API 不应允许创建无 Admission 的执行。
func newStartupLegacyReattachFixture(t *testing.T) (*startupReattachFixture, *sql.DB) {
	t.Helper()
	f := newStartupReattachFixture(t, false, true)
	ctx, now := context.Background(), f.now()
	w := domain.Workflow{ID: "legacy-wf", Title: "legacy", Goal: "observe existing host", State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now}
	task := domain.WorkflowTask{ID: "legacy-task", WorkflowID: w.ID, Title: "observe", Kind: domain.WorkflowTaskKindDevelopment, State: domain.WorkflowTaskStatePending, MaxAttempts: 2, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if _, _, err := f.s.CreateWorkflow(ctx, w, nil, []domain.WorkflowTask{task}, nil, nil, "legacy-create", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO workflow_attempts(id,workflow_id,task_id,attempt_no,session_id,worker_id,lease_token,lease_until,state,created_at,updated_at)
		VALUES('legacy-attempt',?,?,1,?,'legacy-worker','legacy-lease',?,'unknown',?,?)`, w.ID, task.ID, f.rec.ID, now.Add(-time.Minute), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workflow_attempt_bindings(attempt_id,workflow_id,task_id,spawn_request_id,spawn_hash,state,session_id,controller_generation,bound_at,revision,created_at,updated_at)
		VALUES('legacy-attempt',?,?,'legacy-spawn',?,'recovery_uncertain',?,?,?,1,?,?)`, w.ID, task.ID, strings.Repeat("b", 64), f.rec.ID, f.rec.Metadata.ControllerGeneration, now, now, now); err != nil {
		t.Fatal(err)
	}
	f.attempt, _, err = f.s.GetWorkflowAttempt(ctx, "legacy-attempt")
	if err != nil {
		t.Fatal(err)
	}
	return f, db
}

// boot 模拟 daemon 的领导权恢复；不提供资源采样器，因此无法意外取得普通执行准入。
func (f *startupReattachFixture) boot(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	l, err := f.scheduler.MaintainLeadership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.ad.ID != "" {
		f.ad, _, err = f.s.AdoptSchedulerAdmission(ctx, f.ad.ID, f.ad.Revision, l.Fence, f.now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.s.CompleteSchedulerRecovery(ctx, l.Fence, l.Revision, f.now()); err != nil {
		t.Fatal(err)
	}
}

// seedWorkflow 通过真实准入/Binding/Session事务构造 Host，随后按生产恢复语义标记 UNKNOWN。
func (f *startupReattachFixture) seedWorkflow(t *testing.T) {
	t.Helper()
	ctx, now := context.Background(), f.now()
	a := sqlitetest.NewAdmissionFixture(t, f.s, f.now)
	n := int64(10)
	policy := domain.BudgetPolicy{ID: "startup-budget", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 100, ReservationNanos: &n, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "test"}
	if _, _, err := f.s.PutBudgetPolicy(ctx, policy, 0, domain.PolicyChange{IdempotencyKey: "budget", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "test", Reason: "TEST_BUDGET", Now: now}); err != nil {
		t.Fatal(err)
	}
	w := domain.Workflow{ID: "startup-wf", Title: "startup", Goal: "recover host", State: domain.WorkflowStateDraft, Revision: 1, CreatedAt: now, UpdatedAt: now}
	task := domain.WorkflowTask{ID: "startup-task", WorkflowID: w.ID, Title: "observe", Kind: domain.WorkflowTaskKindDevelopment, State: domain.WorkflowTaskStatePending, MaxAttempts: 2, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if _, _, err := f.s.CreateWorkflow(ctx, w, nil, []domain.WorkflowTask{task}, nil, nil, "create", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	w, _, err := f.s.ActivateWorkflow(ctx, w.ID, 1, "activate", strings.Repeat("b", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err = f.s.GetWorkflowTask(ctx, w.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.PrepareWorkflowAdmission(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(20 * time.Second)
	f.attempt = domain.WorkflowAttempt{ID: "startup-attempt", WorkflowID: w.ID, TaskID: task.ID, WorkerID: "worker", LeaseToken: "private-lease", LeaseUntil: &until, State: domain.WorkflowAttemptStateClaimed, CreatedAt: now, UpdatedAt: now}
	f.attempt, f.ad, _, err = f.s.ReserveAndClaim(ctx, domain.ClaimAdmissionInput{Attempt: f.attempt, ExpectedTaskRevision: task.Revision, ExpectedWorkflowRevision: w.Revision, IdempotencyKey: "claim", RequestFingerprint: strings.Repeat("c", 64), Evidence: e})
	if err != nil {
		t.Fatal(err)
	}
	permitCtx := func() context.Context { return sqlitetest.ExecutionContext(ctx, t, f.s, f.attempt.ID, f.now()) }
	b := domain.WorkflowAttemptBinding{AttemptID: f.attempt.ID, WorkflowID: w.ID, TaskID: task.ID, SpawnRequestID: "startup-spawn", SpawnHash: strings.Repeat("d", 64)}
	b, _, err = f.s.CreateWorkflowAttemptBinding(permitCtx(), b, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.TransitionWorkflowAttemptBinding(permitCtx(), b.AttemptID, domain.WorkflowBindingStateSpawnRequested, "", b.Revision, now); err != nil {
		t.Fatal(err)
	}
	f.rec.ClientRequestID, f.rec.ClientRequestHash = b.SpawnRequestID, b.SpawnHash
	f.rec, _, err = f.s.CreateAdmittedSession(permitCtx(), f.rec, f.ad, "", now)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err = f.s.GetWorkflowAttemptBinding(ctx, b.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.BindWorkflowAttemptSession(permitCtx(), b.AttemptID, f.rec.ID, f.rec.Metadata.ControllerGeneration, domain.WorkflowBindingStateBoundReady, b.Revision, now); err != nil {
		t.Fatal(err)
	}
	f.ad, _, err = f.s.GetSchedulerAdmission(ctx, f.ad.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Add(int64(31 * time.Second))
	if count, err := f.s.RecoverExpiredWorkflowAttempts(ctx, f.now()); err != nil || count != 1 {
		t.Fatalf("recover expired attempt: count=%d err=%v", count, err)
	}
}
