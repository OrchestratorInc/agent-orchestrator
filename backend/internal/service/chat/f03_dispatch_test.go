package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// TestM2F03QueuedDispatchRechecksWorkspace 删除 claim 前的工作区 guard 即变红。
// 通过真实 SQLite 队列确认等待期间失效会阻断派发，恢复后显式 drain 仍能发送。
func TestM2F03QueuedDispatchRechecksWorkspace(t *testing.T) {
	ctx := context.Background()
	st, conv := openStore(t), newFakeConversation()
	workspaceErr := errors.New("会话工作区已失效")
	var invalid atomic.Bool
	var ids atomic.Uint64
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("f03-%d", ids.Add(1)) },
	})
	ctrl, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
		ValidateWorkspace: func(context.Context) error {
			if invalid.Load() {
				return workspaceErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("启动 Controller: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	h := &harness{svc: svc, st: st, conv: conv, ctrl: ctrl}
	generation := ctrl.Generation()
	first, err := svc.SendPinned(ctx, testSession, generation, ports.ChatUserMessage{
		Text: "当前任务", ClientMessageID: "f03-current", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("发送当前任务: %v", err)
	}
	queued, err := svc.SendPinned(ctx, testSession, generation, ports.ChatUserMessage{
		Text: "等待派发", ClientMessageID: "f03-queued", Origin: domain.MessageOriginHuman,
	})
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("有效工作区入队: turn=%+v, err=%v", queued, err)
	}
	invalid.Store(true)
	conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: first.ProviderTurnID,
			TurnState: domain.TurnStateCompleted},
		// 后续活动投影构成屏障，确保完成事件的自动 drain 已执行。
		ports.ChatEvent{Kind: ports.ChatEventActivityCompleted, ProviderTurnID: first.ProviderTurnID,
			ProviderItemID: "f03-after-drain", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusCompleted, Summary: "完成事件已处理"},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, activity := range s.Activities {
			if activity.ProviderItemID == "f03-after-drain" {
				return true
			}
		}
		return false
	})
	if calls := conv.sendCallCount(); calls != 1 {
		t.Fatalf("工作区失效后 Provider 发送次数=%d，期望 1", calls)
	}
	batch, err := st.ListQueuedBatch(ctx, ctrl.ConversationID(), 0)
	if err != nil || len(batch) != 1 || batch[0].TurnID != queued.ID {
		t.Fatalf("拒绝派发后队列必须保留且未被 claim: batch=%+v, err=%v", batch, err)
	}
	if err := svc.DrainQueued(ctx, testSession); !errors.Is(err, workspaceErr) {
		t.Fatalf("失效工作区显式 drain: %v", err)
	}
	invalid.Store(false)
	if err := svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatalf("工作区恢复后显式 drain: %v", err)
	}
	row, err := st.TurnByID(ctx, queued.ID)
	if err != nil || row.State != domain.TurnStateRunning || row.ProviderTurnID == "" {
		t.Fatalf("恢复后原 queued turn 应派发: turn=%+v, err=%v", row, err)
	}
	if calls := conv.sendCallCount(); calls != 2 || ctrl.Generation() != generation {
		t.Fatalf("恢复派发不得重复或更换 generation: calls=%d, generation=%s", calls, ctrl.Generation())
	}

	// 新 Send/Retry 必须在接受前拒绝；已拒绝的请求不能等工作区恢复后自动执行。
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: row.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New("Provider 拒绝此任务")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ID == queued.ID {
				return turn.State == domain.TurnStateFailed
			}
		}
		return false
	})
	invalid.Store(true)
	if _, err := svc.RetryTurn(ctx, testSession, queued.ID); !errors.Is(err, workspaceErr) {
		t.Fatalf("失效工作区 retry 应拒绝: %v", err)
	}
	if _, found, err := st.RetryTurnIDForSource(ctx, ctrl.ConversationID(), queued.ID); err != nil || found {
		t.Fatalf("拒绝 retry 不得留下可执行新 turn: found=%v, err=%v", found, err)
	}
	rejected := ports.ChatUserMessage{Text: "不应执行的消息", ClientMessageID: "f03-rejected"}
	if turn, err := svc.SendPinned(ctx, testSession, generation, rejected); !errors.Is(err, workspaceErr) || turn.ID != "" {
		t.Fatalf("失效工作区 fresh Send 应在接受前拒绝: turn=%+v, err=%v", turn, err)
	}
	if _, found, err := st.ConversationMessageByClientID(ctx, ctrl.ConversationID(), rejected.ClientMessageID); err != nil || found {
		t.Fatalf("拒绝 Send 不得留下可执行新消息: found=%v, err=%v", found, err)
	}
	invalid.Store(false)
	if err := svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatalf("恢复后 drain: %v", err)
	}
	if calls := conv.sendCallCount(); calls != 2 {
		t.Fatalf("恢复后不得执行此前明确拒绝的 Send/Retry: calls=%d", calls)
	}
}

// TestM2F03BranchLaunchRechecksWorkspaceAfterPreparingEnv 删除共享启动准备中的 guard 即变红。
// 工作区在凭据准备期间失效时，目标分支与恢复源分支均不得调用 Provider Resume。
func TestM2F03BranchLaunchRechecksWorkspaceAfterPreparingEnv(t *testing.T) {
	ctx := context.Background()
	st, source := openStore(t), newFakeConversation()
	workspaceErr := errors.New("启动准备期间工作区失效")
	var invalid atomic.Bool
	var ids atomic.Uint64
	var prepares, checks, resumes atomic.Int32
	driver := fakeDriver{conv: source, resume: func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
		resumes.Add(1)
		provider := newFakeConversation()
		provider.providerConversationID = cfg.ProviderConversationID
		return provider, nil
	}}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: driver}, Log: slog.New(slog.DiscardHandler),
		NewID: func() string { return fmt.Sprintf("f03-branch-%d", ids.Add(1)) },
	})
	ctrl, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
		PrepareControllerEnv: func(context.Context, domain.SessionControllerOwner) (map[string]string, error) {
			if prepares.Add(1) > 1 {
				invalid.Store(true)
			}
			return nil, nil
		},
		ValidateWorkspace: func(context.Context) error {
			checks.Add(1)
			if invalid.Load() {
				return workspaceErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("启动源 Controller: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	h := &harness{svc: svc, st: st, conv: source, ctrl: ctrl}
	first := completeTurn(t, h, "源分支任务", "provider-turn-1")
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ID == first {
				return turn.State == domain.TurnStateCompleted
			}
		}
		return false
	})
	branch := snapshot.ActiveBranch
	branch.ID, branch.Active = "f03-inactive-branch", false
	branch.ParentBranchID = snapshot.ActiveBranch.ID
	branch.ReplacedTurnID = first
	branch.ProviderConversationID = "f03-target-thread"
	if err := st.CreateConversationBranch(ctx, branch, time.Now().UTC()); err != nil {
		t.Fatalf("创建待激活分支: %v", err)
	}
	before := checks.Load()
	if _, err := svc.ActivateBranch(ctx, testSession, branch.ID); !errors.Is(err, chatsvc.ErrWorkspaceInvalid) || !errors.Is(err, workspaceErr) {
		t.Fatalf("启动准备后应明确拒绝失效工作区: %v", err)
	}
	if calls := resumes.Load(); calls != 0 {
		t.Fatalf("失效工作区不得启动目标或恢复源 Provider: calls=%d", calls)
	}
	if calls := checks.Load() - before; calls < 2 {
		t.Fatalf("目标启动与源恢复均需重验: checks=%d", calls)
	}
}
