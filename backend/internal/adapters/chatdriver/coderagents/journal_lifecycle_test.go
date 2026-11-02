package coderagents

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func pendingOutputConversation(t *testing.T) (*fakeCoder, *conversation) {
	t.Helper()
	f := newFakeCoder(t)
	conv, _ := startConversation(t, f, ports.PermissionModeBypassPermissions)
	sendTurn(t, conv, "work")
	c := conv.(*conversation)
	if err := c.journal.finish("pending-call", textOutcome(map[string]any{"content": "private file contents"}, "private file contents")); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.turn.started["pending-call"] = true
	c.mu.Unlock()
	return f, c
}

func waitForClosedConversation(t *testing.T, c *conversation) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.Events():
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("conversation did not finish shutting down")
		}
	}
}

func TestTerminateDropsUndeliveredOutputButKeepsReplayTombstone(t *testing.T) {
	_, c := pendingOutputConversation(t)
	if err := c.Terminate(); err != nil {
		t.Fatal(err)
	}
	waitForClosedConversation(t, c)
	record, known, err := c.journal.load("pending-call")
	if err != nil || !known || record.State != callRetired || record.Outcome != nil {
		t.Fatalf("terminated call = %+v, known=%v, err=%v; want only its execution tombstone", record, known, err)
	}
	outcome, ok := c.executeCall(&turnState{started: map[string]bool{}}, coderapi.ToolCall{ToolCallID: "pending-call", ToolName: toolWrite, Args: `{"path":"out.txt","content":"replayed"}`})
	if !ok || !outcome.IsError {
		t.Fatalf("retired call was eligible for execution: %+v, %v", outcome, ok)
	}
}

func TestTerminateRetiresAResultReplayedFromTheJournal(t *testing.T) {
	_, c := pendingOutputConversation(t)
	c.mu.Lock()
	target := c.turn
	delete(target.started, "pending-call")
	c.mu.Unlock()
	if outcome, ok := c.executeCall(target, coderapi.ToolCall{ToolCallID: "pending-call", ToolName: toolReadFile}); !ok || outcome.IsError {
		t.Fatalf("saved result replay failed: %+v, %v", outcome, ok)
	}
	if err := c.Terminate(); err != nil {
		t.Fatal(err)
	}
	waitForClosedConversation(t, c)
	record, known, err := c.journal.load("pending-call")
	if err != nil || !known || record.State != callRetired || record.Outcome != nil {
		t.Fatalf("replayed call retained its output after termination: %+v, %v, %v", record, known, err)
	}
}

func TestActiveDetachKeepsTheResultNeededByResume(t *testing.T) {
	_, c := pendingOutputConversation(t)
	_ = c.Close()
	waitForClosedConversation(t, c)
	record, known, err := c.journal.load("pending-call")
	if err != nil || !known || record.State != callDone || record.Outcome == nil {
		t.Fatalf("detached call lost its pending result: %+v, %v, %v", record, known, err)
	}
}

func TestNativeCompletionDiscardsUnneededToolOutput(t *testing.T) {
	f, c := pendingOutputConversation(t)
	f.push <- []coderapi.StreamEvent{status(coderapi.ChatStatusWaiting)}
	nextEvent(t, c.Events(), ports.ChatEventTurnCompleted)
	_ = c.Close()
	waitForClosedConversation(t, c)
	record, known, err := c.journal.load("pending-call")
	if err != nil || !known || record.State != callRetired || record.Outcome != nil {
		t.Fatalf("finished turn retained tool output: %+v, %v, %v", record, known, err)
	}
}

func TestIdleResumePrunesPreviouslyUndeliveredToolOutput(t *testing.T) {
	f, c := pendingOutputConversation(t)
	key, workspace, dataDir := c.cfg.key, c.cfg.workspace, c.cfg.dataDir
	_ = c.Close()
	waitForClosedConversation(t, c)
	f.mu.Lock()
	f.chats["chat-1"].Status = coderapi.ChatStatusWaiting
	f.history = []coderapi.ChatMessage{
		nativeText(1, coderapi.RoleUser, "work"),
		{ID: 2, Role: coderapi.RoleAssistant, Content: []coderapi.ChatMessagePart{{Type: coderapi.PartToolCall, ToolCallID: "pending-call", ToolName: toolReadFile}}},
	}
	f.mu.Unlock()
	resumed, _ := resumedHistory(t, f, key, workspace, dataDir)
	record, known, err := resumed.(*conversation).journal.load("pending-call")
	if err != nil || !known || record.State != callRetired || record.Outcome != nil {
		t.Fatalf("idle resume retained unneeded output: %+v, %v, %v", record, known, err)
	}
}
