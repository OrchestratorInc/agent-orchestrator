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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

func TestCursorImportUsesTerminalDespiteChatCapability(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := cursorFixture(t, root, cwd)
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
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.NativeTranscriptPath != path || rec.Metadata.AgentSessionID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("record = %#v", rec)
	}
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	launch := t.TempDir()
	if _, err := cursorstore.Copy(context.Background(), root, path, launch); err != nil {
		t.Fatal(err)
	}
	files, err := discoverCursor(context.Background(), root)
	if err != nil || len(files) != 2 {
		t.Fatalf("native buckets = %#v, %v", files, err)
	}
}
