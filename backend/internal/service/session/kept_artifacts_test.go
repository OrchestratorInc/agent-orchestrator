package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	sqlitestore "github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// storeDeletingCommander deletes sessions through the store, as the manager does.
type storeDeletingCommander struct {
	*fakeCommander
	store *sqlitestore.Store
}

func (c *storeDeletingCommander) DeleteSession(ctx context.Context, id domain.SessionID, _ bool) error {
	deleted, err := c.store.DeleteTerminatedSession(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return errors.New("session not deleted")
	}
	return nil
}

// dirEmptier deletes a session's artifact files and leaves the directory, as
// the production deleter does.
type dirEmptier struct{ dataDir string }

func (d dirEmptier) DeleteSession(_ context.Context, id domain.SessionID) error {
	dir := sessionartifacts.Dir(d.dataDir, id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

type keptFixture struct {
	svc     *Service
	store   *sqlitestore.Store
	dataDir string
	now     time.Time
}

func newKeptFixture(t *testing.T) keptFixture {
	t.Helper()
	dataDir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, dataDir)
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	svc := NewWithDeps(Deps{
		Manager: &storeDeletingCommander{fakeCommander: &fakeCommander{}, store: store}, Store: store, DataDir: dataDir,
		ArtifactDeleter: dirEmptier{dataDir: dataDir}, Clock: func() time.Time { return now },
	})
	return keptFixture{svc: svc, store: store, dataDir: dataDir, now: now}
}

// archivedSession creates an archived session with no worktree, named name,
// with the given files in its artifact directory and the first registered.
func (f keptFixture) archivedSession(t *testing.T, name string, files ...string) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	rec, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, DisplayName: name,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: f.now}, CreatedAt: f.now, UpdatedAt: f.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := f.store.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	dir := sessionartifacts.Dir(f.dataDir, rec.ID)
	for _, rel := range append(files, sessionartifacts.VersionsDir+"/blobs/ab/abc") {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(files) > 0 {
		if _, _, err := f.store.AddSessionArtifacts(ctx, rec.ID, []domain.SessionArtifactRegistration{{Path: files[0]}}, f.now); err != nil {
			t.Fatal(err)
		}
	}
	return rec.ID
}

