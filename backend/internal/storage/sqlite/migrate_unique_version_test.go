package sqlite

import (
	"testing"
)

// TestMigrationVersionsAreUnique scans the embedded migration filenames and
// parses each version with goose.NumericComponent — the same function goose
// itself uses — so prefixes that parse to the same int64 (e.g. "014" vs
// "0014") are caught as a collision, not just identical strings. Catches the
// conflict with a clear message instead of a goose panic at runtime.
func TestMigrationVersionsAreUnique(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	seen := map[int64]string{} // parsed version -> filename
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}

		version, err := migrationFileNameVersion(name)
		if err != nil {
			t.Errorf("migration %q has no version goose can parse: %v", name, err)
			continue
		}

		if other, dup := seen[version]; dup {
			t.Errorf("duplicate migration version %d: %s vs %s", version, other, name)
			continue
		}
		seen[version] = name
	}
}
