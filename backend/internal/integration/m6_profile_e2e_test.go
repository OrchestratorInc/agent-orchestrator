package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/providerprofile"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// m6NoNativeAccount 保留本地安装事实，但原生账号不存在；任何账号绑定都是本用例要捕获的错误。
type m6NoNativeAccount struct {
	ports.AgentAccountLaunchEnv
	accountCalls atomic.Int32
	authCalls    atomic.Int32
}

// BindSession 使绕回默认账号的生产调用立即失败。
func (a *m6NoNativeAccount) BindSession(context.Context, domain.SessionID, domain.AgentHarness, string) error {
	a.accountCalls.Add(1)
	return errors.New("native account absent")
}

// SessionLaunchEnv 不提供默认账号的环境。
func (a *m6NoNativeAccount) SessionLaunchEnv(context.Context, domain.SessionID) (map[string]string, error) {
	a.accountCalls.Add(1)
	return nil, errors.New("native account absent")
}

// EnsureAgentReadiness 明确报告原生账号未授权。
func (a *m6NoNativeAccount) EnsureAgentReadiness(context.Context, string, domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	a.authCalls.Add(1)
	return domain.AgentReadinessSnapshot{Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled}, Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationUnauthorized}}, nil
}
func (*m6NoNativeAccount) EnsureAgentInstallation(context.Context, string) (domain.AgentInstallationState, error) {
	return domain.AgentInstallationInstalled, nil
}
func (*m6NoNativeAccount) InvalidateAgentInstallation(string)   {}
func (*m6NoNativeAccount) InvalidateAgentAuthentication(string) {}
func (*m6NoNativeAccount) RecheckAgent(string)                  {}

