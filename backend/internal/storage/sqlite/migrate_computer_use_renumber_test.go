package sqlite

import (
	"database/sql"
	"testing"
)

// A development database that ran this branch while the computer_use migration
// was numbered 20261021135126673 has the column and a receipt under that old
// number. Migrating must move the receipt instead of adding the column again.
func TestMigrateRepairsRenumberedComputerUseHistory(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 206)
	if err := migrate(db); err != nil {
		t.Fatalf("fresh migrate: %v", err)
	}
	assertComputerUseReceipts(t, db, 0, 1)

	// What a branch database looks like: same column, receipt at the old number.
	mustExec(t, db, `UPDATE goose_db_version SET version_id = 20261021135126673 WHERE version_id = 20261023123504180`)
	assertComputerUseReceipts(t, db, 1, 0)
	for range 2 {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate a branch database: %v", err)
		}
		assertComputerUseReceipts(t, db, 0, 1)
	}
	var column int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name = 'computer_use'`).Scan(&column); err != nil || column != 1 {
		t.Fatalf("computer_use columns = %d, %v; want 1", column, err)
	}
}

// assertComputerUseReceipts checks the applied receipts under the old and new
// computer_use migration numbers.
func assertComputerUseReceipts(t *testing.T, db *sql.DB, oldWant, newWant int) {
	t.Helper()
	var old, renumbered int
	if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1),
       (SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1)`,
		computerUseBranchMigrationVersion, computerUseMigrationVersion).Scan(&old, &renumbered); err != nil {
		t.Fatal(err)
	}
	if old != oldWant || renumbered != newWant {
		t.Fatalf("receipts: old %d, new %d; want %d, %d", old, renumbered, oldWant, newWant)
	}
}
