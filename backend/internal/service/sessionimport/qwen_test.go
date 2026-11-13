package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	files, err := discoverFor(context.Background(), domain.HarnessQwen, canonical(root), nil)
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
	ctx := context.Background()
	root := t.TempDir()
	project := t.TempDir()
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
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
	if rec.Metadata.ImportSource == nil || rec.Metadata.ImportSource.NativeID != qwenID {
		t.Fatalf("import source must retain the native id: %#v", rec.Metadata.ImportSource)
	}
	if !rec.NeedsImportResume() || rec.Mode != domain.SessionModeChat {
		t.Fatalf("unexpected record: %#v", rec)
	}
}

func TestQwenOversizeAndTornRecords(t *testing.T) {
	oldRecord, oldString := maxRecordBytes, maxSkimString
	maxRecordBytes, maxSkimString = 1024, 512
	t.Cleanup(func() { maxRecordBytes, maxSkimString = oldRecord, oldString })
	root := t.TempDir()
	records := qwenConversation("/work/repo")
	records = append(records, map[string]any{"type": "assistant", "provenance": "assistant_output",
		"message": map[string]any{"parts": []map[string]any{{"text": strings.Repeat("x", 4096)}, {"text": "Readable tail"}}}})
	path := writeQwenChat(t, root, "/work/repo", qwenID, records)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","message":`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.shortened || len(got.messages) != 3 || got.messages[2].Text != "Readable tail" {
		t.Fatalf("oversize/torn history: shortened=%v messages=%#v", got.shortened, got.messages)
	}
}

func TestQwenScanGroupsNativeIDAcrossProjects(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	source := writeQwenChat(t, root, project, qwenID, qwenConversation(project))
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(root, "projects", "-another-project", "chats", qwenID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(copyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(&importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessQwen: root}
	preview, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 1 || preview.Candidates[0].ID != candidateID(domain.HarnessQwen, canonical(root), qwenID) {
		t.Fatalf("copies must share one native candidate: %#v", preview.Candidates)
	}
}

func qwenLinkedRecord(id, parent, kind, text string) map[string]any {
	provenance := kind
	role := kind
	switch kind {
	case "user":
		provenance = "real_user"
	case "assistant":
		provenance, role = "assistant_output", "model"
	}
	r := map[string]any{"uuid": id, "parentUuid": parent, "type": kind, "provenance": provenance, "timestamp": time.Now()}
	if parent == "" {
		r["parentUuid"] = nil
	}
	if kind == "user" || kind == "assistant" {
		r["message"] = map[string]any{"role": role, "parts": []map[string]any{{"text": text}}}
	}
	return r
}

func TestQwenTranscriptFollowsActiveBranchAfterRewind(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "rewind only", true: "replacement turn"}[replacement], func(t *testing.T) {
			root := t.TempDir()
			records := []map[string]any{
				qwenLinkedRecord("u1", "", "user", "opening question"),
				qwenLinkedRecord("a1", "u1", "assistant", "opening answer"),
				qwenLinkedRecord("u2", "a1", "user", "abandoned question"),
				qwenLinkedRecord("a2", "u2", "assistant", "abandoned answer"),
				qwenLinkedRecord("rewind", "a1", "system", ""),
			}
			records[4]["subtype"] = "rewind"
			if replacement {
				records = append(records,
					qwenLinkedRecord("u3", "rewind", "user", "replacement question"),
					qwenLinkedRecord("tool", "u3", "tool_result", ""),
					qwenLinkedRecord("a3", "tool", "assistant", "replacement answer"),
					qwenLinkedRecord("metadata", "a3", "system", ""))
			}
			path := writeQwenChat(t, root, "/work/repo", qwenID, records)
			got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"user: opening question", "assistant: opening answer"}
			if replacement {
				want = append(want, "user: replacement question", "assistant: replacement answer")
			}
			wantTexts(t, got.messages, want...)
			if got.title != "opening question" || got.shortened {
				t.Fatalf("active title/shortened = %q/%v", got.title, got.shortened)
			}
		})
	}
}

func TestQwenRewindToStartRefusesDiscardedConversation(t *testing.T) {
	root := t.TempDir()
	path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{
		qwenLinkedRecord("u1", "", "user", "discarded prompt"),
		qwenLinkedRecord("a1", "u1", "assistant", "discarded answer"),
		qwenLinkedRecord("rewind", "", "system", ""),
	})
	if _, err := readTranscript(context.Background(), domain.HarnessQwen, root, path); err == nil {
		t.Fatal("a discarded prompt must not make the empty active branch importable")
	}
}

func TestQwenParentTraversalIsBounded(t *testing.T) {
	for _, parent := range []string{"missing", "a1"} {
		t.Run(parent, func(t *testing.T) {
			root := t.TempDir()
			path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{
				qwenLinkedRecord("old", "", "user", "unreachable prompt"),
				qwenLinkedRecord("u1", parent, "user", "reachable prompt"),
				qwenLinkedRecord("a1", "u1", "assistant", "reachable answer"),
			})
			got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: reachable prompt", "assistant: reachable answer")
		})
	}
}

