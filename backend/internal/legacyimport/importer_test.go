package legacyimport

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeStore is an in-memory Store with the importer's idempotency semantics.
type fakeStore struct {
	projects map[string]domain.ProjectRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{projects: map[string]domain.ProjectRecord{}}
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	r, ok := f.projects[id]
	return r, ok, nil
}

// FindProjectByPath mirrors the real query: an active project owning the path.
func (f *fakeStore) FindProjectByPath(_ context.Context, path string) (domain.ProjectRecord, bool, error) {
	if path == "" {
		return domain.ProjectRecord{}, false, nil
	}
	for _, r := range f.projects {
		if r.Path == path && r.ArchivedAt.IsZero() {
			return r, true, nil
		}
	}
	return domain.ProjectRecord{}, false, nil
}

// ListProjects mirrors the real query: active projects ordered by id.
func (f *fakeStore) ListProjects(_ context.Context) ([]domain.ProjectRecord, error) {
	out := make([]domain.ProjectRecord, 0, len(f.projects))
	for _, r := range f.projects {
		if r.ArchivedAt.IsZero() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) UpsertProject(_ context.Context, r domain.ProjectRecord) error {
	f.projects[r.ID] = r
	return nil
}

// writeLegacyRoot builds a minimal legacy store: two projects. Returns the
// legacy root.
func writeLegacyRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	mustMkdir(t, filepath.Join(root, "projects", "alpha", "sessions"))
	mustMkdir(t, filepath.Join(root, "projects", "beta", "sessions"))

	mustWrite(t, filepath.Join(root, "config.yaml"), `projects:
  alpha:
    path: /repos/alpha
    name: Alpha
    defaultBranch: develop
  beta:
    path: /repos/beta
`)
	return root
}

func runOpts(root string) Options {
	return Options{
		Root:          root,
		Now:           time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		RepoOriginURL: func(string) string { return "" },
	}
}

func TestRun_EndToEnd(t *testing.T) {
	root := writeLegacyRoot(t)
	store := newFakeStore()
	ctx := context.Background()

	rep, err := Run(ctx, store, runOpts(root))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.ProjectsImported != 2 {
		t.Fatalf("projectsImported = %d, want 2", rep.ProjectsImported)
	}
	// develop branch survives into the config blob.
	if store.projects["alpha"].Config.DefaultBranch != "develop" {
		t.Fatalf("alpha config = %+v", store.projects["alpha"].Config)
	}
}

func TestRun_Idempotent(t *testing.T) {
	root := writeLegacyRoot(t)
	store := newFakeStore()
	ctx := context.Background()
	if _, err := Run(ctx, store, runOpts(root)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rep, err := Run(ctx, store, runOpts(root))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if rep.ProjectsImported != 0 || rep.ProjectsSkipped != 2 {
		t.Fatalf("re-run projects: imported=%d skipped=%d, want 0/2", rep.ProjectsImported, rep.ProjectsSkipped)
	}
}

func TestRun_DryRunWritesNothing(t *testing.T) {
	root := writeLegacyRoot(t)
	store := newFakeStore()
	opts := runOpts(root)
	opts.DryRun = true
	rep, err := Run(context.Background(), store, opts)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if rep.ProjectsImported != 2 {
		t.Fatalf("dry-run plan = %+v", rep)
	}
	if len(store.projects) != 0 {
		t.Fatal("dry run must not write to the store")
	}
}

func TestRun_NoLegacyData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "empty")
	rep, err := Run(context.Background(), newFakeStore(), Options{Root: root})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.ProjectsImported != 0 || len(rep.Notes) == 0 {
		t.Fatalf("expected empty import with a note, got %+v", rep)
	}
}

func TestHasLegacyData(t *testing.T) {
	root := writeLegacyRoot(t)
	if !HasLegacyData(root) {
		t.Fatal("HasLegacyData = false, want true")
	}
	if HasLegacyData(filepath.Join(t.TempDir(), "nope")) {
		t.Fatal("HasLegacyData = true for missing root")
	}
}

// TestLegacyConfigError_SurfacesParseFailure covers issue #2186 Bug 2: a legacy
// config.yaml with a syntax error must be surfaced as a parse error, not
// swallowed as "no data". The tab-indented line below is a YAML syntax error
// (not a *yaml.TypeError, so it is not a partial decode), exactly the case
// HasLegacyData collapses to false today. HasLegacyData's bool contract for the
// migration-probe service layer must stay intact (still false on a broken store).
func TestLegacyConfigError_SurfacesParseFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "config.yaml"), "projects:\n\talpha:\n  path: /repos/alpha\n")

	err := LegacyConfigError(context.Background(), root)
	if err == nil {
		t.Fatal("LegacyConfigError = nil for a config.yaml with a syntax error")
	}
	if !strings.Contains(err.Error(), "parse legacy config.yaml") {
		t.Fatalf("LegacyConfigError = %q, want an error mentioning parse legacy config.yaml", err)
	}
	// The bool probe used by the migration UI must keep reporting "not available"
	// rather than erroring on a broken store.
	if HasLegacyData(root) {
		t.Fatal("HasLegacyData = true for a config.yaml that fails to parse")
	}
}

