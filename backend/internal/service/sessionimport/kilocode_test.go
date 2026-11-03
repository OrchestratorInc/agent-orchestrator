package sessionimport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestKiloCodeAdmissionRemainsDeferred(t *testing.T) {
	if _, enabled := harnessImporters[domain.HarnessKilocode]; enabled {
		t.Fatal("deferred native export must not admit new Kilo imports")
	}
}

func TestKiloCodeImport(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "kilo.db")))
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
	addSession(t, db, "ses_synthetic", "", "/work/source", "Synthetic Kilo conversation", 1793613600000, nil)
	addSession(t, db, "ses_child", "ses_synthetic", "/work/source", "child", 1793613600000, nil)
	addMessage(t, db, "ses_synthetic", "msg_user", "user", 1793613600000, "synthetic:injected context", "text:Remember synthetic code word", "ignored:hidden")
	addMessage(t, db, "ses_synthetic", "msg_reply", "assistant", 1793613601000, "reasoning", "text:Remembered", "tool")
	files, err := discoverKiloCode(context.Background(), root)
	if err != nil || len(files) != 1 || files[0].nativeID() != "ses_synthetic" {
		t.Fatalf("discover = %v, %v", files, err)
	}
	got, err := readKiloCodeTranscript(context.Background(), root, files[0].paths...)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Remember synthetic code word", "assistant: Remembered")
	if got.cwd != "/work/source" || got.title != "Synthetic Kilo conversation" || got.nativeID != "ses_synthetic" {
		t.Fatalf("metadata = %+v", got)
	}
	if _, err := readKiloCodeTranscript(context.Background(), root, "../escape"); err == nil {
		t.Fatal("accepted invalid id")
	}
}

func TestKiloCodeRoot(t *testing.T) {
	t.Setenv("KILO_DB", "")
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	if got := kiloCodeRoot("/home/example"); got != filepath.Join("/custom/data", "kilo") {
		t.Fatal(got)
	}
	t.Setenv("XDG_DATA_HOME", "")
	if got := kiloCodeRoot("/home/example"); got != filepath.Join("/home/example", ".local", "share", "kilo") {
		t.Fatal(got)
	}
}
