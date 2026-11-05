package sessionimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func cursorFixture(t *testing.T, root, cwd string) string {
	t.Helper()
	const id = "22222222-2222-4222-8222-222222222222"
	path, err := cursorstore.SessionPath(root, cwd, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB); CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	field := func(n uint64, body []byte) []byte {
		out := binary.AppendUvarint(nil, n<<3|2)
		out = binary.AppendUvarint(out, uint64(len(body)))
		return append(out, body...)
	}
	put := func(body []byte) []byte {
		sum := sha256.Sum256(body)
		if _, err := db.Exec("INSERT INTO blobs VALUES (?,?)", hex.EncodeToString(sum[:]), body); err != nil {
			t.Fatal(err)
		}
		return sum[:]
	}
	user := put(field(1, []byte("synthetic native cursor prompt")))
	turn := put(field(1, field(1, user)))
	rootID := put(field(8, turn))
	meta, err := json.Marshal(map[string]any{"agentId": id, "latestRootBlobId": hex.EncodeToString(rootID), "createdAt": 1793600000000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta VALUES ('0',?)", hex.EncodeToString(meta)); err != nil {
		t.Fatal(err)
	}
	sidecar, err := json.Marshal(map[string]any{"schemaVersion": 1, "cwd": cwd, "hasConversation": true, "updatedAtMs": 1793600000000})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "meta.json"), sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCursorImportUsesTerminalWithoutChatCapability(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := cursorFixture(t, root, cwd)
	st := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic", Path: cwd}}}
	svc := New(st, "", func(domain.AgentHarness) bool { return false })
	svc.roots = map[domain.AgentHarness]string{domain.HarnessCursor: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.NativeTranscriptPath != path || rec.Metadata.AgentSessionID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("record = %#v", rec)
	}
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	launch := t.TempDir()
	target, err := cursorstore.Copy(context.Background(), root, path, launch)
	if err != nil {
		t.Fatal(err)
	}
	files, err := discoverCursor(context.Background(), root)
	if err != nil || len(files) != 2 {
		t.Fatalf("native buckets = %#v, %v", files, err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: launch, Env: map[string]string{"CURSOR_DATA_DIR": root}, NativeSessionID: rec.Metadata.AgentSessionID, ImportConfigDir: root, RestoreMode: ports.WorkspaceRestoreImported}
	adapter := cursor.New()
	if err := adapter.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	// The original still exists in another bucket. A restored import must
	// validate its AO copy rather than accepting that unrelated lookup.
	cfg.RestoreMode = ports.WorkspaceRestoreNative
	if err := adapter.PrepareRuntimeLaunch(context.Background(), cfg); err == nil {
		t.Fatal("accepted a missing copied native store")
	}
	cfg.RestoreMode = ports.WorkspaceRestoreFresh
	if err := adapter.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatalf("changed fresh-launch behavior: %v", err)
	}
}

// ACP's session/load reads only acp-sessions/<id>, so a Chat import is copied
// there and the terminal CLI's chat folder is left to the original.
func TestCursorImportPreparesACPCopyForChat(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := cursorFixture(t, root, cwd)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic", Path: cwd}}}
	svc := New(st, "", func(domain.AgentHarness) bool { return true })
	svc.roots = map[domain.AgentHarness]string{domain.HarnessCursor: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Mode != domain.SessionModeChat {
		t.Fatalf("mode = %q, want chat", rec.Mode)
	}
	launch := t.TempDir()
	ref, err := cursor.New().PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{
		NativeSessionID:   rec.Metadata.AgentSessionID,
		ConfigDir:         root,
		TranscriptPath:    path,
		SourceCWD:         cwd,
		LaunchCWD:         launch,
		Chat:              true,
		SourceFingerprint: rec.Metadata.ImportSource.Fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := cursorstore.ACPSessionPath(root, rec.Metadata.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.NativeSessionID != rec.Metadata.AgentSessionID || ref.TranscriptPath != want {
		t.Fatalf("ref = %#v, want ACP copy %s", ref, want)
	}
	copied, err := cursorstore.Read(context.Background(), root, want)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(launch)
	if err != nil {
		t.Fatal(err)
	}
	if copied.ID != rec.Metadata.AgentSessionID || copied.CWD != resolved || len(copied.Messages) == 0 {
		t.Fatalf("ACP copy = %#v", copied)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("source changed: %v", err)
	}
	// A retry reuses the published copy instead of making another.
	again, err := cursor.New().PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{
		NativeSessionID: rec.Metadata.AgentSessionID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd,
		LaunchCWD: launch, Chat: true, SourceFingerprint: rec.Metadata.ImportSource.Fingerprint,
	})
	if err != nil || again.TranscriptPath != want {
		t.Fatalf("retry = %#v, %v", again, err)
	}
	// The ACP copy is not a terminal chat, so it is never offered for import.
	files, err := discoverCursor(context.Background(), root)
	if err != nil || len(files) != 1 {
		t.Fatalf("native buckets = %#v, %v", files, err)
	}
}

func TestCursorScanInvalidatesWALAndSidecarChanges(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := cursorFixture(t, root, cwd)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic-wal", Path: cwd}}}
	svc := New(st, "", func(domain.AgentHarness) bool { return true })
	svc.roots = map[domain.AgentHarness]string{domain.HarnessCursor: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("initial scan = %#v, %v", preview, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err := db.QueryRow("SELECT value FROM meta WHERE key='0'").Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	body, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(body, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata["name"] = "changed only in WAL"
	body, err = json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE meta SET value=? WHERE key='0'", hex.EncodeToString(body)); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(path); err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		t.Fatalf("test changed the main database stat: %v", err)
	}
	preview, err = svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Title != "changed only in WAL" {
		t.Fatalf("WAL scan = %#v, %v", preview, err)
	}
	otherCWD := t.TempDir()
	sidecar := filepath.Join(filepath.Dir(path), "meta.json")
	body, err = json.Marshal(map[string]any{"cwd": otherCWD, "updatedAtMs": 1793600000000})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, body, 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1793600010, 0)
	if err := os.Chtimes(sidecar, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	preview, err = svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 0 {
		t.Fatalf("sidecar moved outside registered project: %#v, %v", preview, err)
	}
}
