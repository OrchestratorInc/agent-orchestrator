package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
)

// TestChatSpawnOmitsLegacyWorkflowLease 捕获真实 Manager 初次 StartChat 参数；
// 删除 runtimeEnvChecked 的旧租约清理，会使账户残留进入 Agent 环境。
func TestChatSpawnOmitsLegacyWorkflowLease(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, runtime := newChatManager(launcher)
	mgr.store = &initialWorkflowTaskStore{fakeStore: store}
	mgr.dataDir = t.TempDir()
	mgr.browserCapabilities = browsersvc.NewAuthority()
	var accountLog []string
	mgr.agentAccounts = &workflowLeaseAccounts{newTransitionAccounts(&accountLog)}
	project := store.projects[string(chatTestProject)]
	project.Config.Env = map[string]string{EnvWorkflowLease: "legacy-project-lease"}
	if envKeysCaseInsensitive {
		project.Config.Env[strings.ToLower(EnvWorkflowLease)] = "legacy-case-alias"
	}
	store.projects[string(chatTestProject)] = project
	_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		WorkflowSpawn: &ports.WorkflowSpawnContext{WorkflowID: "workflow-initial", TaskID: "task-initial", AttemptID: "attempt-initial"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.started) != 1 || runtime.created != 0 {
		t.Fatalf("initial start count=%d terminal spawn=%d", len(launcher.started), runtime.created)
	}
	start := launcher.started[0]
	mcpEnv := mcpEnvOf(t, start)
	for _, env := range []map[string]string{start.Env, mcpEnv} {
		for key, value := range env {
			if strings.EqualFold(key, EnvWorkflowLease) || containsAny(value, "legacy-project-lease", "legacy-case-alias") {
				t.Fatal("initial start sent a legacy lease to Agent or MCP")
			}
		}
	}
	if mcpEnv[EnvWorkflowID] != "workflow-initial" || mcpEnv[EnvWorkflowAttemptID] != "attempt-initial" {
		t.Fatal("initial MCP environment lost its non-secret workflow identity")
	}
	if start.Env[EnvBrowserCapability] == "" || mcpEnv[EnvBrowserCapability] != start.Env[EnvBrowserCapability] {
		t.Fatal("initial MCP environment lost the issued Session capability")
	}
}

// initialWorkflowTaskStore 补齐非 Planner 的持久任务身份，不放宽生产查询门禁。
type initialWorkflowTaskStore struct{ *fakeStore }

func (s *initialWorkflowTaskStore) GetWorkflowTask(_ context.Context, w domain.WorkflowID, id domain.WorkflowTaskID) (domain.WorkflowTask, bool, error) {
	return domain.WorkflowTask{ID: id, WorkflowID: w}, w == "workflow-initial" && id == "task-initial", nil
}

// workflowLeaseAccounts 模拟旧账户启动环境在项目过滤后重新带入租约。
type workflowLeaseAccounts struct{ *transitionAccounts }

// SessionLaunchEnv 在 Windows 同时构造大小写别名，覆盖账户合并的平台语义。
func (a *workflowLeaseAccounts) SessionLaunchEnv(context.Context, domain.SessionID) (map[string]string, error) {
	env := map[string]string{EnvWorkflowLease: "legacy-account-lease"}
	if envKeysCaseInsensitive {
		env[strings.ToLower(EnvWorkflowLease)] = "legacy-account-alias"
	}
	return env, nil
}

func (a *workflowLeaseAccounts) SessionLaunchEnvWith(ctx context.Context, id domain.SessionID, _ string) (map[string]string, error) {
	return a.SessionLaunchEnv(ctx, id)
}

// TestWorkflowMCPEnvClearsLegacyLeaseOnReuse 删除任一 env 清理调用会让旧租约
// 在缺少 Workflow 或 capability 的提前返回分支中残留。
func TestWorkflowMCPEnvClearsLegacyLeaseOnReuse(t *testing.T) {
	workflow := &ports.WorkflowSpawnContext{WorkflowID: "workflow-1", AttemptID: "attempt-1"}
	for _, tc := range []struct {
		name  string
		apply func(map[string]string)
	}{
		{"no workflow", func(env map[string]string) { applyWorkflowMCPEnv(env, nil, "") }},
		{"no capability", func(env map[string]string) { applyWorkflowMCPEnv(env, workflow, "") }},
		{"server without capability", func(env map[string]string) {
			applyWorkflowMCPEnvToServers([]ports.ChatMCPServerConfig{{Env: env}}, "")
		}},
		{"server with capability", func(env map[string]string) {
			applyWorkflowMCPEnvToServers([]ports.ChatMCPServerConfig{{Env: env}}, "new-capability")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{EnvWorkflowLease: "legacy-lease", EnvSessionID: "session-1"}
			if envKeysCaseInsensitive {
				env[strings.ToLower(EnvWorkflowLease)] = "legacy-lease"
			}
			tc.apply(env)
			for key := range env {
				if strings.EqualFold(key, EnvWorkflowLease) {
					t.Fatal("reused MCP environment retained a legacy lease")
				}
			}
			if env[EnvSessionID] != "session-1" {
				t.Fatal("lease cleanup changed the Session identity")
			}
		})
	}
}
