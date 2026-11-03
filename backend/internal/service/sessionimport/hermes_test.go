package sessionimport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestHermesImportStaysTerminalWithRegisteredChatDriver(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`CREATE TABLE sessions(id TEXT,source TEXT,cwd TEXT,title TEXT,started_at REAL,hidden INTEGER)`,
		`CREATE TABLE messages(id INTEGER,session_id TEXT,role TEXT,content TEXT,timestamp REAL,_compressed_summary INTEGER)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO sessions VALUES('20261103_000000_mode','cli',?,'',1790000000,0)`, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages VALUES(1,'20261103_000000_mode','user','synthetic mode request',1790000001,0)`); err != nil {
		t.Fatal(err)
	}
	store := &importStore{projects: []domain.ProjectRecord{{ID: "hermes-mode", Path: workspace}}}
	service := New(store, "", chatSupported)
	service.roots = map[domain.AgentHarness]string{domain.HarnessHermes: root}
	preview, err := service.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	results, err := service.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" || len(store.sessions) != 1 {
		t.Fatalf("import = %#v, sessions = %#v, error = %v", results, store.sessions, err)
	}
	record := store.sessions[0]
	if record.Mode != domain.SessionModeTUI || record.ProjectID != "hermes-mode" || record.Metadata.AgentSessionID != "20261103_000000_mode" || !record.NeedsImportResume() || len(store.messages) != 1 {
		t.Fatalf("native Terminal import = %#v, messages = %#v", record, store.messages)
	}
}

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
		`INSERT INTO messages VALUES(2,'20261102_120000_test','user','[fix] the widget',1790000001,0)`,
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
	if got.title != "[fix] the widget" || got.cwd != "/work/repo" || len(got.messages) != 2 || got.messages[1].Text != "Done" {
		t.Fatalf("transcript=%+v", got)
	}
}

func TestHermesMissingStore(t *testing.T) {
	files, err := discoverHermes(context.Background(), t.TempDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("files=%+v err=%v", files, err)
	}
}

func TestHermesCompressionLineageIsOneNativeConversation(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`CREATE TABLE sessions(id TEXT,source TEXT,cwd TEXT,title TEXT,started_at REAL,hidden INTEGER,parent_session_id TEXT,end_reason TEXT,ended_at REAL,model_config TEXT)`,
		`CREATE TABLE messages(id INTEGER,session_id TEXT,role TEXT,content TEXT,timestamp REAL,_compressed_summary INTEGER)`,
		`INSERT INTO sessions VALUES('root','cli','/work/repo','native title',1790000000,1,NULL,'compression',1790000003,'{}'),('tip','cli','/work/repo',NULL,1790000004,0,'root',NULL,NULL,'{}'),('branch','cli','/work/repo','branch',1790000005,0,'root',NULL,NULL,'{"_branched_from":"root"}'),('reset','cli','/work/repo','reset',1790000006,0,'root',NULL,NULL,'{"_reset_from":"root"}')`,
		`INSERT INTO messages VALUES(1,'root','user','original request',1790000001,0),(2,'root','assistant','original answer',1790000002,0),(3,'tip','user','compressed summary',1790000004,1),(4,'tip','user','follow up',1790000005,0),(5,'tip','assistant','continued answer',1790000006,0),(6,'branch','user','independent branch',1790000007,0),(7,'reset','user','independent reset',1790000008,0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	files, err := discoverHermes(context.Background(), root)
	if err != nil || len(files) != 3 {
		t.Fatalf("lineages=%+v err=%v", files, err)
	}
	for _, file := range files {
		if file.nativeID() == "tip" {
			t.Fatal("compression tip offered as a second conversation")
		}
	}
	got, err := readHermesTranscript(context.Background(), root, "root")
	if err != nil || got.nativeID != "root" || got.title != "native title" || len(got.messages) != 4 {
		t.Fatalf("lineage=%+v err=%v", got, err)
	}
	for i, want := range []string{"original request", "original answer", "follow up", "continued answer"} {
		if got.messages[i].Text != want {
			t.Fatalf("message %d=%q want=%q", i, got.messages[i].Text, want)
		}
	}
	if _, err := readHermesTranscript(context.Background(), root, "tip"); err == nil {
		t.Fatal("accepted a duplicate continuation handle")
	}
	// A fresh read must not aggregate or decode any unrelated conversation.
	// This malformed gateway timestamp deliberately cannot scan into float64.
	if _, err := db.Exec(`INSERT INTO sessions VALUES('gateway','telegram','/gateway',NULL,1790000000,0,NULL,NULL,NULL,'{}');
		INSERT INTO messages VALUES(8,'gateway','user','unrelated','not-a-timestamp',0)`); err != nil {
		t.Fatal(err)
	}
	groups, err := hermesLineages(context.Background(), db, "root")
	if err != nil || len(groups["root"]) != 2 {
		t.Fatalf("scoped compression graph=%+v err=%v", groups, err)
	}
	if _, included := groups["gateway"]; included {
		t.Fatal("unrelated conversation entered per-candidate lookup")
	}
	if _, err := readHermesTranscript(context.Background(), root, "root"); err != nil {
		t.Fatalf("unrelated gateway history blocked fresh import validation: %v", err)
	}
}
