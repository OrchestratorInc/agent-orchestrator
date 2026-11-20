package project_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestManager_AddPersistsRegisteredAtReadableInNumericOffsetZone is the
// regression guard for issue #6890.
//
// projects.registered_at is read back into a time.Time, and modernc.org/sqlite
// binds a time.Time by rendering it with time.Time.String() and re-parses the
// column with "2006-01-02 15:04:05.999999999 -0700 MST". Adding a project used a
// bare time.Now(), so in a zone whose Go name is a numeric offset the stored
// value is "2026-10-18 05:03:55.6144103 +0330 +0330 m=+185.748497701". "+0330"
// cannot match the layout's "MST" element, the driver hands the column back as a
// string, and the scan into time.Time fails:
//
//	unsupported Scan, storing driver.Value type string into type *time.Time
//
// The row is unreadable the moment it is written, which on a fresh install turns
// GET /api/v1/projects into 500 PROJECTS_LIST_FAILED and takes the projects list
// with it.
//
// The zone is forced rather than inherited so the test reproduces the failure on
// any host, including the UTC hosts CI runs on. time.Local is process-wide, so
// this test must not call t.Parallel: the testing package resumes parallel tests
// only after every sequential test in the package has finished.
func TestManager_AddPersistsRegisteredAtReadableInNumericOffsetZone(t *testing.T) {
	original := time.Local
	time.Local = time.FixedZone("+0330", 3*60*60+30*60)
	t.Cleanup(func() { time.Local = original })

	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	ctx := context.Background()

	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store})

	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("numeric-zone")}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// ListProjects scans registered_at into a time.Time, which is the read that
	// fails while the column holds a local-zone String() value.
	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects after Add: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("ListProjects = %d projects, want 1", len(projects))
	}
	got := projects[0].RegisteredAt
	if got.IsZero() {
		t.Fatal("registered_at came back as the zero time")
	}
	// Only the canonical "+0000 UTC" encoding parses back into the UTC location,
	// so this pins the stored form as well as its readability.
	if got.Location() != time.UTC {
		t.Errorf("registered_at location = %v, want UTC", got.Location())
	}
	if delta := time.Since(got); delta < 0 || delta > time.Hour {
		t.Errorf("registered_at = %v, want an instant close to now", got)
	}
}
