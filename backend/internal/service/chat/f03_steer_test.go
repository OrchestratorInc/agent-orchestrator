package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// f03AckWaitContext 在交付查询后观察 ack 等待，避免用 sleep 猜测 goroutine 调度。
type f03AckWaitContext struct {
	context.Context
	armed   atomic.Bool
	entered chan struct{}
	once    sync.Once
}

// Done 只在真实 ack 等待开始时发布屏障，仍沿用原 context 的取消语义。
func (c *f03AckWaitContext) Done() <-chan struct{} {
	if c.armed.Load() {
		c.once.Do(func() { close(c.entered) })
	}
	return c.Context.Done()
}

// f03AckWaitStore 把屏障启用点放在交付查询后，排除前置数据库查询对 Done 的调用。
type f03AckWaitStore struct {
	chatsvc.Store
	ack *f03AckWaitContext
}

// SteerDelivery 读取真实 receipt；仅测试请求会启用下一次 Done 的 ack 屏障。
func (s *f03AckWaitStore) SteerDelivery(ctx context.Context, conversationID, clientMessageID string) (domain.ConversationSteerDelivery, bool, error) {
	delivery, found, err := s.Store.SteerDelivery(ctx, conversationID, clientMessageID)
	if ctx == s.ack {
		s.ack.armed.Store(true)
	}
	return delivery, found, err
}

// f03WorkspaceExpiresOnClaimStore 在真实预留队列后模拟外部工作区失效。
type f03WorkspaceExpiresOnClaimStore struct {
	chatsvc.Store
	invalid *atomic.Bool
}

// ClaimQueuedBatch 保留 SQLite 的真实 claim，仅在 fallback 预留后改变工作区观测。
func (s *f03WorkspaceExpiresOnClaimStore) ClaimQueuedBatch(ctx context.Context, conversationID string, dispatch bool, limit int, now time.Time) ([]domain.QueuedTurn, error) {
	batch, err := s.Store.ClaimQueuedBatch(ctx, conversationID, dispatch, limit, now)
	if err == nil && !dispatch {
		s.invalid.Store(true)
	}
	return batch, err
}

// TestM2F03SteerRechecksWorkspaceAfterAckWait 删除 ack 等待后的工作区 guard 即变红。
// 拒绝时不 claim queued 消息、不占用 client handle；恢复后同一请求可明确派发。
func TestM2F03SteerRechecksWorkspaceAfterAckWait(t *testing.T) {
	ctx := context.Background()
	st, provider := openStore(t), newSteerRecorder()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ack := &f03AckWaitContext{Context: waitCtx, entered: make(chan struct{})}
	workspaceErr := errors.New("ack 等待期间工作区失效")
	var invalid atomic.Bool
	var ids atomic.Uint64
	svc := chatsvc.New(chatsvc.Options{
		Store: &f03AckWaitStore{Store: st, ack: ack}, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("f03-steer-%d", ids.Add(1)) },
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
	generation := ctrl.Generation()
	first, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "当前任务", ClientMessageID: "f03-steer-current"})
	if err != nil {
		t.Fatalf("派发尚未 ack 的 turn: %v", err)
	}
	queued, err := svc.SendPinned(ctx, testSession, generation,
		ports.ChatUserMessage{Text: "等待中的任务", ClientMessageID: "f03-steer-queued"})
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("有效工作区入队: turn=%+v, err=%v", queued, err)
	}
	msg := ports.ChatUserMessage{Text: "修正当前任务", ClientMessageID: "f03-steer-guidance"}
	done := make(chan error, 1)
	go func() {
		_, steerErr := svc.Steer(ack, testSession, msg)
		done <- steerErr
	}()
	select {
	case <-ack.entered:
	case <-waitCtx.Done():
		t.Fatal("Steer 未进入 ack 等待")
	}
	invalid.Store(true)
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: first.ProviderTurnID})
	select {
	case err := <-done:
		if !errors.Is(err, chatsvc.ErrWorkspaceInvalid) || !errors.Is(err, workspaceErr) {
			t.Fatalf("ack 等待后应明确拒绝失效工作区: %v", err)
		}
	case <-waitCtx.Done():
		t.Fatal("Steer 在 ack 等待结束后未返回")
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("失效工作区不得向 Provider 派发 guidance: %+v", calls)
	}
	batch, err := st.ListQueuedBatch(ctx, ctrl.ConversationID(), 0)
	if err != nil || len(batch) != 1 || batch[0].TurnID != queued.ID {
		t.Fatalf("拒绝时 queued 消息不得被 claim: batch=%+v, err=%v", batch, err)
	}
	if _, found, err := st.SteerDelivery(ctx, ctrl.ConversationID(), msg.ClientMessageID); err != nil || found {
		t.Fatalf("拒绝时不得消耗 client handle: found=%v, err=%v", found, err)
	}
	invalid.Store(false)
	result, err := svc.SteerPinned(ctx, testSession, generation, first.ProviderTurnID, msg)
	if err != nil || result.ProviderTurnID != first.ProviderTurnID || result.ActivityID == "" {
		t.Fatalf("恢复后同一 client handle 应可派发: result=%+v, err=%v", result, err)
	}
	calls := provider.steers()
	if len(calls) != 1 || calls[0].turnID != first.ProviderTurnID || !strings.Contains(calls[0].msg.Text, "等待中的任务") {
		t.Fatalf("恢复后 guidance 与 queued 应仅派发一次且归属原 turn: %+v", calls)
	}
	row, err := st.TurnByID(ctx, queued.ID)
	if err != nil || row.State != domain.TurnStateCompleted || ctrl.Generation() != generation {
		t.Fatalf("恢复后应保持 follower 和 generation 语义: turn=%+v, err=%v", row, err)
	}
}

