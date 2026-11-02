package sessionimport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestOpenHandsImportActiveBranch(t *testing.T) {
	root := t.TempDir()
	id := "12345678-1234-1234-1234-123456789012"
	dir := filepath.Join(root, "12345678123412341234123456789012")
	writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"`+id+`","workspace":{"working_dir":"/work/source"},"leaf_event_id":"reply"}`)
	for _, event := range []struct {
		name string
		body string
	}{
		{"event-00000-system.json", `{"id":"system","source":"agent","kind":"SystemPromptEvent","system_prompt":{"text":"hidden"}}`},
		{"event-00001-user.json", `{"id":"user","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"Synthetic prompt"}]},"extended_content":[{"type":"text","text":"injected context"}]}`},
		{"event-00002-abandoned.json", `{"id":"abandoned","parent_id":"user","source":"agent","llm_message":{"role":"assistant","content":[{"type":"text","text":"abandoned"}]}}`},
		{"event-00003-reply.json", `{"id":"reply","parent_id":"user","source":"agent","timestamp":"2026-11-02T10:00:00Z","llm_message":{"role":"assistant","content":[{"type":"text","text":"Synthetic answer"},{"type":"tool_call","text":"hidden"}]}}`},
		{"event-00004-torn.json", `{"id":`},
	} {
		writeFile(t, filepath.Join(dir, "events", event.name), event.body)
	}
	files, err := discoverOpenHands(context.Background(), root)
	if err != nil || len(files) != 1 {
		t.Fatalf("discover = %v, %v", files, err)
	}
	got, err := readTranscript(context.Background(), domain.HarnessOpenHands, root, files[0].paths...)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Synthetic prompt", "assistant: Synthetic answer")
	if got.nativeID != id || got.cwd != "/work/source" || got.title != "Synthetic prompt" || got.last.IsZero() {
		t.Fatalf("metadata = %+v", got)
	}
	writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"87654321-1234-1234-1234-123456789012","workspace":{"working_dir":"/work/source"}}`)
	if _, err := readOpenHandsTranscript(context.Background(), root, files[0].paths...); err == nil {
		t.Fatal("accepted mismatched native id")
	}
}

func TestOpenHandsRoot(t *testing.T) {
	t.Setenv("OPENHANDS_CONVERSATIONS_DIR", "")
	t.Setenv("OPENHANDS_PERSISTENCE_DIR", "")
	if got := openHandsRoot("/home/example"); got != filepath.Join("/home/example", ".openhands", "conversations") {
		t.Fatal(got)
	}
	t.Setenv("OPENHANDS_PERSISTENCE_DIR", "/custom/settings")
	if got := openHandsRoot("/home/example"); got != filepath.Join("/custom/settings", "conversations") {
		t.Fatal(got)
	}
	t.Setenv("OPENHANDS_CONVERSATIONS_DIR", "/custom/chats")
	if got := openHandsRoot("/home/example"); got != "/custom/chats" {
		t.Fatal(got)
	}
}
