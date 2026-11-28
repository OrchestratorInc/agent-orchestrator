package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// Completion must survive ordinary closes without rescanning the data tables.
// The observer counts repair SELECTs only; schema/progress reads are allowed.
func openTimestampProgressStore(t *testing.T, dir string) (*Store, *repairScanCounts) {
	t.Helper()
	counts, done := &repairScanCounts{}, make(chan struct{})
	s, err := openWithTimestampOptions(dir, repairScanOptions(t, counts, done))
	if err != nil {
		t.Fatal(err)
	}
	<-done
	t.Cleanup(func() { _ = s.Close() })
	return s, counts
}

func TestTimestampProgressPlainWriterInvalidatesCompletion(t *testing.T) {
	for name, statement := range map[string]string{
		"update driver offset": `UPDATE sessions SET updated_at='2026-10-21 09:00:00 +0530 +0530' WHERE id='existing'`,
		"update SQL offset":    `UPDATE sessions SET updated_at='2026-10-21 09:00:00+05:30' WHERE id='existing'`,
		"update RFC3339":       `UPDATE sessions SET updated_at='2026-10-21T09:00:00+05:30' WHERE id='existing'`,
		"insert legacy row": `INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 VALUES('added',2,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 +0530')`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 VALUES('existing',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			first, _ := openTimestampProgressStore(t, dir)
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}

			// Use the unwrapped SQLite driver: invalidation cannot depend on Go
			// hooks that are absent from older AO binaries or plain SQLite tools.
			writer, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Exec(statement); err != nil {
				_ = writer.Close()
				t.Fatalf("ordinary SQLite writer failed: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			second, scans := openTimestampProgressStore(t, dir)
			if scans.reader.Load() == 0 {
				t.Fatal("legacy write did not invalidate completed repair")
			}
			id := domain.SessionID("existing")
			if name == "insert legacy row" {
				id = "added"
			}
			record, found, err := second.GetSession(context.Background(), id)
			want := time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
			if err != nil || !found || !record.UpdatedAt.Equal(want) {
				t.Fatalf("legacy instant: found=%v time=%v err=%v", found, record.UpdatedAt, err)
			}
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
			third, clean := openTimestampProgressStore(t, dir)
			if clean.reader.Load() != 0 || clean.writer.Load() != 0 {
				t.Fatalf("repair did not restore completion: reader=%d writer=%d", clean.reader.Load(), clean.writer.Load())
			}
			if err := third.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTimestampProgressCanonicalWritesDoNotRequireRescan(t *testing.T) {
	dir := t.TempDir()
	first, _ := openTimestampProgressStore(t, dir)
	if err := first.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "utc", Path: "/tmp/progress-utc", RegisteredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`UPDATE projects SET registered_at='2026-10-21 03:30:00 +0000 UTC' WHERE id='utc'`); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	second, counts := openTimestampProgressStore(t, dir)
	if counts.reader.Load() != 0 || counts.writer.Load() != 0 {
		t.Fatalf("canonical write invalidated completion: reader=%d writer=%d", counts.reader.Load(), counts.writer.Load())
	}
	if _, found, err := second.GetProject(context.Background(), "utc"); err != nil || !found {
		t.Fatalf("canonical project missing: found=%v err=%v", found, err)
	}
}

func TestTimestampProgressRestoreUsesDatabaseState(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 VALUES('restored',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 +0530')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := openTimestampProgressStore(t, dir)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Restore only the database. A completion ledger belonging to the database
	// that was replaced must not suppress repair of the restored snapshot.
	if err := os.WriteFile(filepath.Join(dir, "timestamp-repair-skips.json"), []byte(`{"_repaired":"3","_complete3/sessions":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ao.db"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, counts := openTimestampProgressStore(t, dir)
	if counts.reader.Load() == 0 {
		t.Fatal("restored legacy snapshot inherited unrelated completion")
	}
	record, found, err := restored.GetSession(context.Background(), "restored")
	want := time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
	if err != nil || !found || !record.UpdatedAt.Equal(want) {
		t.Fatalf("restored instant: found=%v time=%v err=%v", found, record.UpdatedAt, err)
	}
}

func TestTimestampProgressGenerationRejectsStaleCompletion(t *testing.T) {
	db := openMigratedTestDB(t)
	ctx := context.Background()
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareTimestampProgress(ctx, db, columns); err != nil {
		t.Fatal(err)
	}
	generation, _, err := beginTimestampProgress(ctx, db, "projects", "generation-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('concurrent','/tmp/concurrent','2026-10-21 09:00:00 +0530 +0530')`); err != nil {
		t.Fatal(err)
	}
	if err := finishTimestampProgress(ctx, db, "projects", "generation-test", generation, false); err == nil {
		t.Fatal("stale generation marked a concurrently changed table complete")
	}
	_, complete, err := beginTimestampProgress(ctx, db, "projects", "generation-test")
	if err != nil || complete {
		t.Fatalf("concurrent legacy write hidden: complete=%v err=%v", complete, err)
	}
}

func TestTimestampProgressMissingTriggerForcesRescan(t *testing.T) {
	dir := t.TempDir()
	first, _ := openTimestampProgressStore(t, dir)
	if err := first.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "trigger", Path: "/tmp/progress-trigger", RegisteredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	var trigger string
	if err := writer.QueryRow(`SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name='projects'
 AND name GLOB 'ao_timestamp_*' AND sql LIKE '%AFTER UPDATE%'`).Scan(&trigger); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if _, err := writer.Exec("DROP TRIGGER " + quoteIdentifier(trigger)); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if _, err := writer.Exec(`UPDATE projects SET registered_at='2026-10-21 09:00:00 +0530 +0530' WHERE id='trigger'`); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	second, counts := openTimestampProgressStore(t, dir)
	if counts.reader.Load() == 0 {
		t.Fatal("missing invalidation trigger left the completion marker trusted")
	}
	record, found, err := second.GetProject(context.Background(), "trigger")
	want := time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
	if err != nil || !found || !record.RegisteredAt.Equal(want) {
		t.Fatalf("project instant after trigger recovery: found=%v time=%v err=%v", found, record.RegisteredAt, err)
	}
}
