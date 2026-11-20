package sessionimport

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAgyScanUsesEligibleCWDAndNativeIdentity(t *testing.T) {
	root, cwd := canonical(t.TempDir()), canonical(t.TempDir())
	id := "00000000-0000-4000-8000-000000000522"
	path := filepath.Join(root, agy.CLIStorageDir, "conversations", id+".db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"PRAGMA user_version=1", "CREATE TABLE trajectory_meta(trajectory_id TEXT,cascade_id TEXT,trajectory_type INTEGER)", "CREATE TABLE trajectory_metadata_blob(id TEXT,data BLOB)", "CREATE TABLE steps(idx INTEGER,step_type INTEGER,step_format INTEGER,has_subtrajectory INTEGER,metadata BLOB,step_payload BLOB)", "CREATE TABLE parent_references(idx INTEGER,data BLOB)", "CREATE TABLE executor_metadata(idx INTEGER,data BLOB)", "CREATE TABLE gen_metadata(idx INTEGER,data BLOB)", "CREATE TABLE battle_mode_infos(idx INTEGER,data BLOB)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	field := func(n protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), value)
	}
	uriPath := filepath.ToSlash(cwd)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath}).String()
	meta := append(field(1, field(1, []byte(uri))), field(7, []byte(uri))...)
	if _, err := db.Exec("INSERT INTO trajectory_meta VALUES('trajectory',?,4)", id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO trajectory_metadata_blob VALUES('main',?)", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO steps VALUES(0,14,0,0,x'',?)", field(19, field(1, []byte("continue my Agy chat")))); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store := &importStore{projects: []domain.ProjectRecord{{ID: "agy-project", Path: cwd}}}
	svc := New(store, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessAgy: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %+v,%v", preview, err)
	}
	if preview.Candidates[0].Harness != domain.HarnessAgy || preview.Candidates[0].Title != "continue my Agy chat" {
		t.Fatalf("candidate = %+v", preview.Candidates[0])
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %+v,%v", results, err)
	}
	if len(store.sessions) != 1 || store.sessions[0].Mode != domain.SessionModeChat || store.sessions[0].Metadata.AgentSessionID != id || store.sessions[0].Metadata.ImportSource.Fingerprint == "" {
		t.Fatalf("sessions = %+v", store.sessions)
	}
}
