package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// m2f03Flow 复用真实 Manager/Git/SQLite/HTTP 装配，Workflow 只授权一个 Repo。
type m2f03Flow struct {
	*m2smFixture
	wid domain.WorkflowID
	tid domain.WorkflowTaskID
	aid domain.WorkflowAttemptID
	sid domain.SessionID
	row domain.SessionWorktreeRecord
	pin string
	key string
}

// newM2F03Flow 只建一个临时 Git Repo；两项夹具项目登记不增加 Workflow 的 Repo 授权。
func newM2F03Flow(t *testing.T, key string) *m2f03Flow {
	t.Helper()
	dataDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	repo := m2ffRepo(t, key)
	for _, id := range []string{"m2sm-project-a", "m2sm-project-b"} {
		if err := st.UpsertProject(context.Background(), domain.ProjectRecord{
			ID: id, Path: repo, RegisteredAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	f := &m2f03Flow{m2smFixture: newM2SMFixture(t, st, dataDir, t.TempDir()), key: key}
	f.pin = strings.TrimSpace(m2smGit(t, repo, "rev-parse", "HEAD~1"))
	f.tid = domain.WorkflowTaskID(key + "-task")
	body := fmt.Sprintf(`{"title":"F-03","goal":"single repo live verification","idempotencyKey":%q,
		"repositories":[{"repoKey":"repo-a","projectId":%q,"baseSha":%q}],
		"tasks":[{"id":%q,"kind":"development","title":"work","agentHarness":%q,"maxAttempts":3}],
		"taskRepositories":[{"taskId":%q,"repoKey":"repo-a","baseSha":%q}]}`,
		key, f.projectA, f.pin, f.tid, m2smDriverHarness, f.tid, f.pin)
	created := f.do(http.MethodPost, "/workflows", body)
	if created.Status != http.StatusCreated {
		t.Fatalf("创建 Workflow: %d %s", created.Status, created.Raw)
	}
	f.wid = domain.WorkflowID(f.text(created.Body, "workflow", "id"))
	activated := f.do(http.MethodPost, "/workflows/"+string(f.wid)+"/activate", fmt.Sprintf(
		`{"expectedRevision":%d,"idempotencyKey":%q}`, f.number(created.Body, "workflow", "revision"), key+"-activate"))
	if activated.Status != http.StatusOK {
		t.Fatalf("激活 Workflow: %d %s", activated.Status, activated.Raw)
	}
	f.aid, _ = f.claimAndStart(f.wid, f.tid, key)
	bound := f.bind(f.wid, f.aid, key, "执行真实离线测试")
	if bound.Status != http.StatusOK || f.text(bound.Body, "binding", "state") != string(domain.WorkflowBindingStateBoundReady) {
		t.Fatalf("单仓 Bind 未就绪: %d %s", bound.Status, bound.Raw)
	}
	binding := f.binding(f.aid)
	if binding.SessionID == nil {
		t.Fatal("Bind 没有持久化 SessionID")
	}
	f.sid = *binding.SessionID
	rows, err := st.ListSessionWorktrees(context.Background(), f.sid)
	if err != nil || len(rows) != 1 {
		t.Fatalf("显式单仓记录数量=%d err=%v", len(rows), err)
	}
	f.row = rows[0]
	return f
}

// path 将后续操作指向已通过真实 HTTP 创建并绑定的同一个 Attempt。
func (f *m2f03Flow) path(verb string) string {
	return "/workflows/" + string(f.wid) + "/attempts/" + string(f.aid) + "/" + verb
}

// observe 不缓存成功结果，每次经过生产 Gateway 和 Coordinator。
func (f *m2f03Flow) observe() m2smResp {
	return f.do(http.MethodGet, f.path("observe"), "")
}

// m2f03Ready 同时检查 HTTP 的实时 HEAD、Repo 身份与 dirty，防止历史 SHA 冒充当前事实。
func m2f03Ready(t *testing.T, f *m2f03Flow, head string, dirty bool) {
	t.Helper()
	observed := f.observe()
	if observed.Status != http.StatusOK || f.text(observed.Body, "observation", "readiness") != string(ports.SessionReadinessReady) {
		t.Fatalf("正常单仓未 READY: %d %s", observed.Status, observed.Raw)
	}
	live := m2f03Inspect(t, f)
	if live.Readiness != ports.SessionReadinessReady || len(live.Worktrees) != 1 {
		t.Fatalf("实时单仓观察缺失: %+v", live)
	}
	row := live.Worktrees[0]
	if string(row.RepoKey) != f.row.RepoName || row.ProjectID != f.projectA ||
		filepath.Clean(row.WorktreeRef) != filepath.Clean(f.row.WorktreePath) ||
		row.HeadSHA != head || row.Branch != f.row.Branch || row.BaseSHA != f.pin {
		t.Fatalf("实时归属或 HEAD 错误: %+v", live)
	}
	if row.Dirty != dirty {
		t.Fatalf("dirty=%t want=%t: %+v", row.Dirty, dirty, live)
	}
	if live, ok := observed.Body["live"].(bool); !ok || !live {
		t.Fatalf("正常单仓未报告 live: %s", observed.Raw)
	}
}

// m2f03Unavailable 要求失败观察闭锁并清空未经核验的 HEAD，不接受 CreationSHA 降级。
func m2f03Unavailable(t *testing.T, f *m2f03Flow) {
	t.Helper()
	observed := f.observe()
	if observed.Status != http.StatusOK || f.text(observed.Body, "observation", "readiness") != string(ports.SessionReadinessUnknown) {
		t.Fatalf("失效 Worktree 未闭锁: %d %s", observed.Status, observed.Raw)
	}
	if live, _ := observed.Body["live"].(bool); live {
		t.Fatalf("失效 Worktree 仍报告 live: %s", observed.Raw)
	}
	live := m2f03Inspect(t, f)
	if live.Readiness != ports.SessionReadinessUnknown {
		t.Fatalf("失效 Gateway 仍报告 READY: %+v", live)
	}
	for _, row := range live.Worktrees {
		if row.HeadSHA != "" {
			t.Fatalf("失效检查仍把缓存当可信 HEAD: %+v", live)
		}
	}
}

// m2f03Inspect 读取公开 Gateway 契约；现有 HTTP DTO 只公开 readiness/live，不携带 Worktrees。
func m2f03Inspect(t *testing.T, f *m2f03Flow) ports.SessionObservation {
	t.Helper()
	gateway := execution.NewLocalGateway(execution.LocalGatewayDeps{
		Sessions: f.mgr, Records: f.store, Worktrees: f.store, Controllers: f.chat,
	})
	observed, err := gateway.InspectSession(context.Background(), f.sid)
	if err != nil {
		t.Fatal(err)
	}
	return observed
}

// m2f03Write 只写本用例临时文件，失败时保留真实错误。
func m2f03Write(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// m2f03Move 可逆地隔离临时目录，既不 reset/clean，也不移除 dirty 成果。
func m2f03Move(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}
