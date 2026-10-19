package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Preview CodeBuddy builds used 214 and 215 before main assigned them to
// version refresh and harness releases. Release only identifiable preview
// ledger entries whose canonical physical effects are entirely absent. Goose
// then applies main's unchanged migrations; native/session data stays intact.
func repairRenumberedCodeBuddyMigrationHistory(db *sql.DB) error {
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`).Scan(&present); err != nil || present == 0 {
		return err
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if !strings.Contains(schema, "'codebuddy'") {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, version := range []int64{214, 215} {
		var applied int
		if err := tx.QueryRow(`SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1), 0)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied == 0 {
			continue
		}
		var effects int
		query := `SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name = 'version_refresh'`
		expected := 1
		if version == 215 {
			query = `SELECT COUNT(*) FROM sqlite_master WHERE (type = 'table' AND name IN ('managed_harness_releases', 'session_harness_releases')) OR (type = 'trigger' AND name IN ('sessions_record_harness_release_insert', 'sessions_record_harness_release_update'))`
			expected = 4
		}
		if err := tx.QueryRow(query).Scan(&effects); err != nil {
			return err
		}
		if effects == expected {
			continue
		}
		if effects != 0 {
			return fmt.Errorf("canonical migration %d has a partial schema; refusing preview repair", version)
		}
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, version); err != nil {
			return err
		}
	}
	return tx.Commit()
}
