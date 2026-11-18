package cursorstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	if got.ID != testID || got.CWD != cwd || len(got.Messages) != 2 {
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
	target, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), root, target)
	wantCWD, pathErr := filepath.EvalSymlinks(launch)
	if err != nil || pathErr != nil || got.ID != testID || got.CWD != wantCWD || len(got.Messages) != 2 {
		t.Fatalf("copy = %#v, %v", got, err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("source changed: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
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
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, ""); err != nil {
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
	target, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, link, "")
	want, pathErr := SessionPath(root, launch, testID)
	if err != nil || pathErr != nil || target != want {
		t.Fatalf("target = %q, %v; want %q, %v", target, err, want, pathErr)
	}
}

func TestCopyResolvesProviderProfileAlias(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	alias := filepath.Join(t.TempDir(), "profile-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	target, err := CopySelectedIn(context.Background(), LayoutTerminal, alias, source, launch, "")
	want, pathErr := SessionPath(root, launch, testID)
	if err != nil || pathErr != nil || target != want {
		t.Fatalf("profile alias target = %q, %v; want %q, %v", target, err, want, pathErr)
	}
	if _, err := Read(context.Background(), alias, target); err != nil {
		t.Fatal(err)
	}
}

func TestCopyRetriesAfterInterruptedPublication(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	failure := errors.New("synthetic publication failure")
	_, err := copyIn(context.Background(), LayoutTerminal, root, source, launch, "", func(stage, target string) error {
		// At this boundary the complete native directory is ready, but no
		// target exists yet. An interruption cannot leave a partial target.
		if _, err := Read(context.Background(), root, filepath.Join(stage, "store.db")); err == nil {
			t.Fatal("staging directory must not masquerade as a native UUID")
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("target already reserved: %v", err)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("publication error = %v", err)
	}
	target, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, "")
	if err != nil {
		t.Fatal(err)
	}
	if archive, err := Read(context.Background(), root, target); err != nil || archive.ID != testID {
		t.Fatalf("retry = %#v, %v", archive, err)
	}
}

func TestSelectedCopyRefusesChangedNativeInputs(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	expected, err := Fingerprint(context.Background(), root, source)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(filepath.Dir(source), "meta.json")
	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.ReplaceAll(body, []byte("Synthetic chat"), []byte("Different chat"))
	if len(changed) != len(body) || bytes.Equal(changed, body) {
		t.Fatal("fixture must change content without changing size")
	}
	if err := os.WriteFile(sidecar, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sidecar, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, expected); err == nil {
		t.Fatal("copied changed native source evidence")
	}
	target, err := SessionPath(root, launch, testID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published a stale native copy: %v", err)
	}
	fresh, err := Fingerprint(context.Background(), root, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, fresh); err != nil {
		t.Fatal(err)
	}
}

func TestSelectedCopyRefusesWALShiftBeforePublication(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	expected, err := Fingerprint(context.Background(), root, source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = copyIn(context.Background(), LayoutTerminal, root, source, launch, expected, func(_, _ string) error {
		_, err := db.Exec("INSERT INTO blobs (id,data) VALUES ('concurrent-turn',?)", []byte("changed during snapshot"))
		return err
	})
	if err == nil {
		t.Fatal("published a snapshot after its WAL changed")
	}
	target, pathErr := SessionPath(root, launch, testID)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale target exists: %v", err)
	}
	fresh, err := Fingerprint(context.Background(), root, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, fresh); err != nil {
		t.Fatal(err)
	}
}

func TestSelectedCopyRetryRequiresMatchingProvenance(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	expected, err := Fingerprint(context.Background(), root, source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, expected)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO blobs (id,data) VALUES ('later-ao-turn',?)", []byte("keep this")); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.RemoveAll(filepath.Dir(source)); err != nil {
		t.Fatal(err)
	}
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, expected); err != nil {
		t.Fatalf("proven copy cannot be retried after source removal: %v", err)
	}
	db, err = sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM blobs WHERE id='later-ao-turn'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("later native state lost: %d, %v", count, err)
	}
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, "different-selection"); err == nil {
		t.Fatal("reused a copy from another selection")
	}
	if err := os.Remove(filepath.Join(filepath.Dir(target), preparationFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, expected); err == nil {
		t.Fatal("reused an unproven native target")
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
	if _, err := CopySelectedIn(context.Background(), LayoutTerminal, root, source, launch, ""); err == nil {
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
	bodies := [][]byte{{0}, {0x0a, 0x7f}, {0x09, 1}, {0x0d, 1}, {0x0f}, {0x08, 0x80}}
	bodies = append(bodies,
		binary.AppendUvarint([]byte{0x0a}, uint64(maxBlob)+1),
		binary.AppendUvarint([]byte{0x0a}, ^uint64(0)),
	)
	for _, body := range bodies {
		if _, err := decode(body); err == nil {
			t.Fatalf("accepted malformed protobuf %x", body)
		}
	}
}
