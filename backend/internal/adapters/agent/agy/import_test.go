package agy

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const importTestID = "00000000-0000-4000-8000-000000000522"

func importField(n protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), value)
}

func importFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	path := filepath.Join(root, CLIStorageDir, "conversations", importTestID+".db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		"PRAGMA user_version=1",
		"CREATE TABLE trajectory_meta(trajectory_id TEXT PRIMARY KEY,cascade_id TEXT,trajectory_type INTEGER,source INTEGER)",
		"CREATE TABLE trajectory_metadata_blob(id TEXT PRIMARY KEY,data BLOB)",
		"CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,has_subtrajectory NUMERIC,metadata BLOB,error_details BLOB,permissions BLOB,task_details BLOB,render_info BLOB,step_payload BLOB,step_format INTEGER)",
		"CREATE TABLE gen_metadata(idx INTEGER PRIMARY KEY,data BLOB,size INTEGER)",
		"CREATE TABLE executor_metadata(idx INTEGER PRIMARY KEY,data BLOB)",
		"CREATE TABLE parent_references(idx INTEGER PRIMARY KEY,data BLOB)",
		"CREATE TABLE battle_mode_infos(idx INTEGER PRIMARY KEY,data BLOB)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO trajectory_meta VALUES('trajectory',?,4,17)", importTestID); err != nil {
		t.Fatal(err)
	}
	uri := importWorkspaceURI(cwd)
	meta := append(importField(1, importField(1, []byte(uri))), importField(7, []byte(uri))...)
	if _, err := db.Exec("INSERT INTO trajectory_metadata_blob VALUES('main',?)", meta); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		kind  int
		field protowire.Number
		text  string
	}{
		{14, 19, "remember the lime"}, {15, 20, "remembered"},
	} {
		payload := importField(item.field, importField(1, []byte(item.text)))
		if _, err := db.Exec("INSERT INTO steps(idx,step_type,status,has_subtrajectory,step_payload,step_format) VALUES(?,?,3,0,?,0)", i, item.kind, payload); err != nil {
			t.Fatal(err)
		}
	}
	return root, path, cwd
}

func TestImportArchiveReadsVisibleHistoryWithoutSourceWrites(t *testing.T) {
	root, path, cwd := importFixture(t)
	before, err := ImportFingerprint(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := ReadImportArchive(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	if archive.ID != importTestID || archive.CWD != cwd || len(archive.Messages) != 2 || archive.Messages[0].Text != "remember the lime" || archive.Messages[1].Text != "remembered" {
		t.Fatalf("archive = %+v", archive)
	}
	after, err := ImportFingerprint(context.Background(), root, path)
	if err != nil || before != after {
		t.Fatalf("source changed: %v", err)
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("reader created a WAL: %v", err)
	}
}

func TestNativeProbeWithoutRecordedProfileIsInconclusive(t *testing.T) {
	got, err := New().ProbeNativeSession(context.Background(), ports.NativeSessionRef{NativeSessionID: importTestID})
	if err != nil || got != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("unbound profile = %v,%v", got, err)
	}
}

func TestImportArchiveRefusesUnsupportedAndActiveSources(t *testing.T) {
	for _, test := range []struct {
		name, query string
		wal         bool
	}{
		{"schema", "PRAGMA user_version=2", false},
		{"format", "UPDATE steps SET step_format=1", false},
		{"identity", "UPDATE trajectory_meta SET cascade_id='other'", false},
		{"subtrajectory", "UPDATE steps SET has_subtrajectory=1", false},
		{"empty", "DELETE FROM steps", false},
		{"unknown table", "CREATE TABLE sqliteXunexpected(secret TEXT)", false},
		{"wal", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, path, _ := importFixture(t)
			if test.query != "" {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(test.query)
				_ = db.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.wal {
				if err := os.WriteFile(path+"-wal", []byte("active"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadImportArchive(context.Background(), root, path); err == nil {
				t.Fatal("accepted unsupported history")
			}
		})
	}
}

func TestPrepareImportedSessionRemapsOnlyPrivateCopyAndReusesIt(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t)
	fingerprint, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	launch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: launch, Chat: true, SourceFingerprint: fingerprint, DataDir: t.TempDir()}
	ref, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if ref.NativeSessionID != importTestID || ref.ConfigDir == root {
		t.Fatalf("ref = %+v", ref)
	}
	copied, err := ReadImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath)
	if err != nil || copied.CWD != launch || len(copied.Messages) != 2 {
		t.Fatalf("copy = %+v, %v", copied, err)
	}
	db, err := sql.Open("sqlite", ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO steps(idx,step_type,status,has_subtrajectory,step_payload,step_format) VALUES(2,14,3,0,?,0)", importField(19, importField(2, []byte("AO later turn"))))
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if after, err := ImportFingerprint(ctx, root, path); err != nil || after != fingerprint {
		t.Fatalf("source changed: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	again, err := p.PrepareImportedSession(ctx, r)
	if err != nil || again != ref {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	later, err := ReadImportArchive(ctx, again.ConfigDir, again.TranscriptPath)
	if err != nil || len(later.Messages) != 3 || later.Messages[2].Text != "AO later turn" {
		t.Fatalf("lost later turns: %+v,%v", later, err)
	}
	if err := os.Remove(ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareImportedSession(ctx, r); err == nil {
		t.Fatal("missing prepared history accepted")
	}
}

func TestPrepareImportedSessionRefusesUnknownBinaryAndChangedSelection(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t)
	p := New()
	p.importVersion = func(context.Context) (string, error) { return "1.5.14", nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), Chat: true, SourceFingerprint: "wrong"}
	if _, err := p.PrepareImportedSession(ctx, r); err == nil {
		t.Fatal("unknown binary accepted")
	}
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	if _, err := p.PrepareImportedSession(ctx, r); err == nil {
		t.Fatal("changed selection accepted")
	}
}

func TestImportedTerminalRestoreUsesPrivateRootAndRefusesMissingHistory(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t)
	fingerprint, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	launch := t.TempDir()
	p := &Plugin{resolvedBinary: "agy", importVersion: func(context.Context) (string, error) { return supportedImportVersion, nil }}
	ref, err := p.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: launch, DataDir: t.TempDir(), SourceFingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: launch, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: ref.NativeSessionID, ports.MetadataKeyImportSourceNativeID: importTestID, ports.MetadataKeyImportResumeNativeID: ref.NativeSessionID, ports.MetadataKeyImportConfigDir: ref.ConfigDir}}}
	cmd, ok, err := p.GetRestoreCommand(ctx, cfg)
	if err != nil || !ok {
		t.Fatalf("restore = %v,%v,%v", cmd, ok, err)
	}
	if !slices.Equal(cmd[:3], []string{"agy", "--gemini-dir", ref.ConfigDir}) || !slices.Equal(cmd[len(cmd)-2:], []string{"--conversation", importTestID}) {
		t.Fatalf("restore argv = %v", cmd)
	}
	if err := os.Remove(ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := p.GetRestoreCommand(ctx, cfg); err == nil || ok {
		t.Fatalf("missing native history accepted: %v,%v", ok, err)
	}
	delete(cfg.Session.Metadata, ports.MetadataKeyAgentSessionID)
	if _, ok, err := p.GetRestoreCommand(ctx, cfg); err == nil || ok {
		t.Fatalf("missing imported ID accepted: %v,%v", ok, err)
	}
}
