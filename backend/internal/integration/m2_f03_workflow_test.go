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
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestM2F03SingleRepoDeletedWorktree 固定真实单仓 F-03；删掉 Gateway 的实时失败闭锁即变红。
// 数据库 CreationSHA 必须保留，但它不能掩盖真实目录已经删除。
func TestM2F03SingleRepoDeletedWorktree(t *testing.T) {
	f := newM2F03Flow(t, "f03-deleted")
	if err := os.RemoveAll(f.row.WorktreePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.row.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("临时 Worktree 未被删除: %v", err)
	}
	row, found, err := f.store.GetSessionWorktree(context.Background(), f.sid, f.row.RepoName)
	if err != nil || !found || row.CreationSHA != f.pin {
		t.Fatalf("必须保留真实旧 CreationSHA 记录: found=%t sha=%s err=%v", found, row.CreationSHA, err)
	}
	observed := f.observe()
	if observed.Status != http.StatusOK || f.text(observed.Body, "observation", "readiness") != string(ports.SessionReadinessUnknown) {
		t.Fatalf("目录已删除但旧 CreationSHA=%s 仍冒充实时 READY: %d %s", f.row.CreationSHA, observed.Status, observed.Raw)
	}
	if live, _ := observed.Body["live"].(bool); live {
		t.Fatalf("缺失目录仍被报告为 live: %s", observed.Raw)
	}
}

