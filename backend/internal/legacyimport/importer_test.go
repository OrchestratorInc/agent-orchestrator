package legacyimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
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
