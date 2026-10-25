package sqlite

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Tests locate chain positions by migration NAME (migrationVersion(t, ...)).
// A bare literal pins a checkpoint to whatever version a file held when the
// test was written; the fork renumbers its local migrations on every sync
// settle, and a stranded literal turns into a mystery red — or silently stops
// covering the migration it meant to target. The 2026-10-25 red of
// TestReviewRunCurrentHeadLookupIndex worked exactly that way: its upTo
// target stayed at an old upstream version while the lookup migration moved
// above it.
//
// This guard fails on any bare 90xx or timestamp-style version literal in
// test code. Files in the allowlist hardcode versions on purpose: they
// simulate specific historical burns that no current file will ever re-occupy.
var hardcodedVersionAllowlist = map[string]string{
	"migrate_burned_versions_test.go":     "simulates specific historical version burns",
	"migrate_fork_reserved_range_test.go": "defines forkReservedFloor itself",
	"migrate_snapshot_test.go":            "synthetic MapFS fixture timestamps, not chain positions",
}

var hardcodedVersionPattern = regexp.MustCompile("[^\\w\"`](90\\d{2}|2026\\d{13})\\b")

func TestNoHardcodedRenumberableMigrationTargets(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, allowed := hardcodedVersionAllowlist[name]; allowed {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if hardcodedVersionPattern.MatchString(line) {
				t.Errorf("%s:%d: bare migration-version literal — resolve by name via migrationVersion(t, \"<suffix>\") instead; hardcodes strand below renumbered files: %s",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
}
