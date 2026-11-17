package sessionimport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// newCursorEditorState creates a state.vscdb with the two tables Cursor's
// editor keeps chats in, points the importer at it and returns its path.
func newCursorEditorState(t *testing.T, items, chats map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`,
		`CREATE TABLE cursorDiskKV (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(table string, rows map[string]any) {
		for key, value := range rows {
			body, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO `+table+` (key, value) VALUES (?, ?)`, key, body); err != nil {
				t.Fatal(err)
			}
		}
	}
	insert("ItemTable", items)
	insert("cursorDiskKV", chats)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	saved := cursorEditorState
	cursorEditorState = func() string { return path }
	t.Cleanup(func() { cursorEditorState = saved })
	return path
}

func TestCursorEditorDatabaseGivesTheFullChat(t *testing.T) {
	ctx := context.Background()
	cursor := t.TempDir()
	// A dotted folder name that no project holds: only the editor's chat list names it.
	project := filepath.Join(canonical(t.TempDir()), "my.fin")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	const id, other = "0e9e5078-f991-4c92-b998-5eca09b5f7c3", "ec206e49-7fa8-4fec-876b-6de840ec2469"
	opened := time.Date(2026, 6, 13, 3, 35, 0, 0, time.UTC)
	state := newCursorEditorState(t,
		map[string]any{
			"composer.composerHeaders": map[string]any{"allComposers": []any{
				map[string]any{"composerId": other, "workspaceIdentifier": map[string]any{"id": "1", "uri": map[string]any{"fsPath": "/elsewhere", "scheme": "file"}}},
				map[string]any{"composerId": id, "name": "Real estate options", "workspaceIdentifier": map[string]any{"id": "2", "uri": map[string]any{"fsPath": project, "scheme": "file"}}},
			}},
			"cursorAuth/accessToken": "secret",
		},
		map[string]any{
			"composerData:" + id: map[string]any{"_v": 13, "composerId": id, "name": "Real estate options", "fullConversationHeadersOnly": []any{
				map[string]any{"bubbleId": "b1", "type": 1}, map[string]any{"bubbleId": "b2", "type": 2}, map[string]any{"bubbleId": "b3", "type": 2},
				map[string]any{"bubbleId": "b4", "type": 1}, map[string]any{"bubbleId": "b5", "type": 2},
			}},
			// Keys sort apart from the conversation's order; the headers order it.
			"bubbleId:" + id + ":b5":    map[string]any{"type": 2, "text": "Option B, at 65 lakh.", "createdAt": opened.Add(48 * time.Hour).Format(time.RFC3339Nano)},
			"bubbleId:" + id + ":b1":    map[string]any{"type": 1, "text": "analyze all the real estate project options i have", "createdAt": opened.UnixMilli()},
			"bubbleId:" + id + ":b2":    map[string]any{"type": 2, "text": "", "toolFormerData": map[string]any{"name": "read_file"}, "createdAt": opened.Add(time.Minute).UnixMilli()},
			"bubbleId:" + id + ":b3":    map[string]any{"type": 2, "text": "You have three: A at 80 lakh, B at 65 lakh and C at 90 lakh.", "createdAt": opened.Add(2 * time.Minute).UnixMilli()},
			"bubbleId:" + id + ":b4":    map[string]any{"type": 1, "text": "which one is cheapest?", "createdAt": opened.Add(47 * time.Hour).UnixMilli()},
			"bubbleId:" + other + ":b1": map[string]any{"type": 1, "text": "another chat"},
		})
	before := mustRead(t, state)
	path := writeCursorEditorChat(t, cursor, project, id, `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>analyze all the real estate project options i have</user_query>"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"You have three: A at [REDACTED], B at [REDACTED] and C at [REDACTED].\n\n[REDACTED]"}]}}
`, true, time.Now())

	got, err := readCursorTranscriptFile(ctx, cursor, nil, path)
	if err != nil {
		t.Fatal(err)
	}
	var shown []string
	for _, m := range got.messages {
		shown = append(shown, string(m.Role)+": "+m.Text)
	}
	want := []string{
		"user: analyze all the real estate project options i have",
		"assistant: You have three: A at 80 lakh, B at 65 lakh and C at 90 lakh.",
		"user: which one is cheapest?",
		"assistant: Option B, at 65 lakh.",
	}
	if !slices.Equal(shown, want) {
		t.Fatalf("messages = %q, want %q", shown, want)
	}
	if got.cwd != project || got.title != "Real estate options" || !got.messages[0].At.Equal(opened) || !got.messages[3].At.Equal(opened.Add(48*time.Hour)) {
		t.Fatalf("cwd = %q, title = %q, times = %v, %v", got.cwd, got.title, got.messages[0].At, got.messages[3].At)
	}
	// A database copy that is behind the transcript leaves the transcript standing.
	ahead := writeCursorEditorChat(t, cursor, project, id, `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>analyze all the real estate project options i have</user_query>"}]}}
{"role":"user","message":{"content":[{"type":"text","text":"<user_query>which one is cheapest?</user_query>"}]}}
{"role":"user","message":{"content":[{"type":"text","text":"<user_query>and the largest?</user_query>"}]}}
`, true, time.Now())
	if behind, err := readCursorTranscriptFile(ctx, cursor, nil, ahead); err != nil || len(behind.messages) != 3 || behind.messages[2].Text != "and the largest?" {
		t.Fatalf("transcript ahead of the database = %+v, %v", behind.messages, err)
	}
	// Cursor's database is only read.
	if !bytes.Equal(mustRead(t, state), before) {
		t.Fatal("reading changed Cursor's database")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(state + suffix); !os.IsNotExist(err) {
			t.Fatalf("reading created %s next to Cursor's database: %v", suffix, err)
		}
	}
}

// Cursor 3.27 can leave a chat's headers empty; its messages' times order it then.
func TestCursorEditorDatabaseOrdersMessagesWithoutHeaders(t *testing.T) {
	const id = "34fb2adb-9cc1-4437-b94a-de92882f059b"
	at := time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)
	newCursorEditorState(t, nil, map[string]any{
		"composerData:" + id:    map[string]any{"_v": 18, "name": "New Agent", "fullConversationHeadersOnly": []any{}, "conversationMap": map[string]any{}},
		"bubbleId:" + id + ":a": map[string]any{"type": 2, "text": "A turf costs about 40 lakh.", "createdAt": at.Add(time.Minute).UnixMilli()},
		"bubbleId:" + id + ":z": map[string]any{"type": 1, "text": "estimate a turf business", "createdAt": at.UnixMilli()},
	})
	chat, found := readCursorEditorChat(context.Background(), id)
	if !found || len(chat.messages) != 2 || chat.messages[0].Role != domain.MessageRoleUser || chat.messages[1].Text != "A turf costs about 40 lakh." || chat.title != "" {
		t.Fatalf("chat = %+v, found = %v", chat, found)
	}
	if _, found := readCursorEditorChat(context.Background(), "11111111-1111-4111-8111-111111111111"); found {
		t.Fatal("found a chat the database does not hold")
	}
}
