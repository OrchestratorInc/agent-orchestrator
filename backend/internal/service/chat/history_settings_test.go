package chat_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// TestHistoryReplacementPreservesPickedSettings 验证模型选择在编辑重发、后续发送和历史分支切换后仍到达供应商。
func TestHistoryReplacementPreservesPickedSettings(t *testing.T) {
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	completeTurn(t, h, "first", "provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 2 })
	second := completeTurn(t, h, "second", "provider-turn-2")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Messages) == 4 })

	picked := domain.ConversationSettings{
		Model: "picked-model", ReasoningEffort: "high", ApprovalMode: ports.PermissionModeAcceptEdits,
	}
	if _, err := h.svc.SetTurnSettings(ctx, testSession, picked); err != nil {
		t.Fatal(err)
	}

	checkSettings := func(want domain.ConversationSettings) {
		t.Helper()
		controller, err := h.svc.Controller(testSession)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
		if err != nil {
			t.Fatal(err)
		}
		if got := controller.Settings(); got != want {
			t.Errorf("控制器设置 = %+v，期望 %+v", got, want)
		}
		if got := snapshot.Conversation.Settings; got != want {
			t.Errorf("持久化设置 = %+v，期望 %+v", got, want)
		}
		driver.mu.Lock()
		resume := driver.resumeCalls[len(driver.resumeCalls)-1]
		driver.mu.Unlock()
		if resume.Model != want.Model || resume.Effort != want.ReasoningEffort || resume.Permissions != want.ApprovalMode {
			t.Errorf("供应商恢复设置 = %q/%q/%q，期望 %+v", resume.Model, resume.Effort, resume.Permissions, want)
		}
	}
	checkSend := func(provider *fakeConversation, want domain.ConversationSettings) {
		t.Helper()
		sent := provider.sentMessages()
		if len(sent) == 0 {
			t.Fatal("供应商未收到消息")
		}
		got := sent[len(sent)-1].Settings
		if got.Model != want.Model || got.Effort != want.ReasoningEffort || got.Approval != want.ApprovalMode {
			t.Errorf("实际发送设置 = %+v，期望 %+v", got, want)
		}
	}
	finish := func(provider *fakeConversation, turn domain.ConversationTurn) {
		t.Helper()
		provider.emit(
			ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID},
			ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted},
		)
		h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
			controller, err := h.svc.Controller(testSession)
			if err != nil || controller.State() != ports.ChatControllerReady {
				return false
			}
			for _, current := range s.Turns {
				if current.ID == turn.ID {
					return current.State.Terminal()
				}
			}
			return false
		})
	}

	edited, err := h.svc.EditMessage(ctx, testSession, second, ports.ChatUserMessage{
		Text: "edited", ClientMessageID: "settings-edit", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	replacement := driver.resumed["thread-forked"]
	driver.mu.Unlock()
	checkSettings(picked)
	checkSend(replacement, picked)
	finish(replacement, edited.Turn)

	next, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "continue", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	checkSend(replacement, picked)
	finish(replacement, next)

	picked.Model, picked.ReasoningEffort, picked.ApprovalMode = "branch-model", "medium", ports.PermissionModeAuto
	if _, err := h.svc.SetTurnSettings(ctx, testSession, picked); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ActivateBranch(ctx, testSession, edited.SourceBranchID); err != nil {
		t.Fatal(err)
	}
	checkSettings(picked)
	driver.mu.Lock()
	root := driver.resumed["thread-1"]
	driver.mu.Unlock()
	if sent := root.sentMessages(); len(sent) != 0 {
		t.Fatalf("切换分支不应发送消息，实际发送 %d 条", len(sent))
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "continue on original branch", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	checkSend(root, picked)
}
