package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestWorkspaceManifestIndexServesStaleSnapshotWhileRefreshing(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nfirst\n")
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := NewWithDeps(Deps{Store: st})

	first, err := svc.GetWorkspaceManifest(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	var pending func()
	svc.runBackground = func(work func()) { pending = work }
	writeWorkspaceFile(t, repo, "README.md", "hello\nsecond\n")
	svc.InvalidateWorkspaceCache("ao-1")

	stale, err := svc.GetWorkspaceManifest(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale || !stale.Refreshing || stale.WorkspaceVersion != first.WorkspaceVersion {
		t.Fatalf("stale manifest = %+v, want refreshing snapshot at version %q", stale, first.WorkspaceVersion)
	}
	if pending == nil {
		t.Fatal("invalidation did not schedule a background refresh")
	}
	pending()

	refreshed, err := svc.GetWorkspaceManifest(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Stale || refreshed.Refreshing {
		t.Fatalf("refreshed manifest still stale: %+v", refreshed)
	}
	if refreshed.WorkspaceVersion == first.WorkspaceVersion {
		t.Fatalf("refreshed version = %q, want a new version", refreshed.WorkspaceVersion)
	}
}

func TestWorkspaceManifestIndexCoalescesConcurrentRefreshes(t *testing.T) {
	index := newWorkspaceManifestIndex()
	manifest := WorkspaceManifest{SessionID: "ao-1", WorkspaceVersion: "one"}
	index.publish("ao-1", manifest, 0)
	if !index.markStale("ao-1") {
		t.Fatal("first invalidation did not claim refresh")
	}
	if index.markStale("ao-1") {
		t.Fatal("second invalidation claimed a duplicate refresh")
	}
	got, ok := index.get("ao-1")
	if !ok || !got.Stale || !got.Refreshing {
		t.Fatalf("index entry = %+v, %v", got, ok)
	}
}

func TestWorkspaceManifestIndexEvictsLeastRecentlyUsedSnapshot(t *testing.T) {
	index := newWorkspaceManifestIndex()
	for n := 0; n < maxWorkspaceManifestEntries; n++ {
		id := domain.SessionID(string(rune(n + 1)))
		index.publish(id, WorkspaceManifest{SessionID: id}, 0)
	}
	oldest := domain.SessionID(string(rune(1)))
	recent := domain.SessionID(string(rune(2)))
	if _, ok := index.get(recent); !ok {
		t.Fatal("recent entry missing before eviction")
	}
	index.publish("overflow", WorkspaceManifest{SessionID: "overflow"}, 0)
	if _, ok := index.get(oldest); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if _, ok := index.get(recent); !ok {
		t.Fatal("recently accessed entry was evicted")
	}
}

func TestWorkspaceManifestIndexRejectsSnapshotInvalidatedDuringRefresh(t *testing.T) {
	index := newWorkspaceManifestIndex()
	generation := index.beginRefresh("ao-1")
	if index.markStale("ao-1") {
		t.Fatal("in-flight refresh should retain ownership")
	}
	if index.publish("ao-1", WorkspaceManifest{SessionID: "ao-1", WorkspaceVersion: "old"}, generation) {
		t.Fatal("snapshot invalidated during refresh was published as fresh")
	}
	got, ok := index.get("ao-1")
	if !ok || !got.Stale || got.Refreshing {
		t.Fatalf("published snapshot = %+v, %v; want usable stale snapshot", got, ok)
	}
}

// invalidatingStore runs onListPRs inside a manifest computation, which reads
// the session's PRs once per run, and counts those runs.
type invalidatingStore struct {
	*fakeStore
	prReads   int
	onListPRs func()
}

func (s *invalidatingStore) ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	s.prReads++
	if hook := s.onListPRs; hook != nil {
		s.onListPRs = nil
		hook()
	}
	return s.fakeStore.ListPRsBySession(ctx, id)
}

// The workspace stream reconciles as it connects, the moment Files first reads.
func TestWorkspaceManifestReaderServesSnapshotInvalidatedDuringItsRead(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nchanged\n")
	st := &invalidatingStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := NewWithDeps(Deps{Store: st})
	var pending []func()
	svc.runBackground = func(work func()) { pending = append(pending, work) }
	st.onListPRs = func() { svc.InvalidateWorkspaceCache("ao-1") }

	read, err := svc.GetWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if st.prReads != 1 {
		t.Fatalf("reader computed the manifest %d times, want once", st.prReads)
	}
	if !read.Stale || !read.Refreshing || len(read.Files) == 0 {
		t.Fatalf("interrupted read = %+v, want a complete snapshot marked stale and refreshing", read)
	}
	if len(pending) != 1 {
		t.Fatalf("background refreshes = %d, want 1", len(pending))
	}
	pending[0]()
	if st.prReads != 2 {
		t.Fatalf("background refresh computed the manifest %d times, want once more", st.prReads-1)
	}
	fresh, err := svc.GetWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Stale || fresh.Refreshing || fresh.WorkspaceVersion != read.WorkspaceVersion {
		t.Fatalf("refreshed manifest = %+v, want fresh at version %q", fresh, read.WorkspaceVersion)
	}
}

// The stream may only report ready with a snapshot no invalidation interrupted.
func TestWorkspaceManifestRefreshRecomputesSnapshotInvalidatedDuringItsRead(t *testing.T) {
	repo := newWorkspaceRepo(t)
	st := &invalidatingStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := NewWithDeps(Deps{Store: st})
	svc.runBackground = func(func()) {}
	st.onListPRs = func() {
		writeWorkspaceFile(t, repo, "README.md", "hello\nedited during the read\n")
		svc.InvalidateWorkspaceCache("ao-1")
	}

	refreshed, err := svc.RefreshWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if st.prReads != 2 || refreshed.Stale || refreshed.Refreshing || len(refreshed.Files) == 0 {
		t.Fatalf("refresh after %d reads = %+v, want a second, fresh read that includes the edit", st.prReads, refreshed)
	}
}

func TestWorkspaceManifestRefreshesAfterUnwatchedEdits(t *testing.T) {
	repo := newWorkspaceRepo(t)
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	now := time.Unix(100, 0)
	svc := NewWithDeps(Deps{Store: st, Clock: func() time.Time { return now }})
	first, err := svc.GetWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, repo, "README.md", "hello\nan unwatched edit\n")
	now = now.Add(workspaceCacheTTL + time.Nanosecond)
	fresh, err := svc.GetWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Stale || fresh.Refreshing || fresh.WorkspaceVersion == first.WorkspaceVersion || len(fresh.Files) == 0 {
		t.Fatalf("expired manifest did not refresh unwatched edit: %+v", fresh)
	}
}

func TestWorkspaceManifestReconcilesUnwatchedEditBeforeCacheExpiry(t *testing.T) {
	repo := newWorkspaceRepo(t)
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	now := time.Unix(100, 0)
	svc := NewWithDeps(Deps{Store: st, Clock: func() time.Time { return now }})
	first, err := svc.GetWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, repo, "reconnected.txt", "created while unwatched\n")
	fresh, err := svc.ReconcileWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Stale || fresh.Refreshing || fresh.WorkspaceVersion == first.WorkspaceVersion || len(fresh.Files) == 0 {
		t.Fatalf("reconnected manifest did not include unwatched edit inside cache lifetime: %+v", fresh)
	}
}

