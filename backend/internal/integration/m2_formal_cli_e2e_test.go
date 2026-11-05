package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestM2FormalCLIThroughReopenedSQLite 走生产 CLI/MCP 与真实 Manager、Git、SQLite。
// 删除 CLI 的 confirm 调用、Manager 的逐仓归属写入或现场核验时，本用例会失败。
func TestM2FormalCLIThroughReopenedSQLite(t *testing.T) {
	for _, name := range []string{"AO_SESSION_ID", "AO_ATTEMPT_ID", "AO_WORKFLOW_ID", "AO_WORKFLOW_LEASE", "AO_BROWSER_CAPABILITY"} {
		t.Setenv(name, "")
	}
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	plant := m2ffPlant{store: st, dataDir: dataDir, managedDir: t.TempDir(),
		repoA: m2ffRepo(t, "formal-alpha"), repoB: m2ffRepo(t, "formal-bravo")}
	f := newM2FFFixture(t, plant)
	pinA := strings.TrimSpace(m2smGit(t, plant.repoA, "rev-parse", "HEAD~1"))
	pinB := strings.TrimSpace(m2smGit(t, plant.repoB, "rev-parse", "HEAD~2"))
	if pinA == pinB {
		t.Fatal("两个仓库必须具有不同历史 SHA")
	}
	wid, tid := f.createWorkflowWithPins("formal", "work", pinA, pinB)
	aid, lease := f.claimAndStart(wid, tid, "formal")
	approvalsBeforeBind := len(f.consent.approvals())
	bindArgs := []string{"bind", "--workflow", string(wid), "--attempt", string(aid),
		"--harness", string(m2smDriverHarness), "--mode", "chat", "--prompt", "work", "--idempotency-key", "formal-bind"}

	// 清空 Agent 环境仍须原生人工确认；拒绝不得写 binding 或创建 worktree。
	f.consent.deny(principal.ErrConsentDenied)
	if _, err := m2RunOperator(t, f, bindArgs...); err == nil || !strings.Contains(err.Error(), principal.CodeApprovalRequired) {
		t.Fatalf("OS 拒绝未透传: %v", err)
	}
	if _, found, err := st.GetWorkflowAttemptBinding(ctx, aid); err != nil || found {
		t.Fatalf("拒绝后产生 binding: found=%t err=%v", found, err)
	}
	if len(f.sessionsFor()) != 0 || dirCount(t, plant.managedDir) != 0 {
		t.Fatal("拒绝产生资源")
	}
	f.consent.deny(nil)
	bound := m2MustOperator(t, f, bindArgs...)
	if !boolean(bound["spawned"]) || str(dig(bound, "binding", "state")) != string(domain.WorkflowBindingStateBoundReady) {
		t.Fatalf("CLI Bind 未就绪: %v", bound)
	}
	sid := domain.SessionID(str(dig(bound, "binding", "sessionId")))
	dirs := dirCount(t, plant.managedDir)
	registered := map[string]string{}
	for _, repo := range []string{plant.repoA, plant.repoB} {
		registered[repo] = m2smGit(t, repo, "worktree", "list", "--porcelain")
	}
	// 逐仓检查实际注册清单，避免只数 managed root 的项目目录而漏掉额外 worktree。
	verifyRegistrations := func() {
		t.Helper()
		for repo, before := range registered {
			if after := m2smGit(t, repo, "worktree", "list", "--porcelain"); after != before {
				t.Fatalf("%s 的 worktree 注册清单改变:\n之前:%s\n之后:%s", repo, before, after)
			}
		}
	}
	duplicate := m2MustOperator(t, f, bindArgs...)
	if boolean(duplicate["spawned"]) || str(dig(duplicate, "binding", "sessionId")) != string(sid) ||
		len(f.sessionsFor()) != 1 || dirCount(t, plant.managedDir) != dirs {
		t.Fatalf("重复 Bind 创建资源: %v", duplicate)
	}
	verifyRegistrations()
	seen := f.consent.approvals()[approvalsBeforeBind:]
	if len(seen) != 3 || seen[1].Payload != seen[2].Payload || !strings.Contains(seen[1].Payload, "formal-bind") {
		t.Fatalf("正式 CLI 未逐次审批相同的精确正文: %v", seen)
	}

	// SQLite 归属和实际 Git HEAD 必须同时正确，重启后复用相同断言。
	verifyRepos := func(f *m2ffFixture) {
		t.Helper()
		rows, bindings := f.worktrees(sid), f.repoBindings(aid)
		if len(rows) != 2 || len(bindings) != 2 {
			t.Fatalf("仓库数量: rows=%d bindings=%d", len(rows), len(bindings))
		}
		for _, expected := range []struct {
			key, repo, pin string
			project        domain.ProjectID
		}{
			{"repo-a", plant.repoA, pinA, m2ffProjectA}, {"repo-b", plant.repoB, pinB, m2ffProjectB},
		} {
			row := rows[expected.key]
			if row.ProjectID != expected.project || filepath.Clean(row.RepoPath) != filepath.Clean(expected.repo) ||
				row.BaseSHA != expected.pin || row.CreationSHA != expected.pin {
				t.Fatalf("归属/基线错误: %+v", row)
			}
			if got := strings.TrimSpace(m2smGit(t, row.WorktreePath, "rev-parse", "HEAD")); got != expected.pin {
				t.Fatalf("%s HEAD=%s want=%s", expected.key, got, expected.pin)
			}
			if !worktreeListedIn(t, expected.repo, row.WorktreePath) {
				t.Fatalf("%s 未注册到原仓库", row.WorktreePath)
			}
			if b := bindings[domain.WorkflowRepoKey(expected.key)]; b.ProjectID != expected.project || b.ExpectedBaseSHA != expected.pin {
				t.Fatalf("attempt 仓库归属错误: %+v", b)
			}
		}
	}
	verifyRepos(f)

	// 真正 MCP stdio 入口读取 Session 能力与租约，服务端派生 Agent 身份。
	t.Run("MCP协作", func(t *testing.T) {
		mcpEnvVars(t, m2MCPEnv{sessionID: string(sid), workflowID: string(wid), attemptID: string(aid), lease: lease, capability: f.issueAgentCapability(sid)})
		deps := mcpDeps(t, &m2Env{srv: f.srv})
		for _, call := range []string{
			mcpCallLine(t, 1, "workflow_message_send", map[string]any{"kind": "progress", "body": "half done", "clientMessageId": "formal-message"}),
			mcpCallLine(t, 2, "workflow_task_request_create", map[string]any{"requestId": "formal-request", "requestKind": "increase_budget", "payload": map[string]any{"credits": 10}}),
		} {
			replies := runRealMCP(t, deps, call)
			if len(replies) != 1 || replies[0].Error != nil || replies[0].Result.IsError {
				t.Fatalf("MCP 失败: %+v", replies)
			}
		}
	})
	messages, err := st.ListWorkflowMessages(ctx, wid, 100, 0)
	if err != nil || len(messages) != 1 || messages[0].SenderKind != domain.WorkflowMessageSenderAgent ||
		messages[0].SenderTaskID == nil || *messages[0].SenderTaskID != tid {
		t.Fatalf("MCP 持久化身份: %+v err=%v", messages, err)
	}
	request, found, err := st.GetWorkflowTaskRequest(ctx, wid, "formal-request")
	if err != nil || !found {
		t.Fatalf("MCP 请求未持久化: %t %v", found, err)
	}
	auditsBeforeDecide := len(f.audits(wid))
	m2MustOperator(t, f, "decide", "--workflow", string(wid), "--request", request.ID, "--approve",
		"--expected-revision", fmt.Sprint(request.Revision))
	audits := f.audits(wid)
	if len(audits) != auditsBeforeDecide+1 || audits[len(audits)-1].Decision != "decide_task_request" || audits[len(audits)-1].TokenFingerprint != principal.NativeConsentIdentity {
		t.Fatalf("CLI Decide 缺少原生审批审计: %+v", audits)
	}

	// 不改 SHA，仅替换实际分支，检查 Reconcile 不能信任缓存的 creation_sha。
	row := f.worktrees(sid)["repo-b"]
	originalBranch := strings.TrimSpace(m2smGit(t, row.WorktreePath, "branch", "--show-current"))
	m2smGit(t, row.WorktreePath, "switch", "-c", "formal-tampered")
	if _, err := m2RunOperator(t, f, "reconcile", "--workflow", string(wid), "--attempt", string(aid)); err == nil || !strings.Contains(err.Error(), "SESSION_NOT_READY") {
		t.Fatalf("篡改现场未被 CLI 闭锁: %v", err)
	}
	uncertain := f.binding(aid)
	if uncertain.State != domain.WorkflowBindingStateRecoveryUncertain || uncertain.SessionID == nil || *uncertain.SessionID != sid {
		t.Fatalf("篡改后未保留原 Session 并标记不确定: %+v", uncertain)
	}
	m2smGit(t, row.WorktreePath, "switch", originalBranch)
	for _, verb := range []string{"resume", "reconcile"} {
		result := m2MustOperator(t, f, verb, "--workflow", string(wid), "--attempt", string(aid))
		if boolean(result["spawned"]) || str(dig(result, "binding", "sessionId")) != string(sid) {
			t.Fatalf("%s 新建 Session: %v", verb, result)
		}
		if verb == "resume" && (boolean(result["frozen"]) || str(dig(result, "binding", "state")) != string(domain.WorkflowBindingStateBoundReady)) {
			t.Fatalf("恢复现场后未就绪: %v", result)
		}
	}

	// 关闭连接并重新打开同一个 SQLite 文件，同时重建 daemon 服务和内存审批表。
	f.stop()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	plant.store = reopened
	second := newM2FFFixture(t, plant)
	verifyRepos(second)
	for _, verb := range []string{"resume", "reconcile"} {
		result := m2MustOperator(t, second, verb, "--workflow", string(wid), "--attempt", string(aid))
		if boolean(result["spawned"]) || str(dig(result, "binding", "sessionId")) != string(sid) {
			t.Fatalf("重启后 %s 归属错误: %v", verb, result)
		}
		if verb == "resume" && (boolean(result["frozen"]) || str(dig(result, "binding", "state")) != string(domain.WorkflowBindingStateBoundReady)) {
			t.Fatalf("重启后恢复未就绪: %v", result)
		}
	}
	verifyRepos(second)
	if len(second.sessionsFor()) != 1 || dirCount(t, plant.managedDir) != dirs {
		t.Fatal("重启恢复创建额外资源")
	}
	verifyRegistrations()
	if _, err := reopened.RecoverExpiredWorkflowAttempts(ctx, time.Now().UTC().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	task, found, err := reopened.GetWorkflowTask(ctx, wid, tid)
	if err != nil || !found {
		t.Fatalf("读取待裁决任务: %t %v", found, err)
	}
	auditsBeforeResolve := len(second.audits(wid))
	resolved := m2MustOperator(t, second, "resolve", "--workflow", string(wid), "--attempt", string(aid),
		"--outcome", "succeeded", "--expected-task-revision", fmt.Sprint(task.Revision), "--idempotency-key", "formal-resolve")
	if str(dig(resolved, "attempt", "state")) != string(domain.WorkflowAttemptStateSucceeded) {
		t.Fatalf("CLI Resolve 失败: %v", resolved)
	}
	if rows := second.audits(wid); len(rows) != auditsBeforeResolve+1 || rows[len(rows)-1].Decision != "resolve_attempt" || rows[len(rows)-1].TokenFingerprint != principal.NativeConsentIdentity {
		t.Fatalf("重开 SQLite 后审计错误: %+v", rows)
	}
}
