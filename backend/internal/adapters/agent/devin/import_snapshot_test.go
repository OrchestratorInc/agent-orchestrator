package devin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImportSnapshotReadsCommittedWALWithoutChangingSource(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cli", "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE probe(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	initialWAL, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO probe VALUES ('committed native marker')"); err != nil {
		t.Fatal(err)
	}
	before := map[string][32]byte{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		before[suffix] = sha256.Sum256(b)
	}
	snapshot, err := readSourceStore(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var value string
	if err := snapshot.db.QueryRow("SELECT value FROM probe").Scan(&value); err != nil || value != "committed native marker" {
		t.Fatalf("committed WAL was lost: %q, %v", value, err)
	}
	if snapshot.fingerprint == "" {
		t.Fatal("missing immutable selection evidence")
	}
	for suffix, expected := range before {
		b, err := os.ReadFile(path + suffix)
		if err != nil || sha256.Sum256(b) != expected {
			t.Fatalf("source %s changed: %v", suffix, err)
		}
	}
	// A torn last frame must not make an uncommitted/torn turn visible.
	wal, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := committedImportPages(context.Background(), append([]byte(nil), main...), initialWAL)
	if err != nil {
		t.Fatal(err)
	}
	torn, err := committedImportPages(context.Background(), append([]byte(nil), main...), wal[:len(wal)-1])
	if err != nil || !bytes.Equal(initial, torn) {
		t.Fatalf("torn WAL published an incomplete turn: %v", err)
	}
	corrupt := append([]byte(nil), wal...)
	corrupt[24] ^= 1
	if _, err := committedImportPages(context.Background(), main, corrupt); err == nil {
		t.Fatal("accepted corrupt WAL header")
	}
}

func TestImportSnapshotRefusesSymlinkedSourceFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cli", "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE probe(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	direct, err := readSourceStore(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	direct.Close()
	target := filepath.Join(root, "cli", "valid.db")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := readSourceStore(context.Background(), root); err == nil {
		t.Fatal("accepted a symlink under the selected source root")
	}
}

func TestImportSnapshotPreservesAbsentStoreDirectory(t *testing.T) {
	if _, err := readSourceStore(context.Background(), t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing cli directory: %v", err)
	}
}
