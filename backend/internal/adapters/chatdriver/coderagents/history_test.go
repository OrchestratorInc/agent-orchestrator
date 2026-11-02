package coderagents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func nativeText(id int64, role, text string) coderapi.ChatMessage {
	return coderapi.ChatMessage{ID: id, Role: role, Content: []coderapi.ChatMessagePart{{Type: coderapi.PartText, Text: text}}}
}

func sameHistoryFacts(left, right []ports.ChatEvent) bool {
	return slices.EqualFunc(left, right, func(a, b ports.ChatEvent) bool {
		return a.ProviderEventID == b.ProviderEventID && a.ProviderTurnID == b.ProviderTurnID &&
			a.ProviderItemID == b.ProviderItemID && a.Kind == b.Kind && a.Text == b.Text && a.TurnState == b.TurnState
	})
}

func resumedHistory(t *testing.T, f *fakeCoder, key, workspace, dataDir string) (ports.ChatConversation, []ports.ChatEvent) {
	t.Helper()
	conv, err := newDriver(f, nil).Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "s1", ProviderConversationID: key, DataDir: dataDir,
		WorkspacePath: workspace, HasPriorTurns: true,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	reader, ok := conv.(ports.ChatHistoryReader)
	if !ok {
		t.Fatal("resumed Coder conversation has no native history reader")
	}
	events, err := reader.ReadHistory(context.Background())
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	again, err := reader.ReadHistory(context.Background())
	if err != nil || !sameHistoryFacts(events, again) {
		t.Fatalf("repeated history observation changed: %v", err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		if event.ProviderEventID == "" || seen[event.ProviderEventID] {
			t.Fatalf("missing or duplicate replay identity: %+v", event)
		}
		seen[event.ProviderEventID] = true
	}
	return conv, events
}

func TestResumeRecoversReplyCompletedWhileDetached(t *testing.T) {
	f := newFakeCoder(t)
	first, workspace := startConversation(t, f, ports.PermissionModeDefault)
	sendTurn(t, first, "hello")
	key, dataDir := first.ProviderConversationID(), first.(*conversation).cfg.dataDir
	_ = first.Close()
	f.mu.Lock()
	f.history = []coderapi.ChatMessage{nativeText(1, coderapi.RoleUser, "hello"), nativeText(2, coderapi.RoleAssistant, "finished while AO was closed")}
	f.chats["chat-1"].Status = coderapi.ChatStatusWaiting
	f.mu.Unlock()
	resumed, events := resumedHistory(t, f, key, workspace, dataDir)
	var user, answer, completed int
	for _, event := range events {
		switch event.Kind {
		case ports.ChatEventUserMessageCompleted:
			if event.Text == "hello" {
				user++
			}
		case ports.ChatEventMessageCompleted:
			if event.Text == "finished while AO was closed" {
				answer++
			}
		case ports.ChatEventTurnCompleted:
			completed++
		}
	}
	if user != 1 || answer != 1 || completed != 1 {
		t.Fatalf("replay recovered users=%d answers=%d completed=%d, want one each", user, answer, completed)
	}
	_ = resumed.Close()
	_, afterRestart := resumedHistory(t, f, key, workspace, dataDir)
	if !sameHistoryFacts(events, afterRestart) {
		t.Fatal("a second resume changed the native replay identities")
	}
}

func TestResumeHistoryReadsBeyondTheNewestPage(t *testing.T) {
	f := newFakeCoder(t)
	key := "8f9d2f8a-3a43-4a5c-9b0e-1f1f2a3b4c5d"
	f.chats["c"] = &coderapi.Chat{ID: "c", OwnerID: "u1", Status: coderapi.ChatStatusWaiting,
		Labels: map[string]string{labelConversation: key, labelSession: "s1"}}
	for id := int64(1); id <= 260; id++ {
		role := coderapi.RoleAssistant
		if id%2 == 1 {
			role = coderapi.RoleUser
		}
		f.history = append(f.history, nativeText(id, role, fmt.Sprintf("message %d", id)))
	}
	_, events := resumedHistory(t, f, key, t.TempDir(), t.TempDir())
	var messages []string
	for _, event := range events {
		if event.Kind == ports.ChatEventMessageCompleted || event.Kind == ports.ChatEventUserMessageCompleted {
			messages = append(messages, event.Text)
		}
	}
	if len(messages) != 260 || messages[0] != "message 1" || messages[259] != "message 260" {
		t.Fatalf("history recovered %d messages, want all 260 in native order", len(messages))
	}
}

func TestActiveResumeSharesTurnWithImportedOutput(t *testing.T) {
	f := newFakeCoder(t)
	key := "8f9d2f8a-3a43-4a5c-9b0e-1f1f2a3b4c5d"
	f.chats["c"] = &coderapi.Chat{ID: "c", OwnerID: "u1", Status: coderapi.ChatStatusRunning,
		Labels: map[string]string{labelConversation: key, labelSession: "s1"}}
	f.history = []coderapi.ChatMessage{nativeText(1, coderapi.RoleUser, "work"), nativeText(2, coderapi.RoleAssistant, "first step")}
	conv, events := resumedHistory(t, f, key, t.TempDir(), t.TempDir())
	started := nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	var previousItem string
	for _, event := range events {
		if event.ProviderTurnID != started.ProviderTurnID {
			t.Fatalf("imported output belongs to %q, live turn is %q", event.ProviderTurnID, started.ProviderTurnID)
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			t.Fatal("history marked an active turn complete")
		}
		if event.Kind == ports.ChatEventMessageCompleted {
			previousItem = event.ProviderItemID
		}
	}
	finalMessage := nativeText(3, coderapi.RoleAssistant, "done")
	f.push <- []coderapi.StreamEvent{
		{Type: coderapi.EventMessage, Message: &finalMessage},
		status(coderapi.ChatStatusWaiting),
	}
	answer := nextEvent(t, conv.Events(), ports.ChatEventMessageCompleted)
	if answer.Text != "done" || answer.ProviderTurnID != started.ProviderTurnID || answer.ProviderItemID == previousItem {
		t.Fatalf("continued output = %+v; must retain the imported message", answer)
	}
	nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
}

func TestHistoryImportDoesNotExecuteRecordedTools(t *testing.T) {
	f := newFakeCoder(t)
	key := "8f9d2f8a-3a43-4a5c-9b0e-1f1f2a3b4c5d"
	f.chats["c"] = &coderapi.Chat{ID: "c", OwnerID: "u1", Status: coderapi.ChatStatusWaiting,
		Labels: map[string]string{labelConversation: key, labelSession: "s1"}}
	f.history = []coderapi.ChatMessage{
		nativeText(1, coderapi.RoleUser, "write a file"),
		{ID: 2, Role: coderapi.RoleAssistant, Content: []coderapi.ChatMessagePart{{
			Type: coderapi.PartToolCall, ToolCallID: "old-call", ToolName: toolWrite,
			Args: []byte(`{"path":"out.txt","content":"old content"}`),
		}}},
		{ID: 3, Role: coderapi.RoleTool, Content: []coderapi.ChatMessagePart{{
			Type: coderapi.PartToolResult, ToolCallID: "old-call", ToolName: toolWrite, Result: []byte(`{"status":"added"}`),
		}}},
		nativeText(4, coderapi.RoleAssistant, "done"),
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "out.txt")
	if err := os.WriteFile(path, []byte("user's later edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, events := resumedHistory(t, f, key, workspace, t.TempDir())
	completed := false
	for _, event := range events {
		if event.Kind == ports.ChatEventActivityCompleted && event.ProviderItemID == toolItemID("old-call") {
			completed = true
		}
	}
	if !completed {
		t.Fatal("recorded tool result was omitted from native history")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "user's later edit" {
		t.Fatalf("history replay changed the worktree: %q, %v", data, err)
	}
	if _, _, results, _ := f.snapshot(); len(results) != 0 {
		t.Fatal("history replay submitted a tool result to Coder")
	}
}