// TestM2F03FallbackRefusalPreservesAcceptedFollowers 删除 fallback 明确拒绝时的 release 分支即变红。
// 新消息在 claim 后被拒绝时，恢复只能发送此前已接受的 followers。
func TestM2F03FallbackRefusalPreservesAcceptedFollowers(t *testing.T) {
	ctx := context.Background()
	st, provider := openStore(t), newSteerRecorder()
	workspaceErr := errors.New("队列 claim 后工作区失效")
	var invalid atomic.Bool
	var ids atomic.Uint64
	svc := chatsvc.New(chatsvc.Options{
		Store:  &f03WorkspaceExpiresOnClaimStore{Store: st, invalid: &invalid},
		Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("f03-fallback-%d", ids.Add(1)) },
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
	h := &harness{svc: svc, st: st, conv: provider.fakeConversation, ctrl: ctrl}
	first, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "当前任务", ClientMessageID: "f03-fallback-current"})
	if err != nil {
		t.Fatalf("发送当前任务: %v", err)
	}
	queued, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "此前已接受的任务", ClientMessageID: "f03-fallback-queued"})
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("接受 queued 任务: turn=%+v, err=%v", queued, err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: first.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New("Provider 中止当前任务")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ID == first.ID {
				return turn.State == domain.TurnStateFailed
			}
		}
		return false
	})
	msg := ports.ChatUserMessage{Text: "应拒绝的新消息", ClientMessageID: "f03-fallback-rejected"}
	if _, err := svc.SteerOrSend(ctx, testSession, msg, false); !errors.Is(err, chatsvc.ErrWorkspaceInvalid) {
		t.Fatalf("claim 后失效应明确拒绝新消息: %v", err)
	}
	batch, err := st.ListQueuedBatch(ctx, ctrl.ConversationID(), 0)
	if err != nil || len(batch) != 1 || batch[0].TurnID != queued.ID {
		t.Fatalf("明确拒绝必须释放此前已接受的 followers: batch=%+v, err=%v", batch, err)
	}
	if _, found, err := st.ConversationMessageByClientID(ctx, ctrl.ConversationID(), msg.ClientMessageID); err != nil || found {
		t.Fatalf("拒绝的新消息不得留入队记录: found=%v, err=%v", found, err)
	}
	invalid.Store(false)
	if err := svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatalf("恢复后发送此前已接受的 followers: %v", err)
	}
	if calls := provider.sendCallCount(); calls != 2 {
		t.Fatalf("恢复后只能发送旧 queued 消息: calls=%d", calls)
	}
}