// Deleting a session and keeping its artifacts leaves its files, view only,
// listed under its project and served from the same directory, while its
// version history goes with it. Deleting the last file takes the rest.
func TestDeleteKeepingArtifactsKeepsTheFilesUntilEachIsDeleted(t *testing.T) {
	ctx := context.Background()
	f := newKeptFixture(t)
	id := f.archivedSession(t, "Quarterly report", "report.html", "img/chart.png")
	dir := sessionartifacts.Dir(f.dataDir, id)

	if err := f.svc.DeleteKeepingArtifacts(ctx, id, false); err != nil {
		t.Fatalf("DeleteKeepingArtifacts: %v", err)
	}
	if _, found, _ := f.store.GetSession(ctx, id); found {
		t.Fatal("session row survived")
	}
	if _, err := os.Stat(filepath.Join(dir, "report.html")); err != nil {
		t.Fatalf("kept file gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionartifacts.VersionsDir)); !os.IsNotExist(err) {
		t.Fatalf("version history kept: %v", err)
	}

	sets, err := f.svc.ListKeptArtifacts(ctx)
	if err != nil || len(sets) != 1 {
		t.Fatalf("kept sets = %+v, %v", sets, err)
	}
	set := sets[0]
	if set.SessionID != id || set.ProjectID != "mer" || set.Title != "Quarterly report" || !set.SessionDeleted || !set.At.Equal(f.now) {
		t.Fatalf("kept set = %+v", set)
	}
	var paths []string
	for _, file := range set.Files {
		paths = append(paths, file.Path)
		if file.Path == "report.html" && (!file.Registered || file.ID == "") {
			t.Fatalf("report.html lost its registration: %+v", file)
		}
	}
	if !slices.Equal(paths, []string{"img/chart.png", "report.html"}) {
		t.Fatalf("kept files = %v", paths)
	}

	if location, err := f.svc.ArtifactLocation(ctx, id); err != nil || location != dir {
		t.Fatalf("ArtifactLocation = %q, %v; want %q", location, err, dir)
	}
	stand, kept, err := f.svc.ArtifactSession(ctx, id)
	if err != nil || !kept || stand.Metadata.ArtifactDir != dir || !stand.IsTerminated || len(stand.ArtifactFiles) != 2 {
		t.Fatalf("ArtifactSession = %+v, %v, %v", stand, kept, err)
	}

	for _, rel := range []string{"report.html", "img/chart.png"} {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.ForgetKeptArtifact(ctx, id, rel); err != nil {
			t.Fatalf("ForgetKeptArtifact(%s): %v", rel, err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("empty kept dir left behind: %v", err)
	}
	if sets, err := f.svc.ListKeptArtifacts(ctx); err != nil || len(sets) != 0 {
		t.Fatalf("kept sets after the last delete = %+v, %v", sets, err)
	}
	if _, _, err := f.svc.ArtifactSession(ctx, id); !isNotFound(err) {
		t.Fatalf("ArtifactSession after the last delete err = %v, want not found", err)
	}
}

// A session without artifact files is simply deleted, and a plain delete takes
// the files and forgets a kept record an earlier, failed keeping delete left.
func TestDeleteWithoutKeepingLeavesNothingKept(t *testing.T) {
	ctx := context.Background()
	f := newKeptFixture(t)
	empty := f.archivedSession(t, "Nothing")
	if err := f.svc.DeleteKeepingArtifacts(ctx, empty, false); err != nil {
		t.Fatalf("DeleteKeepingArtifacts: %v", err)
	}
	withFiles := f.archivedSession(t, "Report", "report.md")
	if err := f.store.KeepSessionArtifacts(ctx, domain.KeptArtifactSession{SessionID: withFiles, ProjectID: "mer", KeptAt: f.now}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, withFiles, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(sessionartifacts.Dir(f.dataDir, withFiles)); !os.IsNotExist(err) {
		t.Fatalf("artifact dir kept: %v", err)
	}
	if _, found, err := f.store.GetKeptArtifactSession(ctx, withFiles); err != nil || found {
		t.Fatalf("kept record left: %v, %v", found, err)
	}
	if sets, err := f.svc.ListKeptArtifacts(ctx); err != nil || len(sets) != 0 {
		t.Fatalf("kept sets = %+v, %v", sets, err)
	}
}

// Retention deletes expired sessions and keeps the artifacts of those that
// have any.
func TestDeleteExpiredKeepsArtifactsOfTheSessionsItDeletes(t *testing.T) {
	ctx := context.Background()
	f := newKeptFixture(t)
	withFiles := f.archivedSession(t, "Report", "report.md")
	without := f.archivedSession(t, "Nothing")
	f.svc.clock = func() time.Time { return f.now.AddDate(0, 0, 60) }

	if err := f.svc.DeleteExpired(ctx, 30); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	for _, id := range []domain.SessionID{withFiles, without} {
		if _, found, _ := f.store.GetSession(ctx, id); found {
			t.Fatalf("expired session %s not deleted", id)
		}
	}
	sets, err := f.svc.ListKeptArtifacts(ctx)
	if err != nil || len(sets) != 1 || sets[0].SessionID != withFiles {
		t.Fatalf("kept sets = %+v, %v; want %s's", sets, err, withFiles)
	}
}

// A removed project's sessions keep their rows, which no project lists, so
// their artifacts are listed as kept, with no project.
func TestListKeptArtifactsIncludesRemovedProjectSessions(t *testing.T) {
	ctx := context.Background()
	f := newKeptFixture(t)
	id := f.archivedSession(t, "Report", "report.md")
	f.archivedSession(t, "Nothing")
	if sets, err := f.svc.ListKeptArtifacts(ctx); err != nil || len(sets) != 0 {
		t.Fatalf("kept sets before removal = %+v, %v", sets, err)
	}
	if ok, err := f.store.ArchiveProject(ctx, "mer", f.now); err != nil || !ok {
		t.Fatalf("archive project = %v, %v", ok, err)
	}
	sets, err := f.svc.ListKeptArtifacts(ctx)
	if err != nil || len(sets) != 1 {
		t.Fatalf("kept sets = %+v, %v", sets, err)
	}
	if set := sets[0]; set.SessionID != id || set.ProjectID != "" || set.SessionDeleted || set.Title != "Report" || len(set.Files) != 1 {
		t.Fatalf("kept set = %+v", set)
	}
}
