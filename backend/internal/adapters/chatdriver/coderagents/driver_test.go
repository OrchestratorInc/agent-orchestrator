package coderagents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func startConversation(t *testing.T, f *fakeCoder, permissions ports.PermissionMode) (ports.ChatConversation, string) {
	t.Helper()
	worktree := t.TempDir()
	conv, err := New(f, nil).Start(context.Background(), ports.ChatStartConfig{
		SessionID: "s1", DataDir: t.TempDir(), WorkspacePath: worktree,
		Permissions: permissions, SystemPrompt: "AO STANDING INSTRUCTIONS",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	return conv, worktree
}

func sendTurn(t *testing.T, conv ports.ChatConversation, text string) string {
	t.Helper()
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: text})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
	return ref.ProviderTurnID
}

// The first turn creates a Coder chat bound to no workspace, with AO's
// instructions in the hidden system prompt and only AO's local tools.
func TestFirstTurnCreatesLocalOnlyChat(t *testing.T) {
	f := newFakeCoder(t)
	conv, worktree := startConversation(t, f, ports.PermissionModeBypassPermissions)
	turnID := sendTurn(t, conv, "hello")

	creates, _, _, _ := f.snapshot()
	if len(creates) != 1 {
		t.Fatalf("created %d chats, want 1", len(creates))
	}
	body := creates[0]
	if _, bound := body["workspace_id"]; bound {
		t.Fatal("chat was created with a workspace")
	}
	prompt, _ := body["system_prompt"].(string)
	if !strings.Contains(prompt, "AO STANDING INSTRUCTIONS") || !strings.Contains(prompt, worktree) {
		t.Fatalf("system prompt = %q", prompt)
	}
	if content, _ := body["content"].([]any); len(content) != 1 || strings.Contains(prompt, "hello") {
		t.Fatalf("content = %v; the user text must not leak into the system prompt", body["content"])
	}
	labels, _ := body["labels"].(map[string]any)
	if labels[labelConversation] != conv.ProviderConversationID() || labels[labelSession] != "s1" {
		t.Fatalf("labels = %v", labels)
	}
	tools, _ := body["unsafe_dynamic_tools"].([]any)
	if len(tools) != len(toolSpecs) {
		t.Fatalf("declared %d tools, want %d", len(tools), len(toolSpecs))
	}

	f.push <- []coderapi.StreamEvent{
		status(coderapi.ChatStatusRunning),
		{Type: coderapi.EventMessagePart, MessagePart: &coderapi.StreamMessagePart{Role: coderapi.RoleAssistant, Part: coderapi.ChatMessagePart{Type: coderapi.PartText, Text: "Hi"}}},
		{Type: coderapi.EventMessage, Message: &coderapi.ChatMessage{ID: 2, Role: coderapi.RoleAssistant, Content: []coderapi.ChatMessagePart{{Type: coderapi.PartText, Text: "Hi there"}}}},
		status(coderapi.ChatStatusWaiting),
	}
	delta := nextEvent(t, conv.Events(), ports.ChatEventMessageDelta)
	completed := nextEvent(t, conv.Events(), ports.ChatEventMessageCompleted)
	if delta.Delta != "Hi" || completed.Text != "Hi there" || completed.ProviderItemID != delta.ProviderItemID {
		t.Fatalf("delta %+v, completed %+v", delta, completed)
	}
	done := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted)
	if done.ProviderTurnID != turnID || done.TurnState != domain.TurnStateCompleted {
		t.Fatalf("turn completion = %+v", done)
	}

	// A follow-up is a message on the same chat, not a new chat.
	sendTurn(t, conv, "again")
	creates, sent, _, _ := f.snapshot()
	if len(creates) != 1 || len(sent) != 1 {
		t.Fatalf("follow-up created %d chats and sent %d messages", len(creates), len(sent))
	}
}

