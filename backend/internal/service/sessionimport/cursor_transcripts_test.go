package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// cursorLines are records as Cursor 2026.11 writes them: a prompt wrapped in
// <user_query> after a <timestamp>, each reply step as its own record with the
// model's hidden thinking appended as "[REDACTED]", tool calls without
// results, a notice Cursor sent itself and the turn_ended marker.
const cursorLines = `{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Saturday, Jun 13, 2026, 9:05 AM (UTC+5:30)</timestamp>\n<user_query>\nanalyze all the real estate project options i have\n</user_query>"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"I'll read your notes first.\n\n[REDACTED]"},{"type":"tool_use","name":"Read","input":{"path":"notes.md"}}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell","input":{"command":"ls"}}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"You have three options: A, B and C."}]}}
{"type":"turn_ended","status":"success"}
{"role":"user","message":{"content":[{"type":"text","text":"The subagent finished; summarize its result."}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"[REDACTED]"}]}}
{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Tuesday, Nov 17, 2026, 6:40 PM (UTC+5:30)</timestamp>\n<user_query>\nwhich one is cheapest?\n</user_query>"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"Option B."}]}}
not json
`

func writeCursorEditorChat(t *testing.T, cursor, folder, id, body string, nested bool, at time.Time) string {
	t.Helper()
	path := filepath.Join(cursor, "projects", cursorFolderName(folder), "agent-transcripts", id+".jsonl")
	if nested {
		path = filepath.Join(cursor, "projects", cursorFolderName(folder), "agent-transcripts", id, id+".jsonl")
	}
	writeFile(t, path, body)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

// withoutCursorEditorState keeps a test to the transcripts, whatever the machine running it holds.
func withoutCursorEditorState(t *testing.T) {
	t.Helper()
	saved := cursorEditorState
	cursorEditorState = func() string { return "" }
	t.Cleanup(func() { cursorEditorState = saved })
}

func TestCursorEditorChatImportsAndContinuesInClaudeCode(t *testing.T) {
	ctx := context.Background()
	withoutCursorEditorState(t)
	cursor, claude, project := t.TempDir(), t.TempDir(), canonical(t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	const (
		main  = "0e9e5078-f991-4c92-b998-5eca09b5f7c3"
		older = "ec206e49-7fa8-4fec-876b-6de840ec2469"
		cli   = "34fb2adb-9cc1-4437-b94a-de92882f059b"
	)
	writeCursorEditorChat(t, cursor, project, main, cursorLines, true, time.Now())
	// A subagent's transcript belongs to its parent chat.
	writeFile(t, filepath.Join(cursor, "projects", cursorFolderName(project), "agent-transcripts", main, "subagents", "11111111-1111-4111-8111-111111111111.jsonl"), cursorLines)
	// The older flat layout, last used months ago, is still offered.
	writeCursorEditorChat(t, cursor, project, older, `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>Analuze my finances so afr</user_query>"}]}}`+"\n", false, time.Now().AddDate(0, -5, 0))
	// A Cursor CLI chat is offered from its own database, not its transcript.
	writeCursorEditorChat(t, cursor, project, cli, cursorLines, true, time.Now())
	if err := os.MkdirAll(filepath.Join(cursor, "chats", "0123abcd", cli), 0o700); err != nil {
		t.Fatal(err)
	}

	st := &importStore{projects: []domain.ProjectRecord{{ID: "fin", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{cursorTranscriptSource: cursor, domain.HarnessClaudeCode: claude}
	preview, err := svc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, c := range preview.Candidates {
		titles = append(titles, c.Title)
		if c.Harness != domain.HarnessClaudeCode || c.Source != domain.HarnessCursor || c.ProjectID != "fin" {
			t.Fatalf("candidate = %+v", c)
		}
	}
	if want := []string{"analyze all the real estate project options i have", "Analuze my finances so afr"}; !slices.Equal(titles, want) {
		t.Fatalf("titles = %q, want %q", titles, want)
	}
	chat := preview.Candidates[0]
	if chat.MessageCount != 4 {
		t.Fatalf("message count = %d, want 4", chat.MessageCount)
	}

	results, err := svc.Import(ctx, []string{chat.ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %+v, %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Harness != domain.HarnessClaudeCode || rec.ProjectID != "fin" || rec.Metadata.AgentSessionID != main || rec.Metadata.ImportSource.ConfigDir != canonical(claude) || rec.Metadata.ImportSource.CWD != project {
		t.Fatalf("session = %+v, import = %+v", rec, rec.Metadata.ImportSource)
	}
	var shown []string
	for _, m := range st.messages {
		shown = append(shown, string(m.Role)+": "+m.Text)
	}
	want := []string{
		"user: analyze all the real estate project options i have",
		"assistant: I'll read your notes first.\n\nYou have three options: A, B and C.",
		"user: which one is cheapest?",
		"assistant: Option B.",
	}
	if !slices.Equal(shown, want) {
		t.Fatalf("archive = %q, want %q", shown, want)
	}
	if opened := time.Date(2026, 6, 13, 3, 35, 0, 0, time.UTC); !st.messages[0].At.Equal(opened) || !st.messages[1].At.Equal(opened) {
		t.Fatalf("times = %v, %v, want %v", st.messages[0].At, st.messages[1].At, opened)
	}

	// The written transcript is one Claude Code reads back, in Claude's project folder for the chat.
	written := rec.Metadata.NativeTranscriptPath
	if written != filepath.Join(canonical(claude), "projects", claudeProjectDirChars.ReplaceAllString(project, "-"), main+".jsonl") {
		t.Fatalf("written to %s", written)
	}
	body, err := os.ReadFile(written)
	if err != nil {
		t.Fatal(err)
	}
	var parent *string
	for i, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var l claudeLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatal(err)
		}
		if l.SessionID != main || l.CWD != project || (parent == nil) != (l.ParentUUID == nil) || parent != nil && *parent != *l.ParentUUID || l.Type != []string{"user", "assistant"}[i%2] {
			t.Fatalf("line %d = %s", i, line)
		}
		parent = &l.UUID
	}
	if err := svc.RefreshArchive(ctx, rec); err != nil {
		t.Fatal(err)
	}
	shown = shown[:0]
	for _, m := range st.messages {
		shown = append(shown, string(m.Role)+": "+m.Text)
	}
	if !slices.Equal(shown, want) {
		t.Fatalf("Claude Code reads back %q, want %q", shown, want)
	}

	// Rescanning offers the chat once, as the imported Claude Code chat.
	preview, err = svc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var imported int
	for _, c := range preview.Candidates {
		if c.Title == chat.Title {
			imported++
			if c.SessionID != rec.ID || c.Source != "" {
				t.Fatalf("rescan candidate = %+v", c)
			}
		}
	}
	if imported != 1 || len(preview.Candidates) != 2 {
		t.Fatalf("rescan = %+v", preview.Candidates)
	}
}

func TestCursorTranscriptWithoutQueryTagsKeepsEveryPrompt(t *testing.T) {
	withoutCursorEditorState(t)
	cursor, project := t.TempDir(), canonical(t.TempDir())
	path := writeCursorEditorChat(t, cursor, project, "22222222-2222-4222-8222-222222222222", `{"role":"assistant","message":{"content":[{"type":"text","text":"Welcome back."}]}}
{"role":"user","message":{"content":"plain prompt"}}
{"role":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}
`, true, time.Now())
	got, err := readCursorTranscriptFile(context.Background(), cursor, []string{project}, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.messages) != 3 || got.messages[1].Text != "plain prompt" || got.title != "plain prompt" {
		t.Fatalf("messages = %+v", got.messages)
	}
	// Claude Code is given turns that open with the user.
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	written, err := writeCursorTranscript(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	back, err := readClaudeTranscript(context.Background(), claude, written.path)
	if err != nil || len(back.messages) != 2 || back.messages[0].Text != "plain prompt" {
		t.Fatalf("read back = %+v, %v", back.messages, err)
	}
	// Importing it again, once its session has ended, continues the transcript written the first time.
	lines := strings.Split(strings.TrimSpace(string(mustRead(t, written.path))), "\n")
	var last claudeLine
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	continued, err := json.Marshal(claudeLine{ParentUUID: &last.UUID, Type: "user", SessionID: last.SessionID, CWD: last.CWD, UUID: "33333333-3333-4333-8333-333333333333", Timestamp: claudeTimestamp(time.Now()), Message: json.RawMessage(`{"role":"user","content":"continued in AO"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(written.path, append(mustRead(t, written.path), append(continued, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := writeCursorTranscript(context.Background(), got)
	if err != nil || again.path != written.path || again.messages[len(again.messages)-1].Text != "continued in AO" {
		t.Fatalf("second import = %+v, %v", again, err)
	}
}

func TestCursorWorkspaceRecoversTheFolder(t *testing.T) {
	withoutCursorEditorState(t)
	cursor, base := t.TempDir(), canonical(t.TempDir())
	dotted, dashed, plain := filepath.Join(base, "my.app"), filepath.Join(base, "my-app"), filepath.Join(base, "plain")
	for _, dir := range []string{dotted, dashed, plain} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// "my.app" and "my-app" share Cursor's folder name; only the trusted path tells them apart.
	trusted, err := json.Marshal(map[string]string{"trustedAt": "2026-06-13T03:35:00.000Z", "workspacePath": dotted})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cursor, "projects", cursorFolderName(dotted), ".workspace-trusted"), string(trusted))
	if got := cursorWorkspace(cursor, cursorFolderName(dotted), []string{dashed}, nil); got != dotted {
		t.Fatalf("trusted = %q, want %q", got, dotted)
	}
	if got := cursorWorkspace(cursor, cursorFolderName(plain), []string{dashed, plain}, nil); got != plain {
		t.Fatalf("registered = %q, want %q", got, plain)
	}
	// With no record of the folder, the name is read as a path when that path exists.
	if spelled := strings.ReplaceAll(strings.Trim(plain, "/"), "/", "-"); spelled == cursorFolderName(plain) {
		if got := cursorWorkspace(cursor, spelled, nil, nil); got != plain {
			t.Fatalf("spelled = %q, want %q", got, plain)
		}
	}
	// Two registered projects with one folder name and no trusted path are ambiguous.
	// Two registered projects named alike choose neither, nor the folder the name spells.
	if err := os.MkdirAll(filepath.Join(base, "x", "y"), 0o700); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(base, "x-y"), filepath.Join(base, "x_y")
	if cursorWorkspace(cursor, cursorFolderName(a), []string{a, b}, nil) != "" {
		t.Fatal("two registered projects named alike chose a folder")
	}
	// The editor's chat list settles it, when that folder exists.
	if err := os.MkdirAll(b, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := cursorWorkspace(cursor, cursorFolderName(a), []string{a, b}, func() string { return b }); got != b {
		t.Fatalf("recorded = %q, want %q", got, b)
	}
	// A recorded path comes before a project that merely shares the name.
	if got := cursorWorkspace(cursor, cursorFolderName(b), []string{a}, func() string { return b }); got != b {
		t.Fatalf("recorded over a name match = %q, want %q", got, b)
	}
	if got := cursorWorkspace(cursor, "Users-nobody-gone", nil, nil); got != "" {
		t.Fatalf("missing = %q, want none", got)
	}
	// A transcript whose folder cannot be recovered is not offered.
	path := writeCursorEditorChat(t, cursor, "/Users/nobody/gone", "33333333-3333-4333-8333-333333333333", cursorLines, true, time.Now())
	if _, err := readCursorTranscriptFile(context.Background(), cursor, nil, path); !errors.Is(err, errNoReadableConversation) {
		t.Fatalf("read = %v, want errNoReadableConversation", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCursorFolderNameMatchesCursor(t *testing.T) {
	for path, want := range map[string]string{
		"/Users/dhruvsharma/fin":            "Users-dhruvsharma-fin",
		"/Users/christophe.vidal/Documents": "Users-christophe-vidal-Documents",
		"/tmp/x//y_z/":                      "tmp-x-y-z",
		`C:\Users\me\repo`:                  "C-Users-me-repo",
	} {
		if got := cursorFolderName(path); got != want {
			t.Errorf("cursorFolderName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCursorTime(t *testing.T) {
	for text, want := range map[string]time.Time{
		"Tuesday, Jun 2, 2026, 11:20 AM (UTC+8)":      time.Date(2026, 6, 2, 3, 20, 0, 0, time.UTC),
		"Tuesday, Sep 22, 2026, 2:35\u202fPM (UTC-3)": time.Date(2026, 9, 22, 17, 35, 0, 0, time.UTC),
		"Saturday, June 13, 2026, 9:05 AM (UTC+5:30)": time.Date(2026, 6, 13, 3, 35, 0, 0, time.UTC),
		"Tuesday, Nov 17, 2026, 18:40 (UTC)":          time.Date(2026, 11, 17, 18, 40, 0, 0, time.UTC),
		"Tuesday, Jun 2, 2026, 11:20 AM (CEST)":       {},
		"Tuesday, Jun 2, 2026, 11:20 AM":              {},
		"Tuesday, Jun 2, 2026, 11:20 AM (UTC+2.5)":    {},
		"<timestamp>not a date</timestamp> (UTC+1)":   {},
	} {
		if got := cursorTime(text); !got.Equal(want) {
			t.Errorf("cursorTime(%q) = %v, want %v", text, got, want)
		}
	}
}
