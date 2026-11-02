package sessionimport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHermesLocalTranscript(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		`CREATE TABLE sessions(id TEXT,source TEXT,cwd TEXT,title TEXT,started_at REAL,hidden INTEGER)`,
		`CREATE TABLE messages(id INTEGER,session_id TEXT,role TEXT,content TEXT,timestamp REAL,_compressed_summary INTEGER)`,
		`INSERT INTO sessions VALUES('20261102_120000_test','cli','/work/repo',NULL,1790000000,0),('gateway','telegram','/work',NULL,1790000000,0)`,
		`INSERT INTO messages VALUES(1,'20261102_120000_test','system','injected context',1790000000,0)`,
		`INSERT INTO messages VALUES(2,'20261102_120000_test','user','Fix the widget',1790000001,0)`,
		`INSERT INTO messages VALUES(3,'20261102_120000_test','tool','tool output',1790000002,0)`,
		`INSERT INTO messages VALUES(4,'20261102_120000_test','assistant','Done',1790000003,0)`,
		`INSERT INTO messages VALUES(5,'20261102_120000_test','user','summary noise',1790000004,1)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	files, err := discoverHermes(context.Background(), root)
	if err != nil || len(files) != 1 || files[0].nativeID() != "20261102_120000_test" {
		t.Fatalf("discovery=%+v err=%v", files, err)
	}
	got, err := readHermesTranscript(context.Background(), root, "20261102_120000_test")
	if err != nil {
		t.Fatal(err)
	}
	if got.title != "Fix the widget" || got.cwd != "/work/repo" || len(got.messages) != 2 || got.messages[1].Text != "Done" {
		t.Fatalf("transcript=%+v", got)
	}
}

func TestHermesMissingStore(t *testing.T) {
	files, err := discoverHermes(context.Background(), t.TempDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("files=%+v err=%v", files, err)
	}
}