// A client-executed tool runs in the AO worktree, its result goes back once,
// and a replayed action_required never runs it again.
func TestToolCallRunsInWorktreeOnceAcrossReplay(t *testing.T) {
	f := newFakeCoder(t)
	conv, worktree := startConversation(t, f, ports.PermissionModeBypassPermissions)
	sendTurn(t, conv, "write a file")
	call := coderapi.ToolCall{ToolCallID: "call-1", ToolName: toolWrite, Args: `{"path":"out.txt","content":"BANANA"}`}
	f.push <- []coderapi.StreamEvent{status(coderapi.ChatStatusRequiresAction), actionRequired(call)}

	results := f.waitResults(t, 1)
	if len(results[0]) != 1 || results[0][0].ToolCallID != "call-1" || results[0][0].IsError {
		t.Fatalf("results = %+v", results)
	}
	data, err := os.ReadFile(filepath.Join(worktree, "out.txt"))
	if err != nil || string(data) != "BANANA" {
		t.Fatalf("worktree file = %q, %v", data, err)
	}
	nextEvent(t, conv.Events(), ports.ChatEventActivityCompleted)

	if err := os.WriteFile(filepath.Join(worktree, "out.txt"), []byte("CHANGED"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.push <- []coderapi.StreamEvent{actionRequired(call)}
	time.Sleep(300 * time.Millisecond)
	if _, _, results, _ := f.snapshot(); len(results) != 1 {
		t.Fatalf("replayed call was answered again: %d batches", len(results))
	}
	if data, _ := os.ReadFile(filepath.Join(worktree, "out.txt")); string(data) != "CHANGED" {
		t.Fatalf("replayed call ran again: file = %q", data)
	}
}

// Ask-first mode parks a command on an AO approval; a rejection returns an
// error result to Coder and the command never runs.
func TestDefaultModeRejectionBlocksCommand(t *testing.T) {
	f := newFakeCoder(t)
	conv, worktree := startConversation(t, f, ports.PermissionModeDefault)
	sendTurn(t, conv, "run something")
	f.push <- []coderapi.StreamEvent{actionRequired(coderapi.ToolCall{
		ToolCallID: "call-2", ToolName: toolExecute, Args: `{"command":"touch ran.txt"}`,
	})}
	approval := nextEvent(t, conv.Events(), ports.ChatEventApprovalRequested)
	if approval.ActivityKind != domain.ActivityKindCommand || len(approval.Decisions) != 2 {
		t.Fatalf("approval = %+v", approval)
	}
	if err := conv.ResolveRequest(context.Background(), approval.RequestID, ports.ChatDecision{ID: "bogus"}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("unknown decision = %v", err)
	}
	if err := conv.ResolveRequest(context.Background(), approval.RequestID, ports.ChatDecision{ID: decisionReject}); err != nil {
		t.Fatal(err)
	}
	results := f.waitResults(t, 1)
	if !results[0][0].IsError {
		t.Fatalf("rejected call returned success: %+v", results[0][0])
	}
	if _, err := os.Stat(filepath.Join(worktree, "ran.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected command ran: %v", err)
	}
}

// Accept-edits applies file changes without asking.
func TestAcceptEditsWritesWithoutApproval(t *testing.T) {
	f := newFakeCoder(t)
	conv, worktree := startConversation(t, f, ports.PermissionModeAcceptEdits)
	sendTurn(t, conv, "edit")
	f.push <- []coderapi.StreamEvent{actionRequired(coderapi.ToolCall{
		ToolCallID: "call-3", ToolName: toolWrite, Args: `{"path":"a/b.txt","content":"ok"}`,
	})}
	f.waitResults(t, 1)
	if data, err := os.ReadFile(filepath.Join(worktree, "a", "b.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("file = %q, %v", data, err)
	}
}

// A Coder built-in that would provision a workspace fails the conversation
// closed: the run is interrupted and no further turn is accepted.
func TestWorkspaceToolFailsClosed(t *testing.T) {
	f := newFakeCoder(t)
	conv, _ := startConversation(t, f, ports.PermissionModeBypassPermissions)
	sendTurn(t, conv, "do it")
	f.push <- []coderapi.StreamEvent{
		status(coderapi.ChatStatusRunning),
		{Type: coderapi.EventMessagePart, MessagePart: &coderapi.StreamMessagePart{Role: coderapi.RoleAssistant, Part: coderapi.ChatMessagePart{
			Type: coderapi.PartToolCall, ToolCallID: "ws-1", ToolName: "create_workspace", Args: []byte(`{"template":"x"}`),
		}}},
	}
	errEvent := nextEvent(t, conv.Events(), ports.ChatEventError)
	if errEvent.Err == nil || !strings.Contains(errEvent.Err.Error(), "create_workspace") {
		t.Fatalf("error event = %+v", errEvent)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, interrupts := f.snapshot(); interrupts > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, _, interrupts := f.snapshot(); interrupts == 0 {
		t.Fatal("run was not interrupted")
	}
	f.push <- []coderapi.StreamEvent{status(coderapi.ChatStatusWaiting)}
	if done := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted); done.TurnState != domain.TurnStateFailed {
		t.Fatalf("turn state = %q, want failed", done.TurnState)
	}
	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "more"}); err == nil {
		t.Fatal("broken conversation accepted another turn")
	}
}

// A workspace Coder bound during a turn is detected when the turn settles.
func TestWorkspaceBoundDuringTurnFailsTurn(t *testing.T) {
	f := newFakeCoder(t)
	conv, _ := startConversation(t, f, ports.PermissionModeBypassPermissions)
	sendTurn(t, conv, "go")
	workspace := "ws-uuid"
	f.mu.Lock()
	f.chats["chat-1"].WorkspaceID = &workspace
	f.mu.Unlock()
	f.push <- []coderapi.StreamEvent{status(coderapi.ChatStatusRunning), status(coderapi.ChatStatusWaiting)}
	if done := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted); done.TurnState != domain.TurnStateFailed {
		t.Fatalf("turn state = %q, want failed", done.TurnState)
	}
}

