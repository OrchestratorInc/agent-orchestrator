package integration

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM2F03QueuedTurnRechecksWorkspace 覆盖排队后失效的窗口；删除自动 drain 的核验即变红。
// 失败回调事件和同步 DrainQueued 组成 dispatch barrier，不能用 sleep 推测第二条没有发出。
func TestM2F03QueuedTurnRechecksWorkspace(t *testing.T) {
	f := newM2F03Flow(t, "f03-queued-gate")
	ctx := context.Background()
	select {
	case err := <-f.workspaceChecks:
		if err != nil {
			t.Fatalf("真实 Manager 初始验证失败: %v", err)
		}
	default:
		t.Fatal("Manager 未向 StartChat 注入真实 ValidateWorkspace 回调")
	}
	conversation, err := f.store.ConversationForSession(ctx, f.sid)
	if err != nil {
		t.Fatal(err)
	}
	first, _, active := f.chat.RunningProviderTurn(ctx, f.sid)
	conversations := f.driver.Conversations()
	if !active || first == "" || len(conversations) != 1 || f.driver.SendCalls() != 1 {
		t.Fatal("必须有一个真实 Scripted turn 正在运行")
	}
	queued := f.do(http.MethodPost, f.path("interventions"),
		`{"mode":"queue","text":"工作区恢复后继续","clientMessageId":"f03-held-queue"}`)
	if queued.Status != http.StatusOK || f.text(queued.Body, "intervention", "state") != string(domain.WorkflowInterventionStateQueued) {
		t.Fatalf("真实 HTTP Queue 未保存: %d %s", queued.Status, queued.Raw)
	}
	pending, err := f.store.NextQueuedTurn(ctx, conversation.ID)
	if err != nil || pending.TurnID == "" || pending.ClientMessageID != "f03-held-queue" {
		t.Fatalf("SQLite 未保留排队消息: %+v err=%v", pending, err)
	}
	for len(f.workspaceChecks) > 0 {
		if err := <-f.workspaceChecks; err != nil {
			t.Fatalf("工作区失效前验证失败: %v", err)
		}
	}
	// 重命名保留完整现场，第一次完成事件只能触发只读拒绝，不能消费第二条。
	saved := f.row.WorktreePath + "-saved"
	marker := filepath.Join(f.row.WorktreePath, "history-0.txt")
	note := filepath.Join(f.row.WorktreePath, "user-dirty.txt")
	const content = "排队期间保留用户修改\n"
	m2f03Write(t, marker, []byte(content))
	m2f03Write(t, note, []byte(content))
	m2f03Move(t, f.row.WorktreePath, saved)
	restored := false
	defer func() {
		if !restored {
			m2f03Move(t, saved, f.row.WorktreePath)
		}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conversations[0].CompleteTurn(first)
	for {
		select {
		case err := <-f.workspaceChecks:
			if err != nil {
				goto checked
			}
		case <-waitCtx.Done():
			t.Fatal("自动 drain 未经过真实工作区失败核验")
		}
	}

checked:
	// drainOne 使用与完成事件相同的 sendMu，返回即证明自动 dispatch 已离开临界区。
	if err := f.chat.DrainQueued(waitCtx, f.sid); err == nil {
		t.Fatal("失效目录上的同步 drain 未拒绝")
	}
	completed, err := f.store.TurnByProviderID(ctx, conversation.ID, first)
	if err != nil || completed.State != domain.TurnStateCompleted {
		t.Fatalf("第一条未真实完成: %+v err=%v", completed, err)
	}
	held, err := f.store.TurnByID(ctx, pending.TurnID)
	if err != nil || held.State != domain.TurnStateQueued || held.ProviderTurnID != "" || f.driver.SendCalls() != 1 {
		t.Fatalf("失效目录仍消费第二条: turn=%+v sends=%d err=%v", held, f.driver.SendCalls(), err)
	}
	retained, err := f.store.NextQueuedTurn(ctx, conversation.ID)
	if err != nil || retained.TurnID != pending.TurnID || retained.Text != pending.Text {
		t.Fatalf("失败验证改变 durable queue: %+v err=%v", retained, err)
	}
	m2f03Move(t, saved, f.row.WorktreePath)
	restored = true
	if err := f.chat.DrainQueued(ctx, f.sid); err != nil {
		t.Fatalf("恢复现场后 drain 失败: %v", err)
	}
	if f.driver.SendCalls() != 2 {
		t.Fatalf("恢复后应恰好发送第二条一次: sends=%d", f.driver.SendCalls())
	}
	if _, err := f.store.NextQueuedTurn(ctx, conversation.ID); !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatalf("发送后 durable queue 未消费: %v", err)
	}
	for _, path := range []string{marker, note} {
		if got, err := os.ReadFile(path); err != nil || string(got) != content {
			t.Fatalf("队列闭锁破坏 dirty 内容: %q err=%v", got, err)
		}
	}
}
