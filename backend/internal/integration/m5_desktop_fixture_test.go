package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/scripted"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestM5DesktopFixture 是显式选择的桌面数据装配器，不计入自动回归或原生授权通过数。
// 复用正式规划/收尾链，仅 Driver、运行时和人工验证沿用离线 fixture；桌面重新启动后使用正式 OS 验证器。
func TestM5DesktopFixture(t *testing.T) {
	root := os.Getenv("AO_M5_DESKTOP_FIXTURE_ROOT")
	if root == "" {
		t.Skip("仅显式指定隔离证据目录时生成桌面数据")
	}
	root, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(root) {
		t.Fatal("需要绝对隔离目录")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
	// 每次只创建新目录，不能覆盖已经运行过的桌面或用户数据库。
	lab, err := os.MkdirTemp(root, "m5-desktop-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(lab, "data")
	st := sqlitetest.MustOpenAt(t, data)
	c := newM3Control(t, st, &m3Clock{now: time.Now().UTC()}, "m5-desktop-seed")
	c.quality = &m4Services{}
	d := &m5Driver{Driver: scripted.NewDriver(scripted.Config{Harness: domain.HarnessCodex})}
	repoA, repoB := m5DesktopRepo(t, lab, "repo-a"), m5DesktopRepo(t, lab, "repo-b")
	f := newM2FFFixture(t, m2ffPlant{store: st, dataDir: data, managedDir: filepath.Join(data, "worktrees"), repoA: repoA, repoB: repoB, m3: c, driver: d})
	m3Policy(t, f, "", 1)
	sha := strings.TrimSpace(m2smGit(t, repoA, "rev-parse", "HEAD"))
	w := m4HTTP[controllers.WorkflowEnvelope](t, f, http.MethodPost, "/workflows/plan", controllers.StartWorkflowPlanRequest{
		Title: "M5离线桌面验收", Goal: "保留规划对话、任务导航与工作树", IdempotencyKey: "desktop-plan", AgentHarness: "codex", Model: "offline-fixture", Effort: "high",
		Repositories: []controllers.WorkflowRepositoryInput{{RepoKey: "repo-a", ProjectID: string(f.projectA), BaseSHA: sha, TargetRef: "refs/heads/main"}},
	}).Workflow
	path := "/workflows/" + w.ID
	m4HTTP[controllers.WorkflowEnvelope](t, f, http.MethodPost, path+"/activate", map[string]any{"expectedRevision": w.Revision, "idempotencyKey": "desktop-activate"})
	planner := m5Dispatch(t, f)
	plan := m5Patch(f, "desktop-worker")
	// 桌面重启后由真实 Manager 校验模型，必须匹配离线 Codex fixture 的能力目录。
	plan.Tasks[0].Model, plan.Tasks[0].Effort = "offline-fixture", ""
	m5Reply(t, f, d, planner, plan)
	def, err := c.workflows.GetDefinition(context.Background(), domain.WorkflowID(w.ID))
	if err != nil {
		t.Fatal(err)
	}
	candidate := m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates", workflow.PlanInput{SourceAttemptID: planner.ID, ExpectedRevision: def.Workflow.Revision, IdempotencyKey: "desktop-candidate"}).Candidate
	m4HTTP[controllers.WorkflowPlanResponse](t, f, http.MethodPost, path+"/plan-candidates/"+candidate.ID+"/confirm", controllers.ConfirmWorkflowPlanRequest{Digest: candidate.Digest, ExpectedRevision: candidate.ExpectedRevision})
	m5Retire(t, f)
	status, err := c.scheduler.WorkflowStatus(context.Background(), domain.WorkflowID(w.ID))
	if err != nil || !status.Paused {
		t.Fatalf("隔离数据必须保持暂停: %+v %v", status, err)
	}
	rec, found, err := st.GetSession(context.Background(), *planner.SessionID)
	if err != nil || !found || !rec.IsTerminated {
		t.Fatalf("离线 Planner 必须完成物理收尾: %+v %v", rec, err)
	}
	metadata, err := json.MarshalIndent(map[string]any{"lab": lab, "dataDir": data, "workflowId": w.ID, "taskId": planner.TaskID, "sessionId": planner.SessionID, "projectId": f.projectA, "schedulerPaused": status.Paused, "sessionTerminated": rec.IsTerminated}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lab, "fixture.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("DESKTOP_FIXTURE=%s", filepath.Join(lab, "fixture.json"))
}

// m5DesktopRepo 仅在本次新建实验目录内建立无远程的仓库，路径保持可供桌面读取。
func m5DesktopRepo(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m2smGit(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("M5 offline desktop fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m2smGit(t, dir, "add", "README.md")
	m2smGit(t, dir, "-c", "user.name=AO Tests", "-c", "user.email=ao@example.com", "commit", "-m", "desktop fixture")
	m2smGit(t, dir, "branch", "-M", "main")
	return dir
}
