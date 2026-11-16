package chat

import (
	"crypto/md5" //nolint:gosec // matches Cursor's folder naming.
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHandoffHistoryFilesPointAtAgentsAOHasNoAnswersFor(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "worktrees", "scratch-1")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	kimiDir := filepath.Join(data, "kimi", "sessions", "wd_scratch-1_x", "session_a")
	write(filepath.Join(kimiDir, "agents", "main", "wire.jsonl"), "{}\n")
	write(filepath.Join(data, "kimi", "session_index.jsonl"),
		`{"sessionId":"session_gone","sessionDir":"`+filepath.Join(data, "kimi", "gone")+`","workDir":"`+workspace+`"}`+"\n"+
			`{"sessionId":"session_gone","deleted":true}`+"\n"+
			`{"sessionId":"session_a","sessionDir":"`+kimiDir+`","workDir":"`+workspace+`"}`+"\n"+
			`{"sessionId":"session_b","sessionDir":"`+filepath.Join(data, "other")+`","workDir":"/elsewhere"}`+"\n")
	write(filepath.Join(home, ".factory", "sessions", strings.ReplaceAll(workspace, "/", "-"), "d1.jsonl"), "{}\n")
	key := md5.Sum([]byte(workspace)) //nolint:gosec // matches Cursor's folder naming.
	write(filepath.Join(home, ".cursor", "chats", hex.EncodeToString(key[:]), "c1", "store.db"), "db")

	s := &Service{dataDir: data}
	record := domain.SessionRecord{Harness: domain.HarnessKimi, Metadata: domain.SessionMetadata{WorkspacePath: workspace}}
	turns := []domain.HandoffTurn{
		{Harness: domain.HarnessCursor, UserRequest: "asked"},
		{Harness: domain.HarnessDroid, UserRequest: "asked", AgentResponse: "answered"},
	}
	files := s.handoffHistoryFiles(record, turns)
	got := map[domain.AgentHarness]domain.HandoffHistoryFile{}
	for _, file := range files {
		got[file.Harness] = file
	}
	if len(files) != 2 || got[domain.HarnessKimi].Path != filepath.Join(kimiDir, "agents", "main", "wire.jsonl") {
		t.Fatalf("files = %+v, want Kimi's live session and Cursor only", files)
	}
	if got[domain.HarnessKimi].RequestsOnly || !got[domain.HarnessCursor].RequestsOnly {
		t.Fatalf("requests-only flags = %+v", files)
	}
	note := domain.BuildHandoffNote(domain.HandoffNoteInput{SourceHarness: domain.HarnessKimi, TargetHarness: domain.HarnessClaudeCode, Turns: []domain.HandoffTurn{{UserRequest: "hi"}}, HistoryFiles: files})
	if !strings.Contains(note, "## Earlier agents' own history") || !strings.Contains(note, "- kimi: `"+got[domain.HarnessKimi].Path+"`") {
		t.Fatalf("note does not point at Kimi's history:\n%s", note)
	}
}

func TestHandoffHistoryFilesSkipChatsOlderThanTheSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "worktrees", "scratch-1")
	old := filepath.Join(home, ".factory", "sessions", strings.ReplaceAll(workspace, "/", "-"), "old.jsonl")
	if err := os.MkdirAll(filepath.Dir(old), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := time.Now()
	if err := os.Chtimes(old, created.Add(-time.Hour), created.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	s := &Service{dataDir: t.TempDir()}
	record := domain.SessionRecord{Harness: domain.HarnessClaudeCode, CreatedAt: created, Metadata: domain.SessionMetadata{WorkspacePath: workspace}}
	if files := s.handoffHistoryFiles(record, nil); len(files) != 0 {
		t.Fatalf("files = %+v, want none: Droid never ran in this session", files)
	}
}