func TestLegacyConfigError_NilWhenAbsentOrEmpty(t *testing.T) {
	if LegacyConfigError(context.Background(), "") != nil {
		t.Fatal("LegacyConfigError(\"\") = non-nil, want nil")
	}
	if LegacyConfigError(context.Background(), filepath.Join(t.TempDir(), "nope")) != nil {
		t.Fatal("LegacyConfigError on a missing root = non-nil, want nil")
	}
}

// A legacy project whose path an existing active project already owns under a
// different id must be skipped, not written: two active projects on one path
// would bypass the daemon's PATH_ALREADY_REGISTERED contract, and a re-run must
// stay idempotent rather than overwriting the newer owner.
func TestRun_SkipsProjectWhosePathIsAlreadyRegisteredUnderAnotherID(t *testing.T) {
	root := writeLegacyRoot(t)
	store := newFakeStore()
	// The importer normalizes legacy paths, so the pre-existing owner must hold
	// the normalized form — the same one importProject looks up.
	alphaPath, err := filepath.Abs("/repos/alpha")
	if err != nil {
		t.Fatal(err)
	}
	store.projects["existing"] = domain.ProjectRecord{ID: "existing", Path: alphaPath}

	rep, err := Run(context.Background(), store, runOpts(root))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, imported := store.projects["alpha"]; imported {
		t.Fatal("alpha was written even though its path belongs to project existing")
	}
	if got := store.projects["existing"].ID; got != "existing" {
		t.Fatalf("existing project was replaced by %q", got)
	}
	if _, imported := store.projects["beta"]; !imported {
		t.Fatal("beta has a free path and must still import")
	}
	if rep.ProjectsImported != 1 || rep.ProjectsSkipped != 1 {
		t.Fatalf("projects: imported=%d skipped=%d, want 1/1", rep.ProjectsImported, rep.ProjectsSkipped)
	}
	if len(rep.Notes) == 0 {
		t.Fatal("path collision must be reported, not silently skipped")
	}
}

// An alias of an already-registered directory must be caught too: the textual
// lookup cannot see it, so the importer compares directory identity the way the
// daemon's registration path does. The stored path differs textually from the
// imported one but names the same directory, so this exercises the os.SameFile
// branch on every platform.
func TestRun_SkipsProjectWhosePathResolvesToARegisteredDirectory(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	mustMkdir(t, filepath.Join(root, "projects", "aliased", "sessions"))
	mustWrite(t, filepath.Join(root, "config.yaml"), "projects:\n  aliased:\n    path: "+filepath.ToSlash(realDir)+"\n")

	store := newFakeStore()
	// Trailing separator: not equal as text, the same directory on disk.
	store.projects["existing"] = domain.ProjectRecord{ID: "existing", Path: realDir + string(os.PathSeparator)}

	rep, err := Run(context.Background(), store, runOpts(root))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, imported := store.projects["aliased"]; imported {
		t.Fatal("aliased path was imported as a second project on the same directory")
	}
	if rep.ProjectsSkipped != 1 || rep.ProjectsImported != 0 {
		t.Fatalf("projects: imported=%d skipped=%d, want 0/1", rep.ProjectsImported, rep.ProjectsSkipped)
	}
	if len(rep.Notes) == 0 {
		t.Fatal("alias collision must be reported")
	}
}

// The realistic alias is a symlink; it needs privileges Windows may not grant,
// so that host skips rather than silently passing.
func TestRun_SkipsProjectWhosePathSymlinksToARegisteredDirectory(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "real-repo")
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias-repo")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	mustMkdir(t, filepath.Join(root, "projects", "aliased", "sessions"))
	mustWrite(t, filepath.Join(root, "config.yaml"), "projects:\n  aliased:\n    path: "+filepath.ToSlash(alias)+"\n")

	store := newFakeStore()
	store.projects["existing"] = domain.ProjectRecord{ID: "existing", Path: realDir}

	rep, err := Run(context.Background(), store, runOpts(root))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, imported := store.projects["aliased"]; imported {
		t.Fatal("symlinked path was imported as a second project on the same directory")
	}
	if rep.ProjectsSkipped != 1 || rep.ProjectsImported != 0 {
		t.Fatalf("projects: imported=%d skipped=%d, want 0/1", rep.ProjectsImported, rep.ProjectsSkipped)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
