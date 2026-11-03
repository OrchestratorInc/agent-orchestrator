package sessionimport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMiMoCodeAdmissionRemainsDeferred(t *testing.T) {
	if _, enabled := harnessImporters[domain.HarnessMiMoCode]; enabled {
		t.Fatal("deferred native export must not admit new MiMo imports")
	}
}

func TestMiMoCodeImport(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "mimocode.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, title TEXT, time_updated INTEGER, time_archived INTEGER)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	addSession(t, db, "ses_synthetic", "", "/work/source", "Synthetic MiMo conversation", 1793613600000, nil)
	addSession(t, db, "ses_child", "ses_synthetic", "/work/source", "child", 1793613600000, nil)
	addMessage(t, db, "ses_synthetic", "msg_user", "user", 1793613600000, "synthetic:injected context", "text:Remember synthetic code word", "ignored:hidden")
	addMessage(t, db, "ses_synthetic", "msg_reply", "assistant", 1793613601000, "reasoning", "text:Remembered", "tool")
	files, err := discoverMiMoCode(context.Background(), root)
	if err != nil || len(files) != 1 || files[0].nativeID() != "ses_synthetic" {
		t.Fatalf("discover = %v, %v", files, err)
	}
	got, err := readTranscript(context.Background(), domain.HarnessMiMoCode, root, files[0].paths...)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Remember synthetic code word", "assistant: Remembered")
	if got.cwd != "/work/source" || got.title != "Synthetic MiMo conversation" || got.nativeID != "ses_synthetic" {
		t.Fatalf("metadata = %+v", got)
	}
	if _, err := readMiMoCodeTranscript(context.Background(), root, "../escape"); err == nil {
		t.Fatal("accepted invalid id")
	}
}

func TestMiMoCodeRoot(t *testing.T) {
	t.Setenv("MIMOCODE_DB", "")
	t.Setenv("MIMOCODE_HOME", "/custom/mimo")
	if got := mimoCodeRoot("/home/example"); got != filepath.Join("/custom/mimo", "data") {
		t.Fatal(got)
	}
	t.Setenv("MIMOCODE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	if got := mimoCodeRoot("/home/example"); got != filepath.Join("/home/example", ".local", "share", "mimocode") {
		t.Fatal(got)
	}
	t.Setenv("MIMOCODE_DB", "/custom/other.db")
	if got := mimoCodeRoot("/home/example"); len(got) != 0 {
		t.Fatal("custom database must not import against default resume binding")
	}
}