// TestM6ProfileHTTPManagerRestart 删除任一 Profile 透传、原生账号隔离或重启固定身份检查会阻断正式链或重复启动。
// HTTP、SQLite、Scheduler、Manager、Git 和审批使用生产实现；仅安装事实、鉴权服务及 Chat Driver 离线替代。
func TestM6ProfileHTTPManagerRestart(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Error("unexpected provider operation")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer m6-fake-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"profile-model"}]}`))
	}))
	defer server.Close()
	data := t.TempDir()
	path := filepath.Join(data, providerprofile.Filename)
	raw, err := json.Marshal([]ports.ProviderProfile{{ID: "offline", Name: "离线供应商", Harness: domain.HarnessCodex, BaseURL: server.URL + "/v1", Model: "profile-model", Effort: "high", CredentialEnv: "AO_WORKFLOW_CREDENTIAL_TEST"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	registry := func() *providerprofile.Registry {
		return &providerprofile.Registry{Path: path, Transport: server.Client().Transport, LookupEnv: func(string) (string, bool) { return "m6-fake-key", true }}
	}
	profiles := registry()
	accounts := &m6NoNativeAccount{}
	st := sqlitetest.MustOpenAt(t, data)
	clock := &m3Clock{now: time.Now().UTC().Add(time.Hour)}
	c := newM3Control(t, st, clock, "m6-profile-boot")
	c.quality = &m4Services{}
	d := &m5Driver{Driver: scripted.NewDriver(scripted.Config{Harness: domain.HarnessCodex})}
	plant := m2ffPlant{store: st, dataDir: data, managedDir: filepath.Join(data, "worktrees"), repoA: m2smGitRepo(t), repoB: m2smGitRepo(t), m3: c, driver: d, profiles: profiles, readiness: accounts, accounts: accounts}
	f := newM2FFFixture(t, plant)
	m3Policy(t, f, "", 1)
	directory := m4HTTP[controllers.WorkflowProviderProfilesResponse](t, f, http.MethodGet, "/workflows/provider-profiles", nil)
	if len(directory.Profiles) != 1 {
		t.Fatalf("profile directory: %+v", directory)
	}
	ref := directory.Profiles[0].Reference
	public := m3JSON(t, directory)
	if strings.Contains(public, "m6-fake-key") || strings.Contains(public, "AO_WORKFLOW_CREDENTIAL_TEST") || strings.Contains(public, server.URL) {
		t.Fatal("HTTP exposed private provider data")
	}
	sha := strings.TrimSpace(m2smGit(t, f.repoA, "rev-parse", "HEAD"))
	created := m4HTTP[controllers.WorkflowEnvelope](t, f, http.MethodPost, "/workflows/plan", controllers.StartWorkflowPlanRequest{Title: "供应商规划", Goal: "离线规划", IdempotencyKey: "m6-profile-plan", AgentHarness: "codex", Model: "profile-model", Effort: "high", Profile: ref, Repositories: []controllers.WorkflowRepositoryInput{{RepoKey: "repo-a", ProjectID: string(f.projectA), BaseSHA: sha, TargetRef: "refs/heads/main"}}})
	w := domain.WorkflowID(created.Workflow.ID)
	workflowPath := "/workflows/" + string(w)
	m4HTTP[controllers.WorkflowEnvelope](t, f, http.MethodPost, workflowPath+"/activate", map[string]any{"expectedRevision": created.Workflow.Revision, "idempotencyKey": "m6-profile-activate"})
	planner := m5Dispatch(t, f)
	d.mu.Lock()
	cfg := d.starts[0]
	d.mu.Unlock()
	if cfg.Profile != ref || cfg.Model != "profile-model" || cfg.Effort != "high" || !cfg.ReadOnly || len(cfg.MCPServers) != 0 {
		t.Fatal("HTTP selection or Planner boundary lost before Driver")
	}
	for _, value := range cfg.Env {
		if strings.Contains(value, "m6-fake-key") {
			t.Fatal("secret cached in Chat start configuration")
		}
	}
	plan := m5Patch(f, "profile-worker")
	plan.Tasks[0].Model = ""
	plan.Tasks[0].Effort = ""
	m5Reply(t, f, d, planner, plan)
	def, err := c.workflows.GetDefinition(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	candidate := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, workflowPath+"/plan-candidates", workflow.PlanInput{SourceAttemptID: planner.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "m6-profile-candidate"}).Candidate
	m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, workflowPath+"/plan-candidates/"+candidate.ID+"/confirm", controllers.ConfirmWorkflowPlanRequest{Digest: candidate.Digest, ExpectedRevision: candidate.ExpectedRevision})
	f.stop()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	reopened, err := sqlite.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	c = newM3Control(t, reopened, clock, "m6-profile-restart")
	c.quality = &m4Services{}
	plant.store, plant.m3, plant.profiles = reopened, c, registry()
	f = newM2FFFixture(t, plant)
	if len(d.Conversations()) != 1 {
		t.Fatal("restart spawned duplicate Planner")
	}
	task, found, err := reopened.GetWorkflowTask(ctx, w, "profile-worker")
	if err != nil || !found || task.Profile != ref || task.Model != "profile-model" || task.Effort != "high" {
		t.Fatalf("persisted generated selection lost: found=%v err=%v", found, err)
	}
	m5Retire(t, f)
	m5Resume(t, f, w)
	worker := m5Dispatch(t, f)
	d.mu.Lock()
	cfg = d.starts[1]
	d.mu.Unlock()
	if cfg.Profile != ref || cfg.Model != "profile-model" || cfg.Effort != "high" || cfg.ReadOnly {
		t.Fatal("reopened worker changed its route or permission boundary")
	}
	if accounts.accountCalls.Load() != 0 || accounts.authCalls.Load() != 0 {
		t.Fatalf("profile used native account: env=%d auth=%d", accounts.accountCalls.Load(), accounts.authCalls.Load())
	}
	if err := f.chat.SwitchProvider(ctx, *worker.SessionID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode}); err == nil {
		t.Fatal("profile switched to a native provider")
	}
	if _, err := c.manager.StartInterfaceTransition(ctx, *worker.SessionID, domain.SessionModeTUI, domain.SessionInterfaceTransitionPolicy("drain"), domain.SessionInterfaceTransitionHistoryPolicy("strict")); err == nil {
		t.Fatal("profile switched to terminal")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "profile-model", "changed-model", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	response := f.asOperator(http.MethodPost, f.bindPath(w, worker.ID), m3JSON(t, map[string]any{"harness": "codex", "mode": "chat", "prompt": task.Title + "\n" + task.Brief, "idempotencyKey": "m6-drift"}))
	if response.Status == http.StatusOK || len(d.Conversations()) != 2 {
		t.Fatalf("drift reused native evidence or duplicated spawn: status=%d", response.Status)
	}
	worktrees, err := reopened.ListSessionWorktrees(ctx, *worker.SessionID)
	if err != nil || len(worktrees) != 1 {
		t.Fatalf("worker worktree lost: %v", err)
	}
	if _, err := os.Stat(worktrees[0].WorktreePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	stop := m4HTTP[controllers.WorkflowInterventionEnvelope](t, f, http.MethodPost, f.attemptPath(w, worker.ID, "interventions"), map[string]any{"mode": "stop", "clientMessageId": "m6-profile-stop"})
	def, err = c.workflows.GetDefinition(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	replan := m4HTTP[controllers.WorkflowTaskEnvelope](t, f, http.MethodPost, workflowPath+"/replan", workflow.ReplanInput{StopInterventionID: stop.Intervention.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "m6-profile-replan", AgentHarness: domain.HarnessCodex, Model: "profile-model", Effort: "high", Profile: ref})
	if replan.Task.Profile != ref {
		t.Fatal("Stop & Replan lost selected provider")
	}
}
