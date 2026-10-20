package sqlite

import (
	"fmt"
	"math"
	"testing"
	"testing/fstest"
)

func TestMigrateRepairsAgentInstallJobsFromLegacyVersions(t *testing.T) {
	for _, legacyVersion := range []int64{119, 120} {
		t.Run(fmt.Sprintf("version_%d", legacyVersion), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 118)

			contents, err := migrationsFS.ReadFile("migrations/0123_agent_install_jobs.sql")
			if err != nil {
				t.Fatalf("read agent install jobs migration: %v", err)
			}
			if err := migrateUpTo(db, fstest.MapFS{
				fmt.Sprintf("migrations/%04d_agent_install_jobs.sql", legacyVersion): &fstest.MapFile{Data: contents},
			}, math.MaxInt64); err != nil {
				t.Fatalf("apply legacy agent install migration: %v", err)
			}

			if err := migrate(db); err != nil {
				t.Fatalf("migrate legacy agent install database: %v", err)
			}

			for _, version := range []int64{119, 120, 123} {
				var applied int
				if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
					t.Fatalf("read migration %d: %v", version, err)
				}
				if applied != 1 {
					t.Fatalf("migration %d applied = %d, want 1", version, applied)
				}
			}
			var tableCount int
			if err := db.QueryRow(
				`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_install_jobs'`,
			).Scan(&tableCount); err != nil {
				t.Fatalf("read agent_install_jobs table: %v", err)
			}
			if tableCount != 1 {
				t.Fatalf("agent_install_jobs table count = %d, want 1", tableCount)
			}
		})
	}
}
