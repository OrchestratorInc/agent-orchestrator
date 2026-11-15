package minimaxcode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
	t.Run("compaction summary", func(t *testing.T) {
		p, w, id := historyFixture(t)
		messages := `{"message_id":"m2","turn_id":"t1","message":{"role":"compactionSummary","summary":"Earlier conversation summary"}}` + "\n"
		if err := os.WriteFile(filepath.Join(p, "v2", "sessions", "exact", "messages.jsonl"), []byte(messages), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateNativeHistory(context.Background(), p, id, w); err != nil {
			t.Fatalf("native compaction summary refused: %v", err)
		}
	})
	t.Run("oversized manifest", func(t *testing.T) {
		p, w, id := historyFixture(t)
		manifest := filepath.Join(p, "v2", "sessions", "exact", "manifest.json")
		if err := os.WriteFile(manifest, make([]byte, maxHistoryManifestBytes+1), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateNativeHistory(context.Background(), p, id, w); err == nil {
			t.Fatal("accepted oversized native history manifest")
		}
	})
	t.Run("manifest symlink", func(t *testing.T) {
		p, w, id := historyFixture(t)
		manifest := filepath.Join(p, "v2", "sessions", "exact", "manifest.json")
		outside := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(outside, []byte(`{"sessionId":"`+id+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(manifest); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, manifest); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := validateNativeHistory(context.Background(), p, id, w); err == nil {
			t.Fatal("accepted symlinked native history manifest")
		}
	})
	// The history directory must stay below v2/sessions even when the recorded
	// relative path only escapes after cleaning. Each escape target holds a
	// complete, valid history so only the location check can refuse it.
	for _, relative := range []string{"exact/../..", ".", "exact/.."} {
		t.Run("escaping location "+relative, func(t *testing.T) {
			p, w, id := historyFixture(t)
			target := filepath.Clean(filepath.Join(p, "v2", "sessions", relative))
			for _, name := range []string{"manifest.json", "messages.jsonl"} {
				body, err := os.ReadFile(filepath.Join(p, "v2", "sessions", "exact", name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, name), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			db, err := sql.Open("sqlite", filepath.Join(p, "v2", "sqlite", "runtime-state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`UPDATE local_runtime_sessions SET history_relative_dir=? WHERE session_id=?`, relative, id); err != nil {
				t.Fatal(err)
			}
			if err := validateNativeHistory(context.Background(), p, id, w); err == nil {
				t.Fatalf("accepted history location %q outside the native sessions directory", relative)
			}
		})
	}
	t.Run("missing identity", func(t *testing.T) {
		p, w, _ := historyFixture(t)
		if err := validateNativeHistory(context.Background(), p, "mvs_00000000000000000000000000000000", w); err == nil {
			t.Fatal("accepted wrong identity")
		}
	})
}

func TestHistoryRejectsValidJSONWithInvalidNativeShape(t *testing.T) {
	for _, data := range []string{
		`{}`,
		`[]`,
		`null`,
		`{"message":{"role":"user"}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"alien","content":[{"type":"text"}]}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"compactionSummary"}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"compactionSummary","summary":""}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"compactionSummary","summary":"ok","content":null}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"compactionSummary","summary":"ok","content":[]}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"custom","content":"no custom type","display":false}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"custom","customType":"note","content":null}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"custom","customType":"note","content":[{"text":"untyped"}]}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"bashExecution","command":"ls"}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"branchSummary","summary":"left branch"}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"user","content":null}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"user","content":[]}}`,
		`{"message_id":"x","turn_id":"t","message":{"role":"assistant","content":"plain text"}}`,
	} {
		if validHistoryMessage([]byte(data)) {
			t.Fatalf("accepted invalid native envelope: %s", data)
		}
	}
}

// MiniMax Code 0.6.5's canonical history also stores custom records (among
// them the plugin_hook_context that AO's standing instructions become), shell
// executions and branch summaries. Restore must accept them.
func TestHistoryAcceptsNativeRecordRoles(t *testing.T) {
	records := []string{
		`{"message_id":"m1","turn_id":"t1","message":{"role":"user","content":"plain prompt","timestamp":1}}`,
		`{"message_id":"m2","turn_id":"t1","message":{"role":"custom","customType":"plugin_hook_context","content":"<plugin-hook-context>\nAO standing instructions\n</plugin-hook-context>","display":false,"timestamp":2}}`,
		`{"message_id":"m3","turn_id":"t1","message":{"role":"custom","customType":"note","content":[{"type":"text","text":"portable"}],"display":true,"timestamp":3}}`,
		`{"message_id":"m4","turn_id":"t1","message":{"role":"bashExecution","command":"ls","output":"README.md\n","exitCode":0,"cancelled":false,"truncated":false,"timestamp":4}}`,
		`{"message_id":"m5","turn_id":"t2","message":{"role":"branchSummary","summary":"Earlier branch","fromId":"m4","timestamp":5}}`,
		`{"message_id":"m6","turn_id":"t2","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"timestamp":6}}`,
	}
	for _, data := range records {
		if !validHistoryMessage([]byte(data)) {
			t.Fatalf("rejected native record: %s", data)
		}
	}
	p, w, id := historyFixture(t)
	if err := os.WriteFile(filepath.Join(p, "v2", "sessions", "exact", "messages.jsonl"), []byte(strings.Join(records, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeHistory(context.Background(), p, id, w); err != nil {
		t.Fatalf("native history with hook context and native records refused restore: %v", err)
	}
}

func TestHistoryAcceptsNativeCompactionSummary(t *testing.T) {
	data := `{"message_id":"m2","turn_id":"t1","message":{"role":"compactionSummary","summary":"Earlier conversation summary"}}`
	if !validHistoryMessage([]byte(data)) {
		t.Fatalf("rejected native compaction summary: %s", data)
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