func TestQwenUserDisplayProjection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		payload   map[string]any
		modelText string
		want      string
	}{
		{"display prompt", map[string]any{"displayText": "inspect @note.txt", "hookContext": "PRIVATE HOOK CONTEXT"}, "expanded resource contents\n<user_prompt_submit_context>PRIVATE HOOK CONTEXT</user_prompt_submit_context>", "inspect @note.txt"},
		{"legacy fallback", nil, "older plain prompt", "older plain prompt"},
		{"empty display refuses model context", map[string]any{"displayText": "", "hookContext": "PRIVATE HOOK CONTEXT"}, "PRIVATE MODEL INPUT", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			user := qwenLinkedRecord("u1", "", "user", tc.modelText)
			if tc.payload != nil {
				user["systemPayload"] = tc.payload
			}
			path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{user, qwenLinkedRecord("a1", "u1", "assistant", "visible reply")})
			got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
			if tc.want == "" {
				if err == nil {
					t.Fatal("an empty display projection must not expose model-only context")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: "+tc.want, "assistant: visible reply")
			if got.title != tc.want {
				t.Fatalf("title = %q, want display text %q", got.title, tc.want)
			}
		})
	}
}

func TestQwenAbandonedTextDoesNotShortenActiveBranch(t *testing.T) {
	oldLimit := maxHeldText
	maxHeldText = 64
	t.Cleanup(func() { maxHeldText = oldLimit })
	root := t.TempDir()
	path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{
		qwenLinkedRecord("u1", "", "user", "opening"),
		qwenLinkedRecord("abandoned", "u1", "user", strings.Repeat("x", 256)),
		qwenLinkedRecord("rewind", "u1", "system", ""),
		qwenLinkedRecord("u2", "rewind", "user", "replacement"),
		qwenLinkedRecord("a2", "u2", "assistant", "reply"),
	})
	got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: opening", "user: replacement", "assistant: reply")
	if got.shortened {
		t.Fatal("discarded text outside the active branch must not mark its history shortened")
	}
}

func TestQwenAggregatesSameUUIDFragments(t *testing.T) {
	root := t.TempDir()
	latest := time.Now().UTC().Add(-time.Second)
	first := qwenLinkedRecord("reply", "prompt", "assistant", "first ")
	first["timestamp"] = latest.Add(-time.Second)
	second := qwenLinkedRecord("reply", "prompt", "assistant", "second")
	second["timestamp"] = latest
	tool := qwenLinkedRecord("reply", "prompt", "assistant", "")
	tool["message"] = map[string]any{"role": "model", "parts": []map[string]any{{"functionCall": map[string]any{"name": "read", "args": map[string]any{}}}}}
	tool["timestamp"] = latest
	usage := qwenLinkedRecord("reply", "abandoned", "assistant", "")
	delete(usage, "message")
	usage["usageMetadata"] = map[string]any{"totalTokenCount": 1}
	usage["timestamp"] = latest
	path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{
		qwenLinkedRecord("abandoned", "", "user", "discarded question"),
		qwenLinkedRecord("prompt", "", "user", "question"), first, second, tool, usage,
	})
	got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: question", "assistant: first second")
	if got.title != "question" || !got.messages[1].At.Equal(latest) || got.shortened {
		t.Fatalf("fragment metadata = title %q, timestamp %v, shortened %v", got.title, got.messages[1].At, got.shortened)
	}
}

func TestQwenUserFragmentsKeepFirstDisplayProjection(t *testing.T) {
	for _, display := range []string{"typed prompt", ""} {
		name := display
		if name == "" {
			name = "empty display"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			first := qwenLinkedRecord("prompt", "", "user", "PRIVATE FIRST MODEL INPUT")
			first["systemPayload"] = map[string]any{"displayText": display}
			second := qwenLinkedRecord("prompt", "", "user", "PRIVATE SECOND MODEL INPUT")
			second["systemPayload"] = map[string]any{"displayText": "later fragment projection"}
			path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{first, second, qwenLinkedRecord("reply", "prompt", "assistant", "answer")})
			got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
			if display == "" {
				if err == nil {
					t.Fatal("a later fragment must not replace an empty display projection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: typed prompt", "assistant: answer")
			if got.title != "typed prompt" {
				t.Fatalf("first display projection was replaced: %q", got.title)
			}
		})
	}
}

func TestQwenFragmentTextBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int
		fragment string
		want     string
	}{
		{"ASCII", 16, "abcdefghij", "1234567890abcdef"},
		{"UTF8", 15, "ééé", "1234567890éé"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldLimit := maxHeldText
			maxHeldText = tc.limit
			t.Cleanup(func() { maxHeldText = oldLimit })
			root := t.TempDir()
			path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{
				qwenLinkedRecord("prompt", "", "user", "1234567890"),
				qwenLinkedRecord("prompt", "", "user", tc.fragment),
			})
			got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: "+tc.want)
			if !got.shortened || len(got.messages[0].Text) > tc.limit {
				t.Fatal("combined fragment text must be bounded and marked shortened")
			}
		})
	}
}

func TestQwenNonConversationRecordsDoNotChangeLeaf(t *testing.T) {
	for _, subtype := range []string{"session_artifact_event", "session_artifact_snapshot", "session_sources_snapshot", "managed_session_header_v1", "managed_session_event_v1", "managed_session_commit_v1", "managed_session_snapshot_base_v1", "foreign"} {
		t.Run(subtype, func(t *testing.T) {
			root := t.TempDir()
			extra := qwenLinkedRecord("extra", "", "system", "")
			extra["subtype"] = subtype
			if subtype == "foreign" {
				extra["type"] = "foreign"
			}
			path := writeQwenChat(t, root, "/work/repo", qwenID, []map[string]any{qwenLinkedRecord("prompt", "", "user", "question"), qwenLinkedRecord("reply", "prompt", "assistant", "answer"), extra})
			got, err := readTranscript(context.Background(), domain.HarnessQwen, root, path)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, "user: question", "assistant: answer")
		})
	}
}
