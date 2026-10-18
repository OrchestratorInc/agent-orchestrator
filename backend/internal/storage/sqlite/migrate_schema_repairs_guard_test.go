package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// guardMigrationFiles indexes migration files by goose version.
func guardMigrationFiles(t *testing.T) map[int64]string {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	files := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		version, err := goose.NumericComponent(e.Name())
		if err != nil {
			t.Fatalf("parse version from %q: %v", e.Name(), err)
		}
		files[version] = e.Name()
	}
	return files
}

// guardReadMigration returns the raw contents of an embedded migration file.
func guardReadMigration(t *testing.T, name string) string {
	t.Helper()
	content, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(content)
}

// TestSchemaRepairsCorrespondToMigrations is the completeness guard for the
// manual schemaRepairs/tableRepairs lists: every repair entry must name a
// table and object that the migration file at that version actually mentions.
// An entry whose objects do not exist in the migration is either a typo (the
// repair silently no-ops at boot) or points at the wrong version (the repair
// replays the wrong DDL). Neither is catchable at boot, because reconcileSchema
// only acts on databases that are already drifted — this test catches it at
// review time instead.
//
// A version with no migration file is expected for burned numbers: the repair
// entry is then the only recovery path, and there is nothing to compare
// against.
func TestSchemaRepairsCorrespondToMigrations(t *testing.T) {
	files := guardMigrationFiles(t)

	checkColumn := func(table, column string, version int64) {
		t.Helper()
		file, ok := files[version]
		if !ok {
			return
		}
		content := guardReadMigration(t, file)
		if !strings.Contains(content, table) || !strings.Contains(content, column) {
			t.Errorf("schemaRepairs entry %s.%s (v%d) names objects absent from %s: "+
				"the repair would no-op or replay the wrong DDL", table, column, version, file)
		}
	}
	checkTable := func(table string, version int64) {
		t.Helper()
		file, ok := files[version]
		if !ok {
			return
		}
		content := guardReadMigration(t, file)
		if !strings.Contains(content, "CREATE TABLE "+table) {
			t.Errorf("tableRepairs entry %s (v%d) does not match %s: no CREATE TABLE %s in the migration",
				table, version, file, table)
		}
	}

	for _, rc := range schemaRepairs {
		checkColumn(rc.table, rc.column, rc.version)
	}
	for _, tr := range tableRepairs {
		checkTable(tr.table, tr.version)
	}
}
