package cursorstore

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A chat packed on one machine resumes from another directory on another.
func TestPackAndUnpackMoveAChat(t *testing.T) {
	source := fixture(t, t.TempDir(), t.TempDir())
	var packed bytes.Buffer
	if _, err := Pack(context.Background(), filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source)))), source, &packed); err != nil {
		t.Fatal(err)
	}
	root, cwd := t.TempDir(), t.TempDir()
	id, err := Unpack(context.Background(), root, &packed, cwd)
	if err != nil || id != testID {
		t.Fatalf("Unpack = %q, %v", id, err)
	}
	assertResumable(t, root, cwd, "Synthetic chat")
}

// AO Cloud's worker packs the store's files as they are, its WAL included.
func TestUnpackAppliesTheWAL(t *testing.T) {
	source := fixture(t, t.TempDir(), t.TempDir())
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	var metaHex string
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow("SELECT value FROM meta WHERE key='0'").Scan(&metaHex); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(metaHex)
	var meta map[string]any
	_ = json.Unmarshal(raw, &meta)
	meta["name"] = "Renamed in Cloud"
	raw, _ = json.Marshal(meta)
	if _, err := db.Exec("UPDATE meta SET value=? WHERE key='0'", hex.EncodeToString(raw)); err != nil {
		t.Fatal(err)
	}
	var packed bytes.Buffer
	tw := tar.NewWriter(&packed)
	for _, name := range []string{"store.db", "store.db-wal", "meta.json"} {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(source), name))
		if err != nil {
			t.Fatal(err)
		}
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	root, cwd := t.TempDir(), t.TempDir()
	if _, err := Unpack(context.Background(), root, &packed, cwd); err != nil {
		t.Fatal(err)
	}
	assertResumable(t, root, cwd, "Renamed in Cloud")
}

func assertResumable(t *testing.T, root, cwd, title string) {
	t.Helper()
	target, err := SessionPath(root, cwd, testID)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := Read(context.Background(), root, target)
	resolved, _ := filepath.EvalSymlinks(cwd)
	if err != nil || archive.ID != testID || archive.CWD != resolved || archive.Title != title || len(archive.Messages) == 0 {
		t.Fatalf("placed chat = %+v, %v", archive, err)
	}
}
