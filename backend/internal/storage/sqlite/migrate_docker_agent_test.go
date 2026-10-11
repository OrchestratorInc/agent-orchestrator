package sqlite

import (
	"fmt"
	"strings"
	"testing"
)

// TestMigration0205AllowsDockerAgentAndReversesBothHistoricalSchemas mirrors the
// fx and DeepSeek coverage: the new harness must be insertable on a current and a
// legacy-qm schema, an unknown harness must still be rejected, and the down
// migration must restore the exact prior constraint.
func TestMigration0205AllowsDockerAgentAndReversesBothHistoricalSchemas(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 194)
			if legacyQM {
				// The retained legacy 'qm' fixture harness sits before
				// 'codewhale' in every variant 0205 rewrites, so anchor
				// there to produce a schema the migration still recognizes.
				mustExec(t, db, `PRAGMA writable_schema = ON`)
				mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''openhands'', ''codewhale''', '''openhands'', ''qm'', ''codewhale''') WHERE type = 'table' AND name = 'sessions'`)
				mustExec(t, db, `PRAGMA writable_schema = RESET`)
			}
			upTo(t, db, 194)
			var before string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('docker-project', '/tmp/docker-project', CURRENT_TIMESTAMP)`)
			insert := `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES (?, 'docker-project', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
			mustExec(t, db, insert, "existing-omp", 1, "omp")
			if legacyQM {
				mustExec(t, db, insert, "existing-qm", 2, "qm")
			}
			upTo(t, db, 205)
			if _, err := db.Exec(insert, "docker-session", 3, "docker-agent"); err != nil {
				t.Fatalf("insert docker-agent session after migration: %v", err)
			}
			var version int
			if err := db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&version); err != nil || version != 205 {
				t.Fatalf("migration version = %d, err = %v; want 205", version, err)
			}
			if _, err := db.Exec(insert, "unknown", 4, "unknown-agent"); err == nil {
				t.Fatal("unknown harness bypassed the CHECK constraint")
			}
			// Clear the new harness value before downgrading to the older contract.
			mustExec(t, db, `UPDATE sessions SET harness = '' WHERE harness = 'docker-agent'`)
			downTo(t, db, 194)
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("down migration did not restore original schema: %v", err)
			}
			if _, err := db.Exec(insert, "docker-after-down", 5, "docker-agent"); err == nil || !strings.Contains(err.Error(), "CHECK") {
				t.Fatalf("docker-agent insertion after downgrade = %v; want CHECK failure", err)
			}
			var integrity string
			if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
			}
		})
	}
}

// A burned earlier migration must retain its exact repair recognizer until the
// older list is repaired. A burned Docker version must also be reconciled.
func TestDockerAgentRepairsBurnedHistoryBeforeAppending(t *testing.T) {
	for _, base := range []int64{26, 53, 54, 82, 95, 155, 163, 164, 165, 166, 167, 193, 194} {
		t.Run(fmt.Sprint(base), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, base)
			for _, v := range []int64{165, 205} {
				if v > base {
					mustExec(t, db, `INSERT INTO goose_db_version (version_id,is_applied) VALUES (?,1)`, v)
				}
			}
			if err := migrate(db); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			for _, h := range []string{"muse", "kimchi", "prime-agent", "omp", "gemini", "unreal-agent", "fx", "mimo-code", "deepseek-harness", "opencode-v2", "openhands", "codewhale", "command-code", "docker-agent"} {
				if !strings.Contains(before, "'"+h+"'") {
					t.Fatalf("base %d lost %s: %s", base, h, before)
				}
			}
			if err := reconcileHarnessConstraint(db); err != nil {
				t.Fatal(err)
			}
			var after string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&after); err != nil || after != before {
				t.Fatalf("non-idempotent repair: %v", err)
			}
		})
	}
}
