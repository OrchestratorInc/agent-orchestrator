package sqlite

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// forkReservedFloor is the first migration version reserved for fork-local
// migrations. Upstream numbers its migrations sequentially from 1 and will not
// reach this range, so a rebase can never make an upstream file collide with a
// fork one.
const forkReservedFloor int64 = 9000

// forkLocalMigrations lists the migrations that exist only on this fork.
// They must live at or above forkReservedFloor.
//
// Why this test exists: every sync rebase used to renumber these files onto
// whatever number was free next, and each move burned that number in
// goose_db_version on every live install. The upstream migration that later
// took the number was then skipped forever, and the daemon died at boot on a
// missing column. Measured over this fork's history:
//
//	add_user_config                       5 numbers (28, 32, 37, 41, 89)
//	normalize_activity_last_at            7 numbers (89, 95, 96, 99, 102, 103, 105)
//
// The 0100 collision (add_user_config had held it, then session_model took it)
// is what killed a daemon with "no such column: model". Pinning fork
// migrations above forkReservedFloor removes the collision at the source
// instead of adding one schemaRepairs entry per casualty.
//
// conversation_provider_ownership_epochs used to live here too (it churned
// through 100/101/103 the same way), but upstream shipped the identical
// migration as its own 0101_conversation_provider_ownership_epochs.sql — the
// fork now uses that file directly instead of carrying a duplicate at 9000+.
//
// add_user_config was briefly (and incorrectly) dropped from this list during
// a 2026-08-28 sync: a mid-rebase collision made its file land at a low
// number (0116/0117) that happened to look like an upstream-claimed slot, but
// upstream never actually shipped this migration — confirmed via
// `git show origin/main:.../0116_add_user_config.sql` (does not exist). It
// belongs in forkLocalMigrations like normalize_activity_last_at once lived;
// both should live at forkReservedFloor+ once the collision that displaced it
// is cleared.
//
// normalize_activity_last_at was removed from this list during the 2026-09-02
// sync: upstream merged the fork's #3900 verbatim as its own
// 0120_normalize_activity_last_at.sql (byte-identical to the fork's 9002
// copy), so the fork now uses that file directly instead of carrying a
// duplicate at 9000+ — same as conversation_provider_ownership_epochs above.
var forkLocalMigrations = []string{
	"add_user_config",
	"review_run_publication",
	"session_provider_stamp",
	"gateway_entries",
}

func TestForkMigrationsUseReservedRange(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		for _, fork := range forkLocalMigrations {
			if !strings.Contains(name, fork) {
				continue
			}
			found[fork] = true
			version, err := goose.NumericComponent(name)
			if err != nil {
				t.Errorf("fork migration %q has no version goose can parse: %v", name, err)
				continue
			}
			if version < forkReservedFloor {
				t.Errorf("fork-local migration %q sits at version %d, below the reserved floor %d. "+
					"Upstream will eventually claim that number, and the next sync rebase will renumber "+
					"this file — burning %d on every install that already applied it and silently skipping "+
					"the upstream migration that inherits it. Move it to %d+.",
					name, version, forkReservedFloor, version, forkReservedFloor)
			}
		}
	}

	for _, fork := range forkLocalMigrations {
		if !found[fork] {
			t.Errorf("fork-local migration %q is listed here but no file matches it: "+
				"either it was merged upstream (drop it from forkLocalMigrations) or it was lost", fork)
		}
	}
}

// TestReservedRangeIsFreeOfUpstreamMigrations is the other half of the
// contract: the reserved range only works while upstream stays out of it.
// If upstream ever ships a migration at 9000+, the reservation must move
// rather than silently start colliding again.
func TestReservedRangeIsFreeOfUpstreamMigrations(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		version, err := goose.NumericComponent(name)
		if err != nil {
			continue // covered by TestMigrationVersionsAreUnique
		}
		if version < forkReservedFloor {
			continue
		}
		isFork := false
		for _, fork := range forkLocalMigrations {
			if strings.Contains(name, fork) {
				isFork = true
				break
			}
		}
		if !isFork {
			t.Errorf("migration %q occupies reserved version %d but is not listed in forkLocalMigrations. "+
				"If upstream now ships migrations at %d+, raise forkReservedFloor; if this is a new "+
				"fork-local migration, add it to the list.", name, version, forkReservedFloor)
		}
	}
}
