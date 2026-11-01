package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const qwenID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

// writeQwenChat writes synthetic JSONL records (no real user content) to the
// Qwen chats layout: <root>/projects/<sanitized-cwd>/chats/<id>.jsonl.
func writeQwenChat(t *testing.T, root, project, id string, records []map[string]any) string {
	t.Helper()
	dir := filepath.Join(root, "projects", "-synthetic-"+id, "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, r := range records {
		if _, ok := r["sessionId"]; !ok {
			r["sessionId"] = id
		}
		if _, ok := r["cwd"]; !ok {
			r["cwd"] = project
		}
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func qwenConversation(project string) []map[string]any {
	at := time.Now()
	return []map[string]any{
		// Injected context and environment are system-provenance, never prompts.
		{"type": "system", "provenance": "system", "timestamp": at, "systemPayload": map[string]any{"subtype": "session_approval_mode"}},
		{"type": "user", "provenance": "real_user", "timestamp": at.Add(time.Second),
			"message": map[string]any{"role": "user", "parts": []map[string]any{{"text": "fix the parser"}}}},
		// The model reply carries a thinking part and a tool call beside its text; only the text shows.
		{"type": "assistant", "provenance": "assistant_output", "timestamp": at.Add(2 * time.Second),
			"message": map[string]any{"role": "model", "parts": []map[string]any{
				{"text": "considering options", "thought": true},
				{"text": "Here is the fix."},
				{"functionCall": map[string]any{"name": "edit", "args": map[string]any{}}},
			}}},
		// Tool output and background notifications are provider traffic, not messages.
		{"type": "tool_result", "provenance": "tool_result", "timestamp": at.Add(3 * time.Second),
			"message": map[string]any{"role": "user", "parts": []map[string]any{{"functionResponse": map[string]any{"name": "edit"}}}}},
		{"type": "user", "provenance": "system", "timestamp": at.Add(4 * time.Second),
			"message": map[string]any{"role": "user", "parts": []map[string]any{{"text": "injected monitor notification"}}}},
	}
}

func TestQwenTranscriptParsing(t *testing.T) {
	root := t.TempDir()
	project := "/work/repo"
	path := writeQwenChat(t, root, project, qwenID, qwenConversation(project))

	got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
	if err != nil {
		t.Fatalf("readTranscript: %v", err)
	}
	if got.nativeID != qwenID {
		t.Fatalf("nativeID = %q", got.nativeID)
	}
	if got.cwd != project {
		t.Fatalf("cwd = %q", got.cwd)
	}
	if got.title != "fix the parser" {
		t.Fatalf("title = %q", got.title)
	}
	if got.shortened {
		t.Fatal("unexpected shortened")
	}
	if len(got.messages) != 2 {
		t.Fatalf("messages = %#v", got.messages)
	}
	if got.messages[0].Role != domain.MessageRoleUser || got.messages[0].Text != "fix the parser" {
		t.Fatalf("user message = %#v", got.messages[0])
	}
	if got.messages[1].Role != domain.MessageRoleAssistant || got.messages[1].Text != "Here is the fix." {
		t.Fatalf("assistant message = %#v (thinking/tool parts must be dropped)", got.messages[1])
	}
}

func TestQwenDiscoverSkipsArchiveAndNonTranscripts(t *testing.T) {
	root := t.TempDir()
	project := "/work/repo"
	writeQwenChat(t, root, project, qwenID, qwenConversation(project))
	// Archived chats and non-transcript files in the project dir must not be offered.
	projectDir := filepath.Join(root, "projects", "-synthetic-"+qwenID)
	archive := filepath.Join(projectDir, "chats", "archive")
	if err := os.MkdirAll(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "memory", "MEMORY.md"), []byte("notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "meta.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := New(&importStore{}, "")
	files, err := svc.discover(context.Background(), domain.HarnessQwen, canonical(root))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("discovered = %#v", files)
	}
	if files[0].nativeID() != qwenID {
		t.Fatalf("nativeID = %q", files[0].nativeID())
	}
}

func TestQwenNativeIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{
		{"type": "user", "provenance": "real_user", "sessionId": "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "timestamp": time.Now(),
			"message": map[string]any{"role": "user", "parts": []map[string]any{{"text": "hi"}}}},
	})
	if _, err := readTranscript(context.Background(), domain.HarnessQwen, root, path); err == nil {
		t.Fatal("expected native identity mismatch error")
	}
}

func TestQwenPromptlessIsNotAConversation(t *testing.T) {
	root := t.TempDir()
	// Only assistant output and injected context, no real user prompt.
	path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{
		{"type": "user", "provenance": "system", "timestamp": time.Now(),
			"message": map[string]any{"role": "user", "parts": []map[string]any{{"text": "injected"}}}},
		{"type": "assistant", "provenance": "assistant_output", "timestamp": time.Now(),
			"message": map[string]any{"role": "model", "parts": []map[string]any{{"text": "hello"}}}},
	})
	if _, err := readTranscript(context.Background(), domain.HarnessQwen, root, path); err == nil {
		t.Fatal("a session with no real prompt is not importable")
	}
}

func TestQwenImportIsFullResume(t *testing.T) {
	// Qwen now does a full copy-and-resume import, so it must NOT be flagged
	// history-only (that would lock the composer and block resume).
	if historyOnlyImport(domain.HarnessQwen) {
		t.Fatal("Qwen imports must not be history-only; they resume via transcript relocation")
	}
	ctx := context.Background()
	root := t.TempDir()
	project := t.TempDir()
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "")
	svc.roots = map[domain.AgentHarness]string{domain.HarnessQwen: root}
	writeQwenChat(t, root, project, qwenID, qwenConversation(project))

	preview, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(preview.Candidates) != 1 {
		t.Fatalf("candidates = %#v", preview.Candidates)
	}
	results, err := svc.Import(ctx, []string{preview.Candidates[0].ID})
	if err != nil || results[0].Status != "created" {
		t.Fatalf("import: %v %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Metadata.ImportSource == nil || rec.Metadata.ImportSource.HistoryOnly {
		t.Fatalf("import source must be resumable, not history-only: %#v", rec.Metadata.ImportSource)
	}
	if !rec.NeedsImportResume() || rec.Mode != domain.SessionModeChat {
		t.Fatalf("unexpected record: %#v", rec)
	}
}
