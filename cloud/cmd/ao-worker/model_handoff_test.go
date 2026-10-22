package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestInteractiveLaunchSettingsRestoresLatestChatSelection(t *testing.T) {
	dataDir := t.TempDir()
	id := "550e8400-e29b-41d4-a716-446655440000"
	path := filepath.Join(dataDir, "codex", "sessions", "rollout-2026-10-02T01-00-00-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"turn_context","payload":{"model":"chat-selected","effort":"xhigh"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launch, err := interactiveLaunchSettings(worker.LaunchContext{Harness: "codex", Model: "session-original"}, dataDir, id, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if launch.Model != "chat-selected" || launch.ReasoningEffort != "xhigh" || launch.AgentSessionID != id {
		t.Fatalf("handoff launch = %+v", launch)
	}
	launch, err = interactiveLaunchSettings(worker.LaunchContext{Harness: "codex", Model: "session-original"}, dataDir, id, "pending-chat-selection", "high")
	if err != nil {
		t.Fatal(err)
	}
	if launch.Model != "pending-chat-selection" || launch.ReasoningEffort != "high" {
		t.Fatalf("pending selection lost to older rollout: %+v", launch)
	}
}

func TestInteractiveLaunchSettingsKeepsPendingSelectionAcrossWorkerRestart(t *testing.T) {
	dataDir := t.TempDir()
	id := "550e8400-e29b-41d4-a716-446655440000"
	path := filepath.Join(dataDir, "codex", "sessions", "rollout-2026-10-02T01-00-00-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := `{"timestamp":"2026-10-02T01:00:00Z","type":"turn_context","payload":{"model":"old-native","effort":"low"}}` + "\n"
	if err := os.WriteFile(path, []byte(rollout), 0o600); err != nil {
		t.Fatal(err)
	}
	launch := worker.LaunchContext{Harness: "codex", Model: "pending-chat-selection", ReasoningEffort: "xhigh", SelectionAt: time.Date(2026, 10, 2, 1, 1, 0, 0, time.UTC)}
	result, err := interactiveLaunchSettings(launch, dataDir, id, "", "")
	if err != nil || result.Model != launch.Model || result.ReasoningEffort != launch.ReasoningEffort {
		t.Fatalf("restarted launch = %+v, %v", result, err)
	}
}

func TestInteractiveLaunchSettingsAppliesClaudeChatSelection(t *testing.T) {
	launch, err := interactiveLaunchSettings(worker.LaunchContext{
		Harness: "claude-code", Model: "original-model", ReasoningEffort: "medium",
	}, t.TempDir(), "native-claude", "chat-model", "high")
	if err != nil || launch.AgentSessionID != "native-claude" || launch.Model != "chat-model" || launch.ReasoningEffort != "high" {
		t.Fatalf("Claude handoff launch = %+v, %v", launch, err)
	}
}

func TestInteractiveLaunchSettingsRestoresClaudeNativeSelection(t *testing.T) {
	dataDir := t.TempDir()
	project := filepath.Join(dataDir, "claude", "projects", "test-project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "native-claude.jsonl"), []byte(`{"type":"assistant","timestamp":"2026-10-04T01:00:00Z","message":{"model":"claude-native","effort":"high"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launch, err := interactiveLaunchSettings(worker.LaunchContext{Harness: "claude-code", Model: "original-model"}, dataDir, "native-claude", "", "")
	if err != nil || launch.Model != "claude-native" || launch.ReasoningEffort != "high" {
		t.Fatalf("launch = %+v, %v", launch, err)
	}
}
