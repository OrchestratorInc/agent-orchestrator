package sqlite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// search.db is derived data: a damaged one must be rebuilt, never keep the
// daemon from starting, and never touch ao.db.
func TestOpenRebuildsADamagedSearchIndex(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "kept", Path: filepath.Join(dir, "repo")}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	path := searchDatabasePath(dir)
	if err := os.WriteFile(path, bytes.Repeat([]byte("not a database "), 512), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err = Open(dir)
	if err != nil {
		t.Fatalf("open with a damaged search.db: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if docs, err := st.CountSessionSearchDocs(ctx); err != nil || docs != 0 {
		t.Fatalf("rebuilt index: %d documents, err %v", docs, err)
	}
	if _, found, err := st.GetProject(ctx, "kept"); err != nil || !found {
		t.Fatalf("ao.db lost its data: found %v, err %v", found, err)
	}
}

// Search is optional. A search.db that cannot be opened at all (here a
// directory stands where the file belongs, which is not "damage" a rebuild
// fixes) must turn search off and leave the rest of the store working.
func TestOpenWorksWhenTheSearchIndexCannotOpen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if err := os.Mkdir(searchDatabasePath(dir), 0o700); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open with an unopenable search.db: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if st.SessionSearchUnavailable() == nil {
		t.Fatal("search reported available over an unopenable search.db")
	}
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "kept", Path: filepath.Join(dir, "repo")}); err != nil {
		t.Fatalf("write with search off: %v", err)
	}
	if _, found, err := st.GetProject(ctx, "kept"); err != nil || !found {
		t.Fatalf("read with search off: found %v, err %v", found, err)
	}
}

// The app's readers never attach search.db, so an index that breaks while the
// daemon runs fails search alone. Each read below runs concurrently, so the
// pool has to open new connections after search.db is gone.
func TestAppReadsSurviveASearchIndexThatBreaksAfterOpen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SessionSearchUnavailable(); err != nil {
		t.Fatalf("search unavailable over a fresh data dir: %v", err)
	}
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "kept", Path: filepath.Join(dir, "repo")}); err != nil {
		t.Fatal(err)
	}
	if err := removeSearchFiles(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(searchDatabasePath(dir), 0o700); err != nil {
		t.Fatal(err)
	}

	const readers = maxReaders
	errs := make(chan error, readers)
	var start sync.WaitGroup
	start.Add(1)
	for range readers {
		go func() {
			start.Wait()
			_, found, err := st.GetProject(ctx, "kept")
			if err == nil && !found {
				err = errors.New("project not found")
			}
			errs <- err
		}()
	}
	start.Done()
	for range readers {
		if err := <-errs; err != nil {
			t.Fatalf("app read after search.db broke: %v", err)
		}
	}
}

// AO_SESSION_SEARCH=off is the support lever for a machine where the index
// misbehaves: search.db is never opened (or created), and the store works.
func TestSearchSwitchedOffNeverOpensTheIndex(t *testing.T) {
	t.Setenv(sessionSearchSwitch, "off")
	dir := t.TempDir()
	ctx := context.Background()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open with search switched off: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if !errors.Is(st.SessionSearchUnavailable(), errSessionSearchOff) {
		t.Fatalf("search unavailable = %v, want switched off", st.SessionSearchUnavailable())
	}
	if _, err := os.Stat(searchDatabasePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("search.db exists with search switched off (stat err %v)", err)
	}
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "kept", Path: filepath.Join(dir, "repo")}); err != nil {
		t.Fatalf("write with search off: %v", err)
	}
	if _, found, err := st.GetProject(ctx, "kept"); err != nil || !found {
		t.Fatalf("read with search off: found %v, err %v", found, err)
	}
}

// The reset behind `ao search --reindex` empties the index in place and leaves
// it usable: same file, empty tables, the same owner.
func TestResetEmptiesTheIndexInPlace(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	if err := st.UpsertSessionSearchDocs(ctx, []domain.SessionSearchDoc{{
		SourceKey: "chat:s", DocKey: "d", SessionID: "s", Kind: domain.SessionSearchKindUser,
		Body: "needle", CreatedAt: now, IndexedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	if docs, err := st.CountSessionSearchDocs(ctx); err != nil || docs != 1 {
		t.Fatalf("before reset: %d documents, err %v", docs, err)
	}
	if err := st.ResetSessionSearchIndex(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if docs, err := st.CountSessionSearchDocs(ctx); err != nil || docs != 0 {
		t.Fatalf("after reset: %d documents, err %v", docs, err)
	}
	if err := st.UpsertSessionSearchDocs(ctx, []domain.SessionSearchDoc{{
		SourceKey: "chat:s", DocKey: "d", SessionID: "s", Kind: domain.SessionSearchKindUser,
		Body: "needle", CreatedAt: now, IndexedAt: now,
	}}); err != nil {
		t.Fatalf("write after reset: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopening keeps the rebuilt index: the reset kept the owner, so the owner
	// check does not clear it again.
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if docs, err := st.CountSessionSearchDocs(ctx); err != nil || docs != 1 {
		t.Fatalf("after reopen: %d documents, err %v", docs, err)
	}
}