func TestWorkspaceDiffBatchesReuseOnlyFreshManifest(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nchanged\n")
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	now := time.Unix(100, 0)
	svc := NewWithDeps(Deps{Store: st, Clock: func() time.Time { return now }})
	first, err := svc.GetWorkspaceManifest(t.Context(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "git.log")
	t.Setenv("GIT_TRACE", trace)
	input := WorkspaceDiffInput{Paths: []string{"README.md"}, WorkspaceVersion: first.WorkspaceVersion}
	for range 3 {
		if _, err := svc.GetWorkspaceDiffs(t.Context(), "ao-1", input); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "--name-status") || strings.Contains(string(data), "--numstat") || strings.Contains(string(data), " status ") {
		t.Fatalf("warm batches recomputed manifest: %s", data)
	}
	writeWorkspaceFile(t, repo, "README.md", "hello\nchanged\nagain\n")
	now = now.Add(workspaceCacheTTL + time.Nanosecond)
	if _, err := svc.GetWorkspaceDiffs(t.Context(), "ao-1", input); err == nil {
		t.Fatal("expired manifest accepted stale workspace version")
	}
	svc.runBackground = func(func()) {}
	svc.InvalidateWorkspaceCache("ao-1")
	if _, err := svc.GetWorkspaceDiffs(t.Context(), "ao-1", input); err == nil {
		t.Fatal("invalidated manifest accepted stale workspace version")
	}
}
