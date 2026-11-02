package cursorstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const testID = "11111111-1111-4111-8111-111111111111"

func field(number uint64, value []byte) []byte {
	out := binary.AppendUvarint(nil, number<<3|2)
	out = binary.AppendUvarint(out, uint64(len(value)))
	return append(out, value...)
}

func fixture(t *testing.T, root, cwd string) string {
	t.Helper()
	path, err := SessionPath(root, cwd, testID)
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
	put := func(body []byte) []byte {
		sum := sha256.Sum256(body)
		if _, err := db.Exec("INSERT OR REPLACE INTO blobs (id,data) VALUES (?,?)", hex.EncodeToString(sum[:]), body); err != nil {
			t.Fatal(err)
		}
		return sum[:]
	}
	user := put(field(1, []byte("explain the synthetic parser")))
	assistant := put(field(1, field(1, []byte("synthetic answer"))))
	tool := put(field(2, []byte("tool payload is not display text")))
	thinking := put(field(3, field(1, []byte("private thought"))))
	turnBody := append(field(1, user), field(2, thinking)...)
	turnBody = append(turnBody, field(2, tool)...)
	turnBody = append(turnBody, field(2, assistant)...)
	turn := put(field(1, turnBody))
	rootBody := append(field(9, []byte("file:///source/workspace")), field(8, turn)...)
	rootID := put(rootBody)
	// An abandoned message is present in the store but unreachable from the root.
	put(field(1, []byte("abandoned prompt")))
	metadata, err := json.Marshal(map[string]any{"agentId": testID, "latestRootBlobId": hex.EncodeToString(rootID), "name": "Synthetic chat", "createdAt": 1793600000000, "lastUsedModel": "synthetic-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta (key,value) VALUES ('0',?)", hex.EncodeToString(metadata)); err != nil {
		t.Fatal(err)
	}
	sidecar, err := json.Marshal(map[string]any{"schemaVersion": 1, "cwd": cwd, "title": "Synthetic chat", "hasConversation": true, "updatedAtMs": 1793600000001})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "meta.json"), sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadFollowsNativeActiveGraph(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := fixture(t, root, cwd)
	got, err := Read(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != testID || got.CWD != cwd || got.Model != "synthetic-model" || len(got.Messages) != 2 {
		t.Fatalf("archive = %#v", got)
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Text != "explain the synthetic parser" || got.Messages[1].Role != "assistant" || got.Messages[1].Text != "synthetic answer" {
		t.Fatalf("messages = %#v", got.Messages)
	}
}

func TestCopyKeepsSourceAndExistingNativeCopy(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := Copy(context.Background(), root, source, launch)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), root, target)
	if err != nil || got.ID != testID || got.CWD != launch || len(got.Messages) != 2 {
		t.Fatalf("copy = %#v, %v", got, err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("source changed: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("copy permissions = %v, %v", info, err)
	}
	// A later open must keep AO's already modified copy.
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO blobs (id,data) VALUES ('ao-later-turn',?)", []byte("later native state")); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Copy(context.Background(), root, source, launch); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM blobs WHERE id='ao-later-turn'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("later state = %d, %v", count, err)
	}
}

func TestCopyUsesResolvedLaunchDirectory(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	link := filepath.Join(t.TempDir(), "launch-link")
	if err := os.Symlink(launch, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	target, err := Copy(context.Background(), root, source, link)
	want, pathErr := SessionPath(root, launch, testID)
	if err != nil || pathErr != nil || target != want {
		t.Fatalf("target = %q, %v; want %q, %v", target, err, want, pathErr)
	}
}

func TestReadRejectsEscapingAndIncompleteState(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := fixture(t, root, cwd)
	outside := fixture(t, t.TempDir(), cwd)
	if _, err := Read(context.Background(), root, outside); err == nil {
		t.Fatal("read outside provider home")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM blobs"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Read(context.Background(), root, path); err == nil {
		t.Fatal("missing native graph accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, root, path); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestCopyRefusesExistingIncompleteTarget(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	target, err := SessionPath(root, launch, testID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	const sentinel = "unrelated native state"
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(context.Background(), root, source, launch); err == nil {
		t.Fatal("overwrote an incomplete target")
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != sentinel {
		t.Fatalf("target changed: %v", err)
	}
}

func TestReadRejectsSymlinkOutsideProvider(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	outside := fixture(t, t.TempDir(), cwd)
	path, err := SessionPath(root, cwd, testID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Read(context.Background(), root, path); err == nil {
		t.Fatal("read native state through escaping symlink")
	}
}

func TestDecodeRejectsMalformedNativeGraph(t *testing.T) {
	for _, body := range [][]byte{{0}, {0x0a, 0x7f}, {0x09, 1}, {0x0d, 1}, {0x0f}, {0x08, 0x80}} {
		if _, err := decode(body); err == nil {
			t.Fatalf("accepted malformed protobuf %x", body)
		}
	}
}