// TestM2F03SingleRepoHTTPThroughSQLiteReopen 固定历史 pin、实时后继 HEAD、执行与重启恢复。
// 删掉 pin 传递、实时 HEAD/dirty 读取、恢复核验或 generation fence 会使本流程失败。
func TestM2F03SingleRepoHTTPThroughSQLiteReopen(t *testing.T) {
	f := newM2F03Flow(t, "f03-reopen")
	ctx := context.Background()
	if f.row.ProjectID != f.projectA || f.row.RepoName != "repo-a" ||
		filepath.Clean(f.row.RepoPath) != filepath.Clean(f.repoA) || f.row.BaseSHA != f.pin || f.row.CreationSHA != f.pin ||
		strings.TrimSpace(m2smGit(t, f.row.WorktreePath, "rev-parse", "HEAD")) != f.pin {
		t.Fatalf("真实单仓 pin/持久化归属错误: %+v", f.row)
	}
	if err := f.mgr.VerifySessionWorktrees(ctx, f.sid); err != nil {
		t.Fatalf("真实单仓 Manager 核验失败: %v", err)
	}
	m2f03Ready(t, f, f.pin, false)
	// 合法后继提交和 dirty 成果允许恢复；CreationSHA 仍固定于历史 pin。
	m2smGit(t, f.row.WorktreePath, "commit", "--allow-empty", "-m", "f03 legitimate descendant")
	head := strings.TrimSpace(m2smGit(t, f.row.WorktreePath, "rev-parse", "HEAD"))
	if head == f.pin {
		t.Fatal("未建立独立的当前 HEAD")
	}
	marker := filepath.Join(f.row.WorktreePath, "user-dirty.txt")
	tracked := filepath.Join(f.row.WorktreePath, "history-0.txt")
	const content = "合法 dirty 内容不得丢失\n"
	m2f03Write(t, marker, []byte(content))
	m2f03Write(t, tracked, []byte(content))
	m2f03Ready(t, f, head, true)
	registered := m2smGit(t, f.repoA, "worktree", "list", "--porcelain")
	replayed := f.bind(f.wid, f.aid, f.key, "执行真实离线测试")
	if replayed.Status != http.StatusOK || f.text(replayed.Body, "binding", "sessionId") != string(f.sid) {
		t.Fatalf("合法后继的重复 Bind 失败: %d %s", replayed.Status, replayed.Raw)
	}
	originalTurn, _, running := f.chat.RunningProviderTurn(ctx, f.sid)
	if !running || originalTurn == "" {
		t.Fatal("没有真实 Scripted 运行 turn，不能验证 Queue")
	}
	queued := f.do(http.MethodPost, f.path("interventions"),
		`{"mode":"queue","text":"继续执行","clientMessageId":"f03-queue"}`)
	if queued.Status != http.StatusOK || f.text(queued.Body, "intervention", "state") != string(domain.WorkflowInterventionStateQueued) {
		t.Fatalf("Queue 未持久化: %d %s", queued.Status, queued.Raw)
	}
	conversations := f.driver.Conversations()
	if len(conversations) != 1 {
		t.Fatalf("Scripted conversation 数量=%d", len(conversations))
	}
	conversations[0].CompleteTurn(originalTurn)
	deadline := time.Now().Add(10 * time.Second)
	for {
		turn, _, running := f.chat.RunningProviderTurn(ctx, f.sid)
		if f.driver.SendCalls() == 2 && running && turn != "" && turn != originalTurn {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("排队消息未执行，SendCalls=%d", f.driver.SendCalls())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if turn := conversations[0].LastTurnID(); turn == "" || turn == originalTurn {
		t.Fatalf("Queue 没有成为新的真实 turn: %q", turn)
	}
	for _, verb := range []string{"resume", "reconcile"} {
		result := f.do(http.MethodPost, f.path(verb), "")
		if result.Status != http.StatusOK || f.text(result.Body, "binding", "sessionId") != string(f.sid) ||
			f.text(result.Body, "binding", "state") != string(domain.WorkflowBindingStateBoundReady) {
			t.Fatalf("重启前 %s 失败: %d %s", verb, result.Status, result.Raw)
		}
	}
	generation := f.binding(f.aid).ControllerGeneration
	f.stopControllers()
	f.closeServer()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	f.m2smFixture = newM2SMFixture(t, reopened, f.dataDir, f.managedRoot)
	for _, verb := range []string{"resume", "reconcile"} {
		result := f.do(http.MethodPost, f.path(verb), "")
		if result.Status != http.StatusOK || f.text(result.Body, "binding", "sessionId") != string(f.sid) ||
			f.text(result.Body, "binding", "state") != string(domain.WorkflowBindingStateBoundReady) {
			t.Fatalf("SQLite 重开后 %s 失败: %d %s", verb, result.Status, result.Raw)
		}
	}
	m2f03Ready(t, f, head, true)
	if current := f.binding(f.aid).ControllerGeneration; current == "" || current == generation {
		t.Fatalf("重开后未轮换 controller generation: %q", current)
	}
	stale := f.do(http.MethodPost, f.path("interventions"), fmt.Sprintf(
		`{"mode":"queue","text":"旧控制器","clientMessageId":"f03-stale","controllerGeneration":%q}`, generation))
	if stale.Status != http.StatusConflict {
		t.Fatalf("旧 generation 越过 fence: %d %s", stale.Status, stale.Raw)
	}
	row, found, err := reopened.GetSessionWorktree(ctx, f.sid, "repo-a")
	if err != nil || !found || row.ProjectID != f.row.ProjectID || row.RepoPath != f.row.RepoPath ||
		row.BaseSHA != f.pin || row.CreationSHA != f.pin || m2smSessionCount(t, reopened) != 1 ||
		m2smGit(t, f.repoA, "worktree", "list", "--porcelain") != registered {
		t.Fatalf("SQLite 重开改变持久化归属或新建资源: found=%t row=%+v err=%v", found, row, err)
	}
	if _, err := reopened.RecoverExpiredWorkflowAttempts(ctx, time.Now().UTC().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	task, found, err := reopened.GetWorkflowTask(ctx, f.wid, f.tid)
	if err != nil || !found {
		t.Fatalf("读取待裁决任务: found=%t err=%v", found, err)
	}
	resolved := f.do(http.MethodPost, f.path("resolve"), fmt.Sprintf(
		`{"outcome":"succeeded","expectedTaskRevision":%d,"idempotencyKey":"f03-resolve"}`, task.Revision))
	if resolved.Status != http.StatusOK || f.text(resolved.Body, "attempt", "state") != string(domain.WorkflowAttemptStateSucceeded) {
		t.Fatalf("HTTP Resolve 失败: %d %s", resolved.Status, resolved.Raw)
	}
	for _, path := range []string{marker, tracked} {
		if got, err := os.ReadFile(path); err != nil || string(got) != content {
			t.Fatalf("执行/恢复破坏 dirty 内容: %q err=%v", got, err)
		}
	}
}

// TestM2F03SingleRepoExecutionGates 每个执行入口都必须重读真实工作区，不能缓存一次成功。
// 删掉 Queue/Steer/Resume/Reconcile/Bind 的 live gate 会使对应 HTTP 409 断言变红。
func TestM2F03SingleRepoExecutionGates(t *testing.T) {
	f := newM2F03Flow(t, "f03-gates")
	marker := filepath.Join(f.row.WorktreePath, "user-dirty.txt")
	tracked := filepath.Join(f.row.WorktreePath, "history-0.txt")
	const content = "执行门禁保留现场\n"
	m2f03Write(t, marker, []byte(content))
	m2f03Write(t, tracked, []byte(content))
	for _, verb := range []string{"queue", "steer", "resume", "reconcile", "bind"} {
		t.Run(verb, func(t *testing.T) {
			if verb == "resume" {
				f.stopControllers()
			}
			saved := f.row.WorktreePath + "-saved"
			m2f03Move(t, f.row.WorktreePath, saved)
			defer func() {
				m2f03Move(t, saved, f.row.WorktreePath)
				resumed := f.do(http.MethodPost, f.path("resume"), "")
				if resumed.Status != http.StatusOK {
					t.Fatalf("恢复有效现场后 Resume 失败: %d %s", resumed.Status, resumed.Raw)
				}
				m2f03Ready(t, f, f.pin, true)
				for _, path := range []string{marker, tracked} {
					if got, err := os.ReadFile(path); err != nil || string(got) != content {
						t.Fatalf("门禁破坏 dirty 内容: %q err=%v", got, err)
					}
				}
			}()
			beforeSends, beforeSteers, beforeOpens := f.driver.SendCalls(), f.driver.SteerCalls(), len(f.driver.Conversations())
			var refused m2smResp
			switch verb {
			case "queue", "steer":
				turn, _, _ := f.chat.RunningProviderTurn(context.Background(), f.sid)
				body := fmt.Sprintf(`{"mode":%q,"text":"禁止执行","clientMessageId":%q}`, verb, "f03-refused-"+verb)
				if verb == "steer" {
					body = fmt.Sprintf(`{"mode":"steer","text":"禁止执行","clientMessageId":"f03-refused-steer","targetProviderTurnId":%q}`, turn)
				}
				refused = f.do(http.MethodPost, f.path("interventions"), body)
			case "bind":
				refused = f.bind(f.wid, f.aid, f.key, "执行真实离线测试")
			default:
				refused = f.do(http.MethodPost, f.path(verb), "")
			}
			if refused.Status != http.StatusConflict || f.text(refused.Body, "code") == "" {
				t.Errorf("失效现场 %s 未拒绝: %d %s", verb, refused.Status, refused.Raw)
			}
			if f.driver.SendCalls() != beforeSends || f.driver.SteerCalls() != beforeSteers || len(f.driver.Conversations()) != beforeOpens ||
				m2smSessionCount(t, f.store) != 1 {
				t.Error("失效门禁仍向 Scripted driver 发消息、启动 controller 或新建 Session")
			}
			if verb == "queue" || verb == "steer" {
				_, found, err := f.store.GetWorkflowInterventionByClientMessage(context.Background(), f.wid, "f03-refused-"+verb)
				if err != nil || found {
					t.Errorf("拒绝后仍写干预回执: found=%t err=%v", found, err)
				}
			}
		})
	}
}
