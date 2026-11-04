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
	writeFile(t, filepath.Join(dir, "events", "event-00005-new.json"), `{"id":"new","parent_id":"reply","source":"user","timestamp":"2026-11-02T10:01:00","llm_message":{"role":"user","content":[{"type":"text","text":"New after scan"}]}}`)
	writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"`+id+`","workspace":{"working_dir":"/work/source"},"leaf_event_id":"new"}`)
	fresh, err := readOpenHandsTranscript(context.Background(), root, got.path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, fresh.messages, "user: Synthetic prompt", "assistant: Synthetic answer", "user: New after scan")
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

func TestOpenHandsImportRecoversMissingEvents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		leaf     string
		parent   string
		artifact bool
	}{
		{name: "missing parent", leaf: "reply", parent: "lost-tool"},
		{name: "missing leaf", leaf: "lost-leaf", parent: "user"},
		{name: "missing leaf with trailing artifact", leaf: "lost-leaf", parent: "lost-tool", artifact: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "12345678123412341234123456789012")
			writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"12345678-1234-1234-1234-123456789012","workspace":{"working_dir":"/work/source"},"leaf_event_id":"`+tc.leaf+`"}`)
			writeFile(t, filepath.Join(dir, "events", "event-00000-old.json"), `{"id":"old","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"Earlier branch"}]}}`)
			writeFile(t, filepath.Join(dir, "events", "event-00001-user.json"), `{"id":"user","parent_id":"__root__","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"Surviving prompt"}]}}`)
			// event-00002 was lost; the native SDK bridges the actual file gap.
			writeFile(t, filepath.Join(dir, "events", "event-00003-reply.json"), `{"id":"reply","parent_id":"`+tc.parent+`","source":"agent","llm_message":{"role":"assistant","content":[{"type":"text","text":"Surviving reply"}]}}`)
			if tc.artifact {
				writeFile(t, filepath.Join(dir, "events", "event-00004-state.json"), `{"id":"state","kind":"ConversationStateUpdateEvent","parent_id":"__root__"}`)
			}
			files, err := discoverOpenHands(context.Background(), root)
			if err != nil || len(files) != 1 {
				t.Fatalf("discover = %v, %v", files, err)
			}
			got, err := readOpenHandsTranscript(context.Background(), root, files[0].paths...)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: Surviving prompt", "assistant: Surviving reply")
		})
	}
}

func TestOpenHandsImportOrdersWideEventIndices(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "12345678123412341234123456789012")
	writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"12345678-1234-1234-1234-123456789012","workspace":{"working_dir":"/work/source"}}`)
	writeFile(t, filepath.Join(dir, "events", "event-99999-user.json"), `{"id":"user","source":"user","timestamp":"2026-11-02T10:00:00Z","llm_message":{"role":"user","content":[{"type":"text","text":"Synthetic prompt"}]}}`)
	writeFile(t, filepath.Join(dir, "events", "event-100000-reply.json"), `{"id":"reply","source":"agent","timestamp":"2026-11-02T10:01:00Z","llm_message":{"role":"assistant","content":[{"type":"text","text":"Synthetic answer"}]}}`)
	files, err := discoverOpenHands(context.Background(), root)
	if err != nil || len(files) != 1 {
		t.Fatalf("discover = %v, %v", files, err)
	}
	got, err := readOpenHandsTranscript(context.Background(), root, files[0].paths...)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Synthetic prompt", "assistant: Synthetic answer")
}
