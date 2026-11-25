package agy

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
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
	uri := importFileURI(cwd)
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
		{"trigger", "CREATE TRIGGER unexpected AFTER UPDATE ON trajectory_metadata_blob BEGIN DELETE FROM gen_metadata; END", false},
		{"view", "CREATE VIEW unexpected AS SELECT idx FROM steps", false},
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
	if _, err := p.PrepareImportedSession(ctx, r); err == nil || !strings.Contains(err.Error(), "terminate this import") {
		t.Fatalf("changed selection recovery = %v", err)
	}
}

func TestPrivateImportReopenReadsCommittedWAL(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t)
	fingerprint, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	launch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := p.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: launch, DataDir: t.TempDir(), SourceFingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO steps(idx,step_type,status,has_subtrajectory,step_payload,step_format) VALUES(2,14,3,0,?,0)", importField(19, importField(2, []byte("committed after import")))); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(ref.TranscriptPath + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("fixture needs a committed WAL: %v", err)
	}
	if available, err := p.ProbeNativeSession(ctx, ref); err != nil || available != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("private WAL probe = %v,%v", available, err)
	}
	if err := p.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{ImportConfigDir: ref.ConfigDir, NativeSessionID: ref.NativeSessionID, WorkspacePath: launch, Env: map[string]string{"GEMINI_HOME": ref.ConfigDir}}); err != nil {
		t.Fatal(err)
	}
	archive, err := readImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath, false)
	if err != nil || len(archive.Messages) != 3 || archive.Messages[2].Text != "committed after import" {
		t.Fatalf("WAL history = %+v,%v", archive, err)
	}
	if after, err := ImportFingerprint(ctx, root, path); err != nil || after != fingerprint {
		t.Fatalf("source changed: %v", err)
	}
}

func TestImportFileURI(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"C:/ao/history.db", "file:///C:/ao/history.db"},
		{filepath.FromSlash("C:/ao/history.db"), "file:///C:/ao/history.db"},
		{"/tmp/ao #space/history.db", "file:///tmp/ao%20%23space/history.db"},
	} {
		if got := importFileURI(tc.path); got != tc.want {
			t.Errorf("importFileURI(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestPreparedImportEscapesSQLiteURI(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t)
	fingerprint, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	dirName := "ao?work# space"
	if filepath.Separator == '\\' {
		dirName = "ao# space"
	}
	ref, err := p.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: filepath.Join(t.TempDir(), dirName), Chat: true, SourceFingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath); err != nil {
		t.Fatal(err)
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

func TestImportedReopenSurvivesCLIUpgradeButPreparationStaysPinned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI uses a POSIX shell")
	}
	ctx := context.Background()
	root, path, cwd := importFixture(t)
	fingerprint, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "agy")
	writeVersion := func(version string) {
		t.Helper()
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeVersion(supportedImportVersion)
	p := &Plugin{resolvedBinary: binary}
	launch := t.TempDir()
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: launch, DataDir: t.TempDir(), SourceFingerprint: fingerprint}
	terminal, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Chat, r.DataDir = true, t.TempDir()
	chat, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	writeVersion("1.5.14")
	if version, err := importBinaryVersion(ctx, binary); err != nil || version != "1.5.14" {
		t.Fatalf("upgraded fake binary = %q,%v", version, err)
	}
	restore := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: launch, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: terminal.NativeSessionID, ports.MetadataKeyImportSourceNativeID: importTestID, ports.MetadataKeyImportResumeNativeID: terminal.NativeSessionID, ports.MetadataKeyImportConfigDir: terminal.ConfigDir}}}
	if _, ok, err := p.GetRestoreCommand(ctx, restore); err != nil || !ok {
		t.Fatalf("restore after CLI update = %v,%v", ok, err)
	}
	hook := ports.WorkspaceHookConfig{ImportConfigDir: terminal.ConfigDir, NativeSessionID: terminal.NativeSessionID, WorkspacePath: launch, Env: map[string]string{"GEMINI_HOME": terminal.ConfigDir}}
	if err := p.PrepareRuntimeLaunch(ctx, hook); err != nil {
		t.Fatalf("launch after CLI update = %v", err)
	}
	if err := ValidateImportedACP(ctx, chat.ConfigDir, chat.NativeSessionID, launch, binary); err != nil {
		t.Fatalf("ACP after CLI update = %v", err)
	}
	r.DataDir = t.TempDir()
	if _, err := p.PrepareImportedSession(ctx, r); err == nil || !strings.Contains(err.Error(), "version 1.5.13") {
		t.Fatalf("new preparation after CLI update = %v", err)
	}
	for _, ref := range []ports.NativeSessionRef{terminal, chat} {
		if err := os.Remove(ref.TranscriptPath); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := p.GetRestoreCommand(ctx, restore); err == nil || ok {
		t.Fatalf("missing terminal history after CLI update = %v,%v", ok, err)
	}
	if err := p.PrepareRuntimeLaunch(ctx, hook); err == nil {
		t.Fatal("missing launch history after CLI update accepted")
	}
	if err := ValidateImportedACP(ctx, chat.ConfigDir, chat.NativeSessionID, launch, binary); err == nil {
		t.Fatal("missing ACP history after CLI update accepted")
	}
}

// Agy keeps writing the private DB after import and may migrate it after an
// update. Reopen checks identity and workspace only, so later native formats,
// subtrajectories, links and tables never make the import unopenable.
func TestPrivateImportReopensAfterNativeFormatChanges(t *testing.T) {
	ctx := context.Background()
	root, path, cwd := importFixture(t)
	fingerprint, err := ImportFingerprint(ctx, root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	launch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := p.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: launch, DataDir: t.TempDir(), Chat: true, SourceFingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO steps(idx,step_type,status,has_subtrajectory,step_payload,step_format) VALUES(90,15,3,1,x'00',2)",
		"INSERT INTO parent_references DEFAULT VALUES",
		"CREATE TABLE later_native_table(id INTEGER PRIMARY KEY)",
		"PRAGMA user_version=2",
	} {
		if _, err := db.Exec(q); err != nil {
			_ = db.Close()
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readImportArchive(ctx, ref.ConfigDir, ref.TranscriptPath, false); err == nil {
		t.Fatal("fixture no longer exceeds the import-time audit")
	}
	if err := ValidateImportedACP(ctx, ref.ConfigDir, ref.NativeSessionID, launch, "agy"); err != nil {
		t.Fatalf("ACP reopen after native format change: %v", err)
	}
	if found, ok, err := p.LocateTranscript(ctx, ref); err != nil || !ok || found != ref.TranscriptPath {
		t.Fatalf("terminal lookup after native format change = %q, %v, %v", found, ok, err)
	}
	if again, err := p.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: launch, DataDir: filepath.Dir(filepath.Dir(filepath.Dir(ref.ConfigDir))), Chat: true, SourceFingerprint: fingerprint}); err != nil || again != ref {
		t.Fatalf("prepared recovery after native format change = %+v, %v", again, err)
	}
}
