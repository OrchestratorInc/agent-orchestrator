package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestProjectWritesReadCommittedRevisionWithoutReaderPool(t *testing.T) {
	version, err := expectedMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	writer := openMigratedDatabaseCopy(t, version)
	reader, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "unused.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(writer, reader)
	ctx := context.Background()
	seed := domain.ProjectRecord{ID: "tx", Path: t.TempDir(), DisplayName: "Original", RepoOriginURL: "https://github.com/example/repo.git", Kind: domain.ProjectKindWorkspace, RegisteredAt: time.Now()}
	if err := store.UpsertProject(ctx, seed); err != nil {
		t.Fatal(err)
	}
	row, updated, err := store.UpdateProjectConfig(ctx, "tx", 0, domain.ProjectConfig{DefaultBranch: "develop"})
	if err != nil || !updated || row.Revision != 1 || row.Config.DefaultBranch != "develop" || row.Path != seed.Path || row.DisplayName != seed.DisplayName || row.RepoOriginURL != seed.RepoOriginURL || row.Kind != seed.Kind || !row.RegisteredAt.Equal(seed.RegisteredAt) {
		t.Fatalf("config read used reader pool or stale row: row=%#v updated=%v err=%v", row, updated, err)
	}
	if _, updated, err := store.UpdateProjectConfig(ctx, "tx", 0, domain.ProjectConfig{}); err != nil || updated {
		t.Fatalf("stale CAS: updated=%v err=%v", updated, err)
	}
	row, updated, err = store.UpdateProjectSettings(ctx, "tx", "Renamed", domain.ProjectConfig{})
	if err != nil || !updated || row.Revision != 2 || row.DisplayName != "Renamed" {
		t.Fatalf("settings committed row: row=%#v updated=%v err=%v", row, updated, err)
	}
	row, updated, err = store.SetProjectPermissions(ctx, "tx", domain.PermissionModeAcceptEdits)
	if err != nil || !updated || row.Revision != 3 || row.Config.AgentConfig.Permissions != domain.PermissionModeAcceptEdits {
		t.Fatalf("permissions committed row: row=%#v updated=%v err=%v", row, updated, err)
	}
	if updated, err := store.ArchiveProject(ctx, "tx", time.Now()); err != nil || !updated {
		t.Fatalf("archive: updated=%v err=%v", updated, err)
	}
	if _, updated, err := store.UpdateProjectConfig(ctx, "tx", 4, domain.ProjectConfig{}); err != nil || updated {
		t.Fatalf("archive fence: updated=%v err=%v", updated, err)
	}
	if _, updated, err := store.UpdateProjectConfig(ctx, "missing", 0, domain.ProjectConfig{}); err != nil || updated {
		t.Fatalf("missing fence: updated=%v err=%v", updated, err)
	}
}

func TestProjectConfigCASAcrossIndependentWriters(t *testing.T) {
	version, err := expectedMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	first := openMigratedDatabaseCopyAt(t, dir, version, pragmas)
	second, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	second.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = second.Close() })
	ctx := context.Background()
	stores := []*sqlitestore.Store{sqlitestore.NewStore(first, first), sqlitestore.NewStore(second, second)}
	if err := stores[0].UpsertProject(ctx, domain.ProjectRecord{ID: "concurrent", Path: t.TempDir(), RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		row     domain.ProjectRecord
		updated bool
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i, store := range stores {
		go func() {
			<-start
			row, updated, err := store.UpdateProjectConfig(ctx, "concurrent", 0, domain.ProjectConfig{DefaultBranch: []string{"alpha", "beta"}[i]})
			results <- result{row, updated, err}
		}()
	}
	close(start)
	winners := 0
	var winner domain.ProjectRecord
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.updated {
			winners++
			winner = got.row
		}
	}
	stored, _, err := stores[0].GetProject(ctx, "concurrent")
	if err != nil || winners != 1 || winner.Revision != 1 || winner.Config.DefaultBranch != stored.Config.DefaultBranch || stored.Revision != 1 {
		t.Fatalf("independent writers: winners=%d winner=%#v stored=%#v err=%v", winners, winner, stored, err)
	}
}

