package sessionimport

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	for _, retained := range [][]string{{got.path}, files[0].paths} {
		fresh, err := readOpenHandsTranscript(context.Background(), root, retained...)
		if err != nil {
			t.Fatal(err)
		}
		wantTexts(t, fresh.messages, "user: Synthetic prompt", "assistant: Synthetic answer", "user: New after scan")
	}
	writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"87654321-1234-1234-1234-123456789012","workspace":{"working_dir":"/work/source"}}`)
	if _, err := readOpenHandsTranscript(context.Background(), root, files[0].paths...); err == nil {
		t.Fatal("accepted mismatched native id")
	}
}

func TestOpenHandsRequiresNativeUUIDHexDirectory(t *testing.T) {
	id := "a2345678-abcd-1234-abcd-123456789abc"
	hex := strings.ReplaceAll(id, "-", "")
	for _, name := range []string{hex, id, strings.ToUpper(hex), "{" + id + "}"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"`+id+`","workspace":{"working_dir":"/work/source"},"leaf_event_id":"user"}`)
			writeFile(t, filepath.Join(dir, "events", "event-00001-user.json"), `{"id":"user","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"Native prompt"}]}}`)
			files, err := discoverOpenHands(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if (len(files) == 1) != (name == hex) {
				t.Fatalf("directory %q admitted %d conversations", name, len(files))
			}
			_, err = readOpenHandsTranscript(context.Background(), root, filepath.Join(dir, "base_state.json"))
			if (err == nil) != (name == hex) {
				t.Fatalf("directory %q read error = %v", name, err)
			}
		})
	}
}

func TestOpenHandsNaiveTimestampUsesLocalZone(t *testing.T) {
	zone := time.FixedZone("SDK-local", 9*60*60)
	want := time.Date(2026, 11, 18, 3, 0, 0, 123456000, time.UTC)
	for _, value := range []string{"2026-11-18T12:00:00.123456", "2026-11-18T12:00:00.123456+09:00", "2026-11-18T03:00:00.123456Z"} {
		if got := openHandsEventTime(value, zone); !got.Equal(want) {
			t.Fatalf("timestamp %q = %s, want %s", value, got, want)
		}
	}
}

func TestOpenHandsAbandonedTextCannotEvictActiveBranch(t *testing.T) {
	oldHeld, oldVisible := maxHeldText, maxVisibleBytes
	maxHeldText, maxVisibleBytes = 64, 128
	t.Cleanup(func() { maxHeldText, maxVisibleBytes = oldHeld, oldVisible })
	root := t.TempDir()
	dir := filepath.Join(root, "12345678123412341234123456789012")
	writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"12345678-1234-1234-1234-123456789012","workspace":{"working_dir":"/work/source"},"leaf_event_id":"C"}`)
	for i, event := range []struct{ id, parent, role, text string }{
		{"user", "__root__", "user", "Opening prompt"},
		{"A", "user", "assistant", "Active answer A"},
		{"B", "A", "assistant", strings.Repeat("abandoned", 64)},
		{"C", "A", "assistant", "Active answer C"},
	} {
		source := "agent"
		if event.role == "user" {
			source = "user"
		}
		raw, err := json.Marshal(map[string]any{"id": event.id, "parent_id": event.parent, "source": source, "llm_message": map[string]any{"role": event.role, "content": []map[string]string{{"type": "text", "text": event.text}}}})
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "events", "event-0000"+string(rune('0'+i))+"-"+event.id+".json"), string(raw))
	}
	got, err := readOpenHandsTranscript(context.Background(), root, filepath.Join(dir, "base_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Opening prompt", "assistant: Active answer A", "assistant: Active answer C")
	if got.shortened {
		t.Fatal("abandoned branch text marked a complete active branch shortened")
	}
}

func TestOpenHandsRejectsStoredCredentialsBeforeAdmission(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "12345678123412341234123456789012")
	writeFile(t, filepath.Join(dir, "base_state.json"), `{"id":"12345678-1234-1234-1234-123456789012","workspace":{"working_dir":"/work/source"},"agent":{"llm":{"api_key":"synthetic-secret"}},"leaf_event_id":"user"}`)
	writeFile(t, filepath.Join(dir, "events", "event-00001-user.json"), `{"id":"user","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"Prompt"}]}}`)
	if _, err := readOpenHandsTranscript(context.Background(), root, filepath.Join(dir, "base_state.json")); err == nil {
		t.Fatal("admitted a conversation whose native configuration contains credentials")
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
