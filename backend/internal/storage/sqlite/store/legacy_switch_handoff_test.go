package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func legacyHandoffStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "ao.db"))+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 VALUES('s',1,'codex','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
 ('other',2,'codex','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
 INSERT INTO agent_native_sessions(id,ao_session_id,harness,native_session_id,created_at,last_used_at)
 VALUES('n','s','codex','native',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
 ('other-session','other','codex','native',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
 ('other-harness','s','claude-code','native',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
 ('other-native','s','codex','different',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func insertLegacyHandoff(t *testing.T, db *sql.DB, id, at, path, session, harness, native, state string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO agent_switches(id,session_id,idempotency_key,request_fingerprint,from_harness,target_harness,
 source_generation_id,target_native_session_ref,state,requested_at,updated_at,final_handoff_path)
 VALUES(?,?,?,?,'aider',?,'source',?,?,?,?,?)`, id, session, id, "v1:"+strings.Repeat("0", 64), harness, native, state, at, at, path)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLegacySwitchHandoffPathComparesInstants(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, want string
	}{
		{"positive offset", "2026-10-21 09:00:00 +0530 +0530", "2026-10-21 04:00:00 +0000 UTC", "second"},
		{"negative offset", "2026-10-20 23:30:00 -0400 EDT", "2026-10-21 02:30:00 +0000 UTC", "first"},
		{"SQL offset and UTC", "2026-10-21 09:00:00+05:30", "2026-10-21 04:00:00", "second"},
		{"RFC3339 nanoseconds", "2026-10-21T09:00:00.123456789+05:30", "2026-10-21 03:30:00.123456788 +0000 UTC", "first"},
		{"instant tie uses ID", "2026-10-21 09:00:00 +0530 IST", "2026-10-21T03:30:00Z", "second"},
		{"valid instant beats invalid", "zzz-invalid", "2026-10-21 04:00:00", "second"},
		{"all invalid retains lexical first", "zzz-invalid", "aaa-invalid", "first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := legacyHandoffStore(t)
			insertLegacyHandoff(t, db, "a", tc.first, "first", "s", "codex", "n", "completed")
			insertLegacyHandoff(t, db, "z", tc.second, "second", "s", "codex", "n", "completed")
			got, err := s.LegacySwitchHandoffPath(context.Background(), "s", domain.HarnessCodex, "native")
			if err != nil || got != tc.want {
				t.Fatalf("path=%q want=%q err=%v", got, tc.want, err)
			}
		})
	}
}

func TestLegacySwitchHandoffPathRetainsScopeAndStateFilters(t *testing.T) {
	s, db := legacyHandoffStore(t)
	const later = "2026-10-22 04:00:00 +0000 UTC"
	insertLegacyHandoff(t, db, "wanted", "2026-10-21 04:00:00 +0000 UTC", "wanted", "s", "codex", "n", "completed")
	insertLegacyHandoff(t, db, "session", later, "wrong-session", "other", "codex", "other-session", "completed")
	insertLegacyHandoff(t, db, "harness", later, "wrong-harness", "s", "claude-code", "other-harness", "completed")
	insertLegacyHandoff(t, db, "native", later, "wrong-native", "s", "codex", "other-native", "completed")
	insertLegacyHandoff(t, db, "empty", later, "", "s", "codex", "n", "completed")
	insertLegacyHandoff(t, db, "unready", later, "unready", "s", "codex", "n", "preparing_handoff")
	got, err := s.LegacySwitchHandoffPath(context.Background(), "s", domain.HarnessCodex, "native")
	if err != nil || got != "wanted" {
		t.Fatalf("path=%q err=%v", got, err)
	}
	got, err = s.LegacySwitchHandoffPath(context.Background(), "s", domain.HarnessCodex, "missing")
	if err != nil || got != "" {
		t.Fatalf("missing native path=%q err=%v", got, err)
	}
}

func TestLegacySwitchHandoffPathEmpty(t *testing.T) {
	s, _ := legacyHandoffStore(t)
	got, err := s.LegacySwitchHandoffPath(context.Background(), "s", domain.HarnessCodex, "native")
	if err != nil || got != "" {
		t.Fatalf("empty path=%q err=%v", got, err)
	}
}