func TestInterruptCallsCoderAndSettlesWaitingRun(t *testing.T) {
	f := newFakeCoder(t)
	f.interruptStatus = coderapi.ChatStatusWaiting
	conv, _ := startConversation(t, f, ports.PermissionModeBypassPermissions)
	turnID := sendTurn(t, conv, "long task")
	if err := conv.Interrupt(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	if done := nextEvent(t, conv.Events(), ports.ChatEventTurnCompleted); done.TurnState != domain.TurnStateInterrupted {
		t.Fatalf("turn state = %q, want interrupted", done.TurnState)
	}
	if err := conv.Interrupt(context.Background(), ""); !errors.Is(err, ports.ErrChatNoActiveTurn) {
		t.Fatalf("second interrupt = %v", err)
	}
}

func TestResumeRefusesMissingForeignOrWorkspaceBoundChats(t *testing.T) {
	key := "8f9d2f8a-3a43-4a5c-9b0e-1f1f2a3b4c5d"
	resume := func(f *fakeCoder, prior bool) error {
		_, err := New(f, nil).Resume(context.Background(), ports.ChatResumeConfig{
			SessionID: "s1", ProviderConversationID: key, DataDir: t.TempDir(),
			WorkspacePath: t.TempDir(), HasPriorTurns: prior,
		})
		return err
	}
	if err := resume(newFakeCoder(t), true); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("missing chat = %v", err)
	}
	foreign := newFakeCoder(t)
	foreign.chats["c"] = &coderapi.Chat{ID: "c", OwnerID: "u1", Status: coderapi.ChatStatusWaiting,
		Labels: map[string]string{labelConversation: key, labelSession: "other"}}
	if err := resume(foreign, true); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("foreign chat = %v", err)
	}
	bound := newFakeCoder(t)
	workspace := "ws"
	bound.chats["c"] = &coderapi.Chat{ID: "c", OwnerID: "u1", Status: coderapi.ChatStatusWaiting, WorkspaceID: &workspace,
		Labels: map[string]string{labelConversation: key, labelSession: "s1"}}
	if err := resume(bound, true); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("workspace-bound chat = %v", err)
	}
	if _, err := New(newFakeCoder(t), nil).Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "s1", ProviderConversationID: "not-a-key", WorkspacePath: t.TempDir(),
	}); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("malformed key = %v", err)
	}
}

func TestResumeReattachesToOwnChat(t *testing.T) {
	key := "8f9d2f8a-3a43-4a5c-9b0e-1f1f2a3b4c5d"
	f := newFakeCoder(t)
	f.chats["c"] = &coderapi.Chat{ID: "c", OwnerID: "u1", Status: coderapi.ChatStatusWaiting,
		Labels: map[string]string{labelConversation: key, labelSession: "s1"}}
	f.history = []coderapi.ChatMessage{{ID: 9, Role: coderapi.RoleAssistant}, {ID: 4, Role: coderapi.RoleUser}}
	conv, err := New(f, nil).Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "s1", ProviderConversationID: key, DataDir: t.TempDir(), WorkspacePath: t.TempDir(), HasPriorTurns: true,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()
	if conv.ProviderConversationID() != key {
		t.Fatalf("conversation id = %q", conv.ProviderConversationID())
	}
	sendTurn(t, conv, "continue")
	if creates, sent, _, _ := f.snapshot(); len(creates) != 0 || len(sent) != 1 {
		t.Fatalf("resume created %d chats and sent %d messages", len(creates), len(sent))
	}
}

func TestStartRejectsAutoModeAndUnknownModel(t *testing.T) {
	f := newFakeCoder(t)
	d := New(f, nil)
	if d.SupportsPermissionMode(ports.PermissionModeAuto) {
		t.Fatal("auto mode offered without a sandbox")
	}
	_, err := d.Start(context.Background(), ports.ChatStartConfig{SessionID: "s1", WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto})
	if !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("auto start = %v", err)
	}
	_, err = d.Start(context.Background(), ports.ChatStartConfig{SessionID: "s1", WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Model: "glm-5-flash"})
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("unknown model = %v", err)
	}
	_, err = d.Start(context.Background(), ports.ChatStartConfig{SessionID: "s1", WorkspacePath: t.TempDir(), DataDir: t.TempDir(), Model: "m1", Effort: "max"})
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("unsupported effort = %v", err)
	}
}

func TestStartReportsDeploymentWithoutChatsAPI(t *testing.T) {
	f := newFakeCoder(t)
	f.noChatsAPI = true
	_, err := New(f, nil).Start(context.Background(), ports.ChatStartConfig{SessionID: "s1", WorkspacePath: t.TempDir()})
	if !errors.Is(err, ports.ErrChatDriverIncompatible) {
		t.Fatalf("Start = %v, want ErrChatDriverIncompatible", err)
	}
}

func TestListModelsReturnsDeploymentCatalog(t *testing.T) {
	f := newFakeCoder(t)
	conv, _ := startConversation(t, f, ports.PermissionModeDefault)
	models, err := conv.(ports.ChatModelLister).ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "m1" || !models[0].Default || len(models[0].Efforts) != 2 {
		t.Fatalf("ListModels = %+v, %v", models, err)
	}
}
