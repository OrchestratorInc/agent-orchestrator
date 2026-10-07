package sqlite

import (
	"fmt"
	"strings"
	"testing"
)

func TestMigratePreservesHibernationPreviewDatabase(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/0190_session_hibernation.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int64{179, 180, 181} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, version-1)
			if _, err := db.Exec(strings.Split(string(body), "-- +goose Down")[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, version); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := migrate(db); err != nil {
					t.Fatal(err)
				}
			}
			for table, columns := range map[string][]string{
				"sessions":              {"hibernated_at", "provision_steps", "artifact_dir", "session_output_type"},
				"conversation_messages": {"sender_session_id", "sender_project_id", "sender_display_name"},
			} {
				for _, column := range columns {
					var count int
					if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil || count != 1 {
						t.Fatalf("%s.%s count=%d, error=%v", table, column, count, err)
					}
				}
			}
		})
	}
}
