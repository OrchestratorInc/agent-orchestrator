package sessionimport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDevinActiveChain(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE sessions(id TEXT,working_directory TEXT,title TEXT,last_activity_at INTEGER,hidden INTEGER,main_chain_id INTEGER)`,
		`CREATE TABLE message_nodes(session_id TEXT,node_id INTEGER,parent_node_id INTEGER,chat_message TEXT,created_at INTEGER)`,
		`INSERT INTO sessions VALUES('gentle-otter','/work/repo',NULL,1790000000000,0,3),('hidden-chat','/work/repo',NULL,0,1,NULL)`,
		`INSERT INTO message_nodes VALUES('gentle-otter',0,NULL,'{"role":"system","content":"injected context"}',1790000000000)`,
		`INSERT INTO message_nodes VALUES('gentle-otter',1,0,'{"role":"user","content":"Fix the widget","metadata":{"is_user_input":true}}',1790000000001)`,
		`INSERT INTO message_nodes VALUES('gentle-otter',2,1,'{"role":"user","content":"tool noise","metadata":{"is_user_input":false}}',1790000000002)`,
		`INSERT INTO message_nodes VALUES('gentle-otter',3,2,'{"role":"assistant","content":"Done"}',1790000000003)`,
		`INSERT INTO message_nodes VALUES('gentle-otter',4,1,'{"role":"assistant","content":"abandoned branch"}',1790000000004)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	files, err := discoverDevin(context.Background(), root)
	if err != nil || len(files) != 1 || files[0].nativeID() != "gentle-otter" {
		t.Fatalf("discovery: %+v %v", files, err)
	}
	got, err := readDevinTranscript(context.Background(), root, "gentle-otter")
	if err != nil {
		t.Fatal(err)
	}
	if got.cwd != "/work/repo" || got.title != "Fix the widget" || len(got.messages) != 2 || got.messages[1].Text != "Done" {
		t.Fatalf("transcript: %+v", got)
	}
	if _, err := readDevinTranscript(context.Background(), root, "hidden-chat"); err == nil {
		t.Fatal("hidden session accepted")
	}
}

func TestDevinMissingStoreAndRoot(t *testing.T) {
	root := t.TempDir()
	files, err := discoverDevin(context.Background(), root)
	if err != nil || len(files) != 0 {
		t.Fatalf("missing store: %+v %v", files, err)
	}
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	if got := devinRoot("/home/test"); got != filepath.Join("/custom/data", "devin", "cli") {
		t.Fatal(got)
	}
}
