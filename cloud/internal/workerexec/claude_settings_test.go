package workerexec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeConversationSettingsAfterHonorsHandoffFence(t *testing.T) {
	dataDir := t.TempDir()
	project := filepath.Join(dataDir, "claude", "projects", "test-project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"assistant","timestamp":"2026-10-02T01:00:00Z","message":{"model":"old-model"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-02T01:02:00Z","isSidechain":true,"message":{"model":"sidechain-model"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-02T01:03:00Z","message":{"model":"new-tui-model","effort":"high"}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, "native-1.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	model, effort, err := ClaudeConversationSettingsAfter(dataDir, "native-1", time.Date(2026, 10, 2, 1, 1, 0, 0, time.UTC))
	if err != nil || model != "new-tui-model" || effort != "high" {
		t.Fatalf("settings = %q/%q, %v", model, effort, err)
	}
	model, effort, err = ClaudeConversationSettingsAfter(dataDir, "native-1", time.Date(2026, 10, 2, 1, 4, 0, 0, time.UTC))
	if err != nil || model != "" || effort != "" {
		t.Fatalf("stale settings = %q/%q, %v", model, effort, err)
	}
	model, effort, err = ClaudeConversationSettingsAfter(dataDir, "../native-1", time.Time{})
	if err != nil || model != "" || effort != "" {
		t.Fatalf("unsafe identity = %q/%q, %v", model, effort, err)
	}
}
