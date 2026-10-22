package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/notificationoutbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestRunHookDoesNotPublishReviewerActivity(t *testing.T) {
	t.Setenv(worker.ReviewTerminalEnv, "1")

	err := runHook(
		context.Background(),
		nil,
		[]string{"codex", "user-prompt-submit"},
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// An agent that survived a worker restart carries its launch epoch in the
// environment; its hooks must stamp the current worker's recorded epoch.
func TestHookEpochPrefersTheCurrentWorkersRecordedEpoch(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("AO_CLOUD_WORKER_EPOCH", "4")
	if got := workerEpochFromEnvironment(); got != 4 {
		t.Fatalf("epoch = %d, want the launch epoch 4 with no recorded epoch", got)
	}
	if err := notificationoutbox.WriteWorkerEpoch(dataDir, 6); err != nil {
		t.Fatal(err)
	}
	if got := workerEpochFromEnvironment(); got != 6 {
		t.Fatalf("epoch = %d, want the current worker's 6", got)
	}
}

func TestNotificationFromActivityMapsNeedsInputWithStableIdentity(t *testing.T) {
	t.Parallel()
	activity := worker.ActivityEvent{
		Harness: "claude-code", Event: "permission-request", State: contract.ActivityBlocked,
		ToolName: "Bash", ToolUseID: "tool-7", AgentSessionID: "agent-1",
	}
	first, ok := notificationFromActivity("session-1", 4, activity)
	if !ok {
		t.Fatal("blocked activity did not produce a notification")
	}
	second, ok := notificationFromActivity("session-1", 4, activity)
	if !ok || second.EventID != first.EventID {
		t.Fatalf("event identity is not stable: %q and %q", first.EventID, second.EventID)
	}
	if first.EventType != "needs_input" || first.WorkerEpoch != 4 {
		t.Fatalf("event = %+v", first)
	}
	var payload struct{ ActivityID, Message string }
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ActivityID != "tool-7" || payload.Message == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestNotificationFromActivityOnlyReportsTerminalFailure(t *testing.T) {
	t.Parallel()
	failed, ok := notificationFromActivity("session-1", 4, worker.ActivityEvent{
		Harness: "claude-code", Event: "session-end", State: contract.ActivityExited, AgentSessionID: "agent-1",
	})
	if !ok || failed.EventType != "agent_failed" {
		t.Fatalf("session end = %+v, %v", failed, ok)
	}
	for _, activity := range []worker.ActivityEvent{
		{Harness: "claude-code", Event: "stop", State: contract.ActivityIdle},
		{Harness: "codex", Event: "user-prompt-submit", State: contract.ActivityActive},
	} {
		if event, ok := notificationFromActivity("session-1", 4, activity); ok {
			t.Fatalf("ordinary activity produced notification %+v", event)
		}
	}
}

// Every terminal Claude hook records the conversation its process is in, so
// the worker follows the transcript /clear or /resume moves it to.
func TestRunHookRecordsTheTerminalClaudeConversation(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("AO_CLOUD_SOURCE_INTERFACE", "tui")
	t.Setenv("CLAUDE_PID", "4242")
	c := &client{tokenFile: dataDir + "/missing-token"}
	hook := func(event, sessionID string) {
		t.Helper()
		if err := runHook(context.Background(), c, []string{"claude-code", event},
			strings.NewReader(`{"session_id":"`+sessionID+`","prompt":"go"}`)); err != nil {
			t.Fatal(err)
		}
	}
	hook("user-prompt-submit", "conv-a")
	hook("user-prompt-submit", "conv-b")
	record, ok := worker.ReadClaudeConversations(dataDir)["4242"]
	if !ok || record.First != "conv-a" || record.Current != "conv-b" {
		t.Fatalf("records = %+v, want process 4242 moved from conv-a to conv-b", worker.ReadClaudeConversations(dataDir))
	}

	// Chat's hooks are not the terminal's.
	t.Setenv("AO_CLOUD_SOURCE_INTERFACE", "chat")
	t.Setenv("CLAUDE_PID", "5151")
	hook("user-prompt-submit", "conv-c")
	if _, ok := worker.ReadClaudeConversations(dataDir)["5151"]; ok {
		t.Fatal("a Chat hook recorded a terminal conversation")
	}
}