func TestProjectRevisionExplicitResetCannotReenableStaleCAS(t *testing.T) {
	version, err := expectedMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	for _, recursive := range []string{"OFF", "ON"} {
		t.Run(recursive, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, version)
			if _, err := db.Exec("PRAGMA recursive_triggers = " + recursive); err != nil {
				t.Fatal(err)
			}
			store := sqlitestore.NewStore(db, db)
			ctx := context.Background()
			if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "reset", Path: t.TempDir(), RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			row, updated, err := store.UpdateProjectConfig(ctx, "reset", 0, domain.ProjectConfig{DefaultBranch: "committed"})
			if err != nil || !updated || row.Revision != 1 {
				t.Fatalf("seed CAS: row=%#v updated=%v err=%v", row, updated, err)
			}
			// A manual reset must never make the pre-update snapshot usable again.
			_, resetErr := db.Exec(`UPDATE projects SET revision = 0 WHERE id = 'reset'`)
			_, updated, err = store.UpdateProjectConfig(ctx, "reset", 0, domain.ProjectConfig{DefaultBranch: "stale"})
			if err != nil || updated {
				t.Fatalf("explicit reset reenabled stale CAS: updated=%v err=%v resetErr=%v", updated, err, resetErr)
			}
			stored, _, err := store.GetProject(ctx, "reset")
			if err != nil || stored.Revision < row.Revision || stored.Config.DefaultBranch != "committed" {
				t.Fatalf("revision rollback or config loss: row=%#v err=%v", stored, err)
			}
			for _, expression := range []string{"revision - 1", "revision + 100"} {
				if _, err := db.Exec(`UPDATE projects SET revision = ` + expression + ` WHERE id = 'reset'`); err == nil {
					t.Fatalf("non-database revision assignment accepted: %s", expression)
				}
			}
			before := stored.Revision
			if _, err := db.Exec(`UPDATE projects SET revision = revision + 1 WHERE id = 'reset'`); err != nil {
				t.Fatal(err)
			}
			stored, _, err = store.GetProject(ctx, "reset")
			if err != nil || stored.Revision != before+1 {
				t.Fatalf("next revision assignment: row=%#v err=%v", stored, err)
			}
		})
	}
}

func TestProjectRevisionCoversEveryWrite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	read := func(query string) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	exec(`INSERT INTO projects (id, path, registered_at) VALUES ('rev', '/rev', CURRENT_TIMESTAMP)`)
	if got := read(`SELECT revision FROM projects WHERE id = 'rev'`); got != 0 {
		t.Fatalf("initial revision=%d want=0", got)
	}
	for _, recursive := range []string{"OFF", "ON"} {
		t.Run(recursive, func(t *testing.T) {
			exec("PRAGMA recursive_triggers = " + recursive)
			for _, query := range []string{
				`UPDATE projects SET path = path WHERE id = 'rev'`,
				`UPDATE projects SET display_name = 'B' WHERE id = 'rev'`,
				`UPDATE projects SET display_name = 'A' WHERE id = 'rev'`,
				`UPDATE projects SET repo_origin_url = 'https://github.com/example/repo.git' WHERE id = 'rev'`,
				`UPDATE projects SET config = '{"defaultBranch":"develop"}' WHERE id = 'rev'`,
				`UPDATE projects SET archived_at = CURRENT_TIMESTAMP WHERE id = 'rev'`,
				`UPDATE projects SET archived_at = NULL WHERE id = 'rev'`,
			} {
				before := read(`SELECT revision FROM projects WHERE id = 'rev'`)
				exec(query)
				if got := read(`SELECT revision FROM projects WHERE id = 'rev'`); got != before+1 {
					t.Fatalf("revision=%d want=%d after %s", got, before+1, query)
				}
			}
			before := read(`SELECT revision FROM projects WHERE id = 'rev'`)
			exec(`BEGIN`)
			exec(`UPDATE projects SET display_name = 'rollback' WHERE id = 'rev'`)
			exec(`ROLLBACK`)
			if got := read(`SELECT revision FROM projects WHERE id = 'rev'`); got != before {
				t.Fatalf("rollback revision=%d want=%d", got, before)
			}
		})
	}
	for _, query := range []string{
		`UPDATE projects SET revision = -1 WHERE id = 'rev'`,
		`UPDATE projects SET revision = 1.5 WHERE id = 'rev'`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("invalid revision accepted: %s", query)
		}
	}
	exec(`INSERT INTO projects (id, path, registered_at, revision) VALUES ('overflow', '/overflow', CURRENT_TIMESTAMP, 9223372036854775807)`)
	for _, recursive := range []string{"OFF", "ON"} {
		exec("PRAGMA recursive_triggers = " + recursive)
		for _, query := range []string{
			`UPDATE projects SET display_name = 'overflow' WHERE id = 'overflow'`,
			`UPDATE projects SET revision = revision + 1 WHERE id = 'overflow'`,
			`UPDATE projects SET revision = 0 WHERE id = 'overflow'`,
		} {
			if _, err := db.Exec(query); err == nil {
				t.Fatalf("revision overflow/reset was not refused: %s", query)
			}
		}
		if got := read(`SELECT revision FROM projects WHERE id = 'overflow'`); got != 9223372036854775807 {
			t.Fatalf("overflow/reset changed revision: %d", got)
		}
	}
	exec(`DROP TRIGGER projects_revision_guard`)
	if err := reconcileSchema(db); err == nil || !strings.Contains(err.Error(), "projects_revision_guard") {
		t.Fatalf("missing revision guard silently admitted: %v", err)
	}
	exec(`DROP TRIGGER projects_revision_update`)
	if err := reconcileSchema(db); err == nil || !strings.Contains(err.Error(), "projects_revision_update") {
		t.Fatalf("missing revision trigger silently admitted: %v", err)
	}
}
