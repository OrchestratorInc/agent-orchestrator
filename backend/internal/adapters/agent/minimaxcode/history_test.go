package minimaxcode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func historyFixture(t *testing.T) (string, string, string) {
	t.Helper()
	profile := t.TempDir()
	workspace := t.TempDir()
	id := "mvs_01234567890123456789012345678901"
	dbDir := filepath.Join(profile, "v2", "sqlite")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dbDir, "runtime-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE local_runtime_sessions(session_id TEXT,workspace_dir TEXT,history_relative_dir TEXT,archived INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO local_runtime_sessions VALUES(?,?,?,0)`, id, workspace, "exact"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(profile, "v2", "sessions", "exact")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"manifest.json": `{"sessionId":"` + id + `"}`, "messages.jsonl": `{"message_id":"m1","turn_id":"t1","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return profile, workspace, id
}
func TestNativeHistoryPreflight(t *testing.T) {
	t.Run("exact", func(t *testing.T) {
		p, w, id := historyFixture(t)
		if err := validateNativeHistory(context.Background(), p, id, w); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("workspace mismatch", func(t *testing.T) {
		p, _, id := historyFixture(t)
		if err := validateNativeHistory(context.Background(), p, id, t.TempDir()); err == nil {
			t.Fatal("accepted different workspace")
		}
	})
	t.Run("corrupt messages", func(t *testing.T) {
		p, w, id := historyFixture(t)
		if err := os.WriteFile(filepath.Join(p, "v2", "sessions", "exact", "messages.jsonl"), []byte("invalid\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateNativeHistory(context.Background(), p, id, w); err == nil {
			t.Fatal("accepted corrupt history")
		}
	})
	t.Run("missing identity", func(t *testing.T) {
		p, w, _ := historyFixture(t)
		if err := validateNativeHistory(context.Background(), p, "mvs_00000000000000000000000000000000", w); err == nil {
			t.Fatal("accepted wrong identity")
		}
	})
}

func TestHistoryRejectsValidJSONWithInvalidNativeShape(t *testing.T) {
	for _, data := range []string{`{}`, `[]`, `null`, `{"message":{"role":"user"}}`, `{"message_id":"x","turn_id":"t","message":{"role":"alien","content":[{"type":"text"}]}}`} {
		if validHistoryMessage([]byte(data)) {
			t.Fatalf("accepted invalid native envelope: %s", data)
		}
	}
}

func TestHistoryTrustedRootAliasAndNestedSymlink(t *testing.T) {
	profile, workspace, id := historyFixture(t)
	alias := filepath.Join(t.TempDir(), "profile")
	if err := os.Symlink(profile, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := validateNativeHistory(context.Background(), alias, id, workspace); err != nil {
		t.Fatalf("trusted profile alias refused: %v", err)
	}
	original := filepath.Join(profile, "v2", "sessions", "exact")
	moved := filepath.Join(profile, "v2", "saved-exact")
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, original); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeHistory(context.Background(), profile, id, workspace); err == nil {
		t.Fatal("nested history symlink accepted")
	}
}
