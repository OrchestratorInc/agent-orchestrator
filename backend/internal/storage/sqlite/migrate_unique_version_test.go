package sqlite

import (
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigrationVersionsAreUnique(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	seen := map[int64]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}

		version, err := goose.NumericComponent(name)
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
