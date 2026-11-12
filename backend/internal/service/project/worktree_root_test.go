package project_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// newManagerWithDataDir builds a Manager whose installation default worktree
// root is <dataDir>/worktrees, the way the daemon wires it.
func newManagerWithDataDir(t *testing.T, dataDir string) project.Manager {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return project.NewWithDeps(project.Deps{Store: store, DataDir: dataDir})
}

// TestAddProjectPersistsDefaultWorktreeRoot covers the registration contract:
// a new project without an explicit root records <AO_DATA_DIR>/worktrees, so a
// later AO_DATA_DIR change cannot move its worktrees.
func TestAddProjectPersistsDefaultWorktreeRoot(t *testing.T) {
	dataDir := t.TempDir()
	m := newManagerWithDataDir(t, dataDir)
	repo := gitRepo(t)

	added, err := m.Add(context.Background(), project.AddInput{Path: repo})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	wantRoot := filepath.Join(dataDir, "worktrees")
	if added.Config == nil || added.Config.WorktreeRoot != wantRoot {
		t.Fatalf("saved worktreeRoot = %+v, want %q", added.Config, wantRoot)
	}
	if added.WorktreeRoot != wantRoot {
		t.Fatalf("effective worktreeRoot = %q, want %q", added.WorktreeRoot, wantRoot)
	}
}

// TestProjectReadModelReportsEffectiveWorktreeRootWithoutClaimingItIsSaved
// covers the read-model rule for projects registered before the field existed:
// the API reports the root in effect, while the saved config stays empty.
func TestProjectReadModelReportsEffectiveWorktreeRootWithoutClaimingItIsSaved(t *testing.T) {
	dataDir := t.TempDir()
	ctx := context.Background()
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, DataDir: dataDir})

	repo := gitRepo(t)
	// A row as it exists for a project registered before worktreeRoot shipped.
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID:     "legacy",
		Path:   repo,
		Kind:   domain.ProjectKindSingleRepo,
		Config: domain.ProjectConfig{DefaultBranch: domain.DefaultBranchAuto},
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	got, err := m.Get(ctx, "legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantRoot := filepath.Join(dataDir, "worktrees")
	if got.Project.WorktreeRoot != wantRoot {
		t.Fatalf("effective worktreeRoot = %q, want %q", got.Project.WorktreeRoot, wantRoot)
	}
	if got.Project.Config == nil || got.Project.Config.WorktreeRoot != "" {
		t.Fatalf("saved config = %+v, want worktreeRoot unset", got.Project.Config)
	}
}

// TestAddProjectRejectsUnsafeWorktreeRoots covers the path rules that protect
// the filesystem: relativism, filesystem roots, non-empty folders, and a root
// inside the project's own repository are all refused at save time.
func TestAddProjectRejectsUnsafeWorktreeRoots(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	m := newManagerWithDataDir(t, dataDir)
	repo := gitRepo(t)

	t.Run("relative root", func(t *testing.T) {
		cfg := domain.ProjectConfig{WorktreeRoot: "relative/root"}
		_, err := m.Add(ctx, project.AddInput{Path: repo, Config: &cfg})
		// Shape rules live in domain.ProjectConfig.Validate, so a relative root
		// is refused there before the service ever touches the filesystem.
		wantCode(t, err, "INVALID_PROJECT_CONFIG")
		if !strings.Contains(err.Error(), "worktreeRoot") {
			t.Fatalf("error = %v, want it to name worktreeRoot", err)
		}
	})

	t.Run("filesystem root", func(t *testing.T) {
		cfg := domain.ProjectConfig{WorktreeRoot: string(filepath.Separator)}
		_, err := m.Add(ctx, project.AddInput{Path: repo, Config: &cfg})
		wantCode(t, err, "INVALID_PROJECT_CONFIG")
		if !strings.Contains(err.Error(), "worktreeRoot") {
			t.Fatalf("error = %v, want it to name worktreeRoot", err)
		}
	})

	t.Run("root inside the project repository", func(t *testing.T) {
		cfg := domain.ProjectConfig{WorktreeRoot: filepath.Join(repo, "worktrees")}
		_, err := m.Add(ctx, project.AddInput{Path: repo, Config: &cfg})
		wantCode(t, err, "WORKTREE_ROOT_INSIDE_REPO")
	})

	t.Run("existing non-empty folder", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := domain.ProjectConfig{WorktreeRoot: dir}
		_, err := m.Add(ctx, project.AddInput{Path: repo, Config: &cfg})
		wantCode(t, err, "WORKTREE_ROOT_NOT_EMPTY")
	})

	t.Run("missing parent", func(t *testing.T) {
		cfg := domain.ProjectConfig{WorktreeRoot: filepath.Join(t.TempDir(), "missing", "root")}
		_, err := m.Add(ctx, project.AddInput{Path: repo, Config: &cfg})
		wantCode(t, err, "WORKTREE_ROOT_PARENT_MISSING")
	})
}

// TestAddProjectCreatesWorktreeRootAndSupportsUnicode covers the supported
// cases: a not-yet-existing root is created beneath an existing parent, and a
// root with spaces and non-ASCII characters round-trips unchanged.
func TestAddProjectCreatesWorktreeRootAndSupportsUnicode(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	m := newManagerWithDataDir(t, dataDir)
	repo := gitRepo(t)

	parent := t.TempDir()
	root := filepath.Join(parent, "工作树 目录")
	cfg := domain.ProjectConfig{WorktreeRoot: root}
	added, err := m.Add(ctx, project.AddInput{Path: repo, Config: &cfg})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("worktree root not created: %v", err)
	}
	if added.Config == nil || added.Config.WorktreeRoot != root {
		t.Fatalf("saved worktreeRoot = %+v, want %q", added.Config, root)
	}
}

// TestWorktreeRootConflictsAcrossProjects covers the cross-project rule: two
// projects may not share a root or nest one inside the other.
func TestWorktreeRootConflictsAcrossProjects(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	m := newManagerWithDataDir(t, dataDir)

	parent := t.TempDir()
	shared := filepath.Join(parent, "shared-root")
	firstCfg := domain.ProjectConfig{WorktreeRoot: shared}
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), Config: &firstCfg}); err != nil {
		t.Fatalf("Add first project: %v", err)
	}

	t.Run("same root", func(t *testing.T) {
		cfg := domain.ProjectConfig{WorktreeRoot: shared}
		_, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), Config: &cfg})
		wantCode(t, err, "WORKTREE_ROOT_CONFLICT")
	})

	t.Run("nested root", func(t *testing.T) {
		cfg := domain.ProjectConfig{WorktreeRoot: filepath.Join(shared, "inner")}
		_, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), Config: &cfg})
		wantCode(t, err, "WORKTREE_ROOT_CONFLICT")
	})

	t.Run("parent of another root", func(t *testing.T) {
		other := filepath.Join(parent, "other-root")
		cfg := domain.ProjectConfig{WorktreeRoot: other}
		if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), Config: &cfg}); err != nil {
			t.Fatalf("Add other project: %v", err)
		}
		cfg = domain.ProjectConfig{WorktreeRoot: parent}
		_, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), Config: &cfg})
		wantCode(t, err, "WORKTREE_ROOT_CONFLICT")
	})
}

// TestSetConfigAcceptsDefaultWorktreeRootAfterProject covers re-saving the root
// every project shares: the installation default holds one directory per
// project, which must not be mistaken for an unrelated non-empty folder.
func TestSetConfigAcceptsDefaultWorktreeRootAfterProject(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	m := newManagerWithDataDir(t, dataDir)

	first, err := m.Add(ctx, project.AddInput{Path: gitRepo(t)})
	if err != nil {
		t.Fatalf("Add first: %v", err)
	}
	second, err := m.Add(ctx, project.AddInput{Path: gitRepo(t)})
	if err != nil {
		t.Fatalf("Add second: %v", err)
	}
	// The default root now contains one directory per project.
	wantRoot := filepath.Join(dataDir, "worktrees")
	cfg := domain.ProjectConfig{WorktreeRoot: wantRoot}
	if _, err := m.SetConfig(ctx, second.ID, project.SetConfigInput{Config: cfg}); err != nil {
		t.Fatalf("SetConfig default root: %v", err)
	}
	if _, err := m.SetConfig(ctx, first.ID, project.SetConfigInput{Config: cfg}); err != nil {
		t.Fatalf("SetConfig default root for second project: %v", err)
	}
}

// TestProjectBranchesListsLocalAndOrigin covers the picker's contract: local
// branches plus origin/<branch> rows, origin/HEAD excluded, and the configured
// default marked.
func TestProjectBranchesListsLocalAndOrigin(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepoOnBranch(t, "main")
	runGit(t, repo, "branch", "feature/with-slash")
	// Point origin at a bare clone of the same repository so remote-tracking
	// refs exist without touching the network.
	remote := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, filepath.Dir(remote), "clone", "--bare", repo, remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "fetch", "origin")
	runGit(t, repo, "remote", "set-head", "origin", "main")

	added, err := m.Add(ctx, project.AddInput{Path: repo})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	branches, err := m.Branches(ctx, added.ID)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	names := make(map[string]project.ProjectBranch, len(branches.Branches))
	for _, branch := range branches.Branches {
		names[branch.Name] = branch
	}
	for _, want := range []string{"main", "feature/with-slash", "origin/main", "origin/feature/with-slash"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("branches = %v, want %q listed", names, want)
		}
	}
	if _, ok := names["origin/HEAD"]; ok {
		t.Fatalf("origin/HEAD must be excluded: %v", names)
	}
	if !names["main"].Current || !names["main"].Local {
		t.Fatalf("main row = %+v, want current local branch", names["main"])
	}
	if names["origin/main"].Remote != "origin" {
		t.Fatalf("origin/main remote = %q, want origin", names["origin/main"].Remote)
	}
	if !names["main"].Default {
		t.Fatalf("default branch %q not marked: %v", branches.Default, names)
	}
	defaults := 0
	for _, branch := range branches.Branches {
		if branch.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("default rows = %d, want exactly the configured spelling: %v", defaults, names)
	}
}

// TestProjectBranchesReportsUnreadableRepository covers the error contract: a
// failure to read the repository is reported, not returned as an empty list.
func TestProjectBranchesReportsUnreadableRepository(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)
	added, err := m.Add(ctx, project.AddInput{Path: repo})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := m.Branches(ctx, added.ID); err != nil {
		t.Fatalf("Branches on a healthy repo: %v", err)
	}
	// Break the repository's git metadata so the branch read fails.
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	_, err = m.Branches(ctx, added.ID)
	if err == nil {
		t.Fatal("Branches on a broken repository = nil error, want a read failure")
	}
}

// TestScratchProjectSkipsWorktreeRoot covers the scope boundary: scratch
// projects report no worktree root and their config still refuses one.
func TestScratchProjectSkipsWorktreeRoot(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.NewWithDeps(project.Deps{Store: store, DataDir: dataDir})

	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID:   "scratch",
		Path: t.TempDir(),
		Kind: domain.ProjectKindScratch,
	}); err != nil {
		t.Fatalf("seed scratch: %v", err)
	}
	got, err := m.Get(ctx, "scratch")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Project.WorktreeRoot != "" {
		t.Fatalf("scratch worktreeRoot = %q, want empty", got.Project.WorktreeRoot)
	}
	branches, err := m.Branches(ctx, "scratch")
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches.Branches) != 0 {
		t.Fatalf("scratch branches = %v, want none", branches.Branches)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

// TestSetConfigWithoutWorktreeRootKeepsSavedRoot covers the older-client path: a
// read-modify-write that never knew the field must not erase a saved root, while
// an explicit empty value still clears it.
func TestSetConfigWithoutWorktreeRootKeepsSavedRoot(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)
	custom := filepath.Join(t.TempDir(), "custom-root")
	added, err := m.Add(ctx, project.AddInput{
		Path:   repo,
		Config: &domain.ProjectConfig{WorktreeRoot: custom},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// An old client's config has no worktreeRoot at all: WorktreeRootPresent
	// stays false and the saved root survives.
	if _, err := m.SetConfig(ctx, added.ID, project.SetConfigInput{
		Config: domain.ProjectConfig{SessionPrefix: "old-client"},
	}); err != nil {
		t.Fatalf("SetConfig from an old client: %v", err)
	}
	got, err := m.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Project.Config == nil || got.Project.Config.WorktreeRoot != custom {
		t.Fatalf("saved root = %+v, want %q preserved", got.Project.Config, custom)
	}
	if got.Project.Config.SessionPrefix != "old-client" {
		t.Fatalf("session prefix = %q, want the rest of the config applied", got.Project.Config.SessionPrefix)
	}

	// An explicit empty value is a real clear.
	if _, err := m.SetConfig(ctx, added.ID, project.SetConfigInput{
		Config:              domain.ProjectConfig{},
		WorktreeRootPresent: true,
	}); err != nil {
		t.Fatalf("SetConfig clearing the root: %v", err)
	}
	got, err = m.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if got.Project.Config != nil && got.Project.Config.WorktreeRoot != "" {
		t.Fatalf("saved root = %q, want cleared", got.Project.Config.WorktreeRoot)
	}
}

// TestUpdateSettingsWithoutWorktreeRootKeepsSavedRoot covers the same rule on the
// settings endpoint, which the settings form uses.
func TestUpdateSettingsWithoutWorktreeRootKeepsSavedRoot(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)
	custom := filepath.Join(t.TempDir(), "settings-root")
	added, err := m.Add(ctx, project.AddInput{
		Path:   repo,
		Config: &domain.ProjectConfig{WorktreeRoot: custom},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := m.UpdateSettings(ctx, added.ID, project.UpdateSettingsInput{
		DisplayName: "Renamed",
		Config:      domain.ProjectConfig{AutoReview: true},
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	got, err := m.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Project.Config == nil || got.Project.Config.WorktreeRoot != custom {
		t.Fatalf("saved root = %+v, want %q preserved", got.Project.Config, custom)
	}
	if got.Project.Name != "Renamed" || !got.Project.Config.AutoReview {
		t.Fatalf("rest of the settings update lost: %+v", got.Project)
	}
}

// TestWorktreeRootSymlinkSpellingConflicts covers M2: a junction/symlink spelling
// of another project's root must be recognised as the SAME root, because the two
// paths address one directory on disk.
func TestWorktreeRootSymlinkSpellingConflicts(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real-root")
	linkRoot := filepath.Join(parent, "linked-root")
	if err := os.MkdirAll(realRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	if _, err := m.Add(ctx, project.AddInput{
		Path:   gitRepo(t),
		Config: &domain.ProjectConfig{WorktreeRoot: realRoot},
	}); err != nil {
		t.Fatalf("Add first project: %v", err)
	}
	_, err := m.Add(ctx, project.AddInput{
		Path:   gitRepo(t),
		Config: &domain.ProjectConfig{WorktreeRoot: linkRoot},
	})
	wantCode(t, err, "WORKTREE_ROOT_CONFLICT")
}

// TestWorktreeRootCreationRollsBackOnValidationFailure covers M3: a root this call
// created is removed when the same call then rejects it, and a folder that already
// existed is never removed by a failed validation.
func TestWorktreeRootCreationRollsBackOnValidationFailure(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)

	// Created inside the project repository: creation happens first, then the
	// inside-repo rule rejects it, and the new folder must not survive.
	inside := filepath.Join(repo, "worktrees-root")
	_, err := m.Add(ctx, project.AddInput{
		Path:   repo,
		Config: &domain.ProjectConfig{WorktreeRoot: inside},
	})
	wantCode(t, err, "WORKTREE_ROOT_INSIDE_REPO")
	if _, statErr := os.Stat(inside); !os.IsNotExist(statErr) {
		t.Fatalf("rejected root survived at %s: %v", inside, statErr)
	}

	// A pre-existing empty folder rejected for another reason stays untouched.
	existing := filepath.Join(t.TempDir(), "existing-empty")
	if err := os.MkdirAll(existing, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(ctx, project.AddInput{
		Path:   gitRepo(t),
		Config: &domain.ProjectConfig{WorktreeRoot: existing},
	}); err != nil {
		t.Fatalf("Add with a pre-existing empty root: %v", err)
	}
	if info, statErr := os.Stat(existing); statErr != nil || !info.IsDir() {
		t.Fatalf("pre-existing root removed after success: %v", statErr)
	}
}

// TestWorktreeRootJunctionSpellingConflicts covers M2 on Windows: a directory
// junction needs no special privilege (unlike a symlink), and the resolver must
// still recognise the junction and its target as one directory. Resolving the
// child with EvalSymlinks alone fails on a junction path with "The system cannot
// find the file specified", which previously rejected a valid new root.
func TestWorktreeRootJunctionSpellingConflicts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junction spellings are a Windows filesystem feature")
	}
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real-root")
	if err := os.MkdirAll(realRoot, 0o750); err != nil {
		t.Fatal(err)
	}

	// A junction the first project owns, plus a sibling junction to the same
	// target used as the second project's spelling.
	firstLink := filepath.Join(parent, "first-junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", firstLink, realRoot).Run(); err != nil {
		t.Skipf("junctions unavailable on this host: %v", err)
	}
	if _, err := m.Add(ctx, project.AddInput{
		Path:   gitRepo(t),
		Config: &domain.ProjectConfig{WorktreeRoot: firstLink},
	}); err != nil {
		t.Fatalf("Add with a junction root: %v", err)
	}

	// The same directory addressed through another junction must conflict.
	secondLink := filepath.Join(parent, "second-junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", secondLink, realRoot).Run(); err != nil {
		t.Skipf("junctions unavailable on this host: %v", err)
	}
	_, err := m.Add(ctx, project.AddInput{
		Path:   gitRepo(t),
		Config: &domain.ProjectConfig{WorktreeRoot: secondLink},
	})
	wantCode(t, err, "WORKTREE_ROOT_CONFLICT")
}

// TestSetConfigWithoutRootKeepsStoredRootUnvalidated documents M1's current
// behaviour: a client that omits worktreeRoot inherits the stored root verbatim
// and no filesystem check runs against it. Re-validating an inherited value on
// every unrelated settings save would fail edits (a root on an unmounted volume
// blocks a display-name change) without improving safety, because spawn re-checks
// the root before creating anything.
func TestSetConfigWithoutRootKeepsStoredRootUnvalidated(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)
	custom := filepath.Join(t.TempDir(), "root-then-removed")
	added, err := m.Add(ctx, project.AddInput{
		Path:   repo,
		Config: &domain.ProjectConfig{WorktreeRoot: custom},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// The root disappears; an unrelated config save from an old client still
	// succeeds and keeps the value, and only the spawn refuses it later.
	if err := os.RemoveAll(custom); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetConfig(ctx, added.ID, project.SetConfigInput{
		Config: domain.ProjectConfig{SessionPrefix: "after-removal"},
	}); err != nil {
		t.Fatalf("SetConfig after the root disappeared: %v", err)
	}
	got, err := m.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Project.Config == nil || got.Project.Config.WorktreeRoot != custom {
		t.Fatalf("stored root = %+v, want %q kept", got.Project.Config, custom)
	}
}

// TestDefaultWorktreeRootThroughJunctionIsShared covers the case where AO_DATA_DIR
// itself is reached through a Windows junction. Two projects must both be able to
// use the default root (it is shared by design), and the comparison that decides
// "this is the shared default" has to resolve the junction the same way the root
// was resolved. Resolving only one side left the junction spelling looking like a
// project-specific root, which then collided with the other project's.
func TestDefaultWorktreeRootThroughJunctionIsShared(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows filesystem feature")
	}
	dataDirReal := t.TempDir()
	linkParent := t.TempDir()
	dataDirLink := filepath.Join(linkParent, "data-junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", dataDirLink, dataDirReal).Run(); err != nil {
		t.Skipf("junctions unavailable on this host: %v", err)
	}
	ctx := context.Background()
	m := newManagerWithDataDir(t, dataDirLink)
	defaultRoot := filepath.Join(dataDirLink, "worktrees")

	first, err := m.Add(ctx, project.AddInput{Path: gitRepo(t)})
	if err != nil {
		t.Fatalf("Add first project: %v", err)
	}
	second, err := m.Add(ctx, project.AddInput{Path: gitRepo(t)})
	if err != nil {
		t.Fatalf("Add second project under a junctioned data dir: %v", err)
	}
	for _, added := range []project.Project{first, second} {
		if added.Config == nil || added.Config.WorktreeRoot == "" {
			t.Fatalf("project %s has no saved root", added.ID)
		}
	}
	// The default root is shared, so both projects may name it explicitly too.
	cfg := domain.ProjectConfig{WorktreeRoot: defaultRoot}
	if _, err := m.SetConfig(ctx, second.ID, project.SetConfigInput{Config: cfg, WorktreeRootPresent: true}); err != nil {
		t.Fatalf("SetConfig default root through junction: %v", err)
	}
}

// TestWorktreeRootInsideRepoThroughJunction covers a project whose own path is
// reached through a junction: a root inside that repository must still be refused,
// which requires the repository path and the root to be resolved the same way.
func TestWorktreeRootInsideRepoThroughJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows filesystem feature")
	}
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repoReal := gitRepo(t)
	linkParent := t.TempDir()
	repoLink := filepath.Join(linkParent, "repo-junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", repoLink, repoReal).Run(); err != nil {
		t.Skipf("junctions unavailable on this host: %v", err)
	}

	// Root spelled through the real path, project registered through the junction.
	insideReal := filepath.Join(repoReal, "worktrees-root")
	_, err := m.Add(ctx, project.AddInput{
		Path:   repoLink,
		Config: &domain.ProjectConfig{WorktreeRoot: insideReal},
	})
	wantCode(t, err, "WORKTREE_ROOT_INSIDE_REPO")

	// And the mirrored spelling: root through the junction, project through the
	// real path.
	insideLink := filepath.Join(repoLink, "other-root")
	_, err = m.Add(ctx, project.AddInput{
		Path:   repoReal,
		Config: &domain.ProjectConfig{WorktreeRoot: insideLink},
	})
	wantCode(t, err, "WORKTREE_ROOT_INSIDE_REPO")
}

// TestRejectedRootRollbackNeverRemovesJunctionTarget covers M3's blast radius: the
// rollback removes only the empty directory this call created. A junction is a
// reparse point, and removing one can reach the directory it points at, so a save
// whose root is a junction must fail without deleting the junction's target.
func TestRejectedRootRollbackNeverRemovesJunctionTarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows filesystem feature")
	}
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)

	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "root-junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Skipf("junctions unavailable on this host: %v", err)
	}

	// A rejected save whose root lies inside the project repository: the folder is
	// created first, then the inside-repo rule rejects it, and the rollback must
	// remove only that empty directory -- never the unrelated junction target.
	insideRepo := filepath.Join(target, "nested")
	if err := os.MkdirAll(insideRepo, 0o750); err != nil {
		t.Fatal(err)
	}
	repoLink := filepath.Join(t.TempDir(), "repo-junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", repoLink, repo).Run(); err != nil {
		t.Skipf("junctions unavailable on this host: %v", err)
	}
	_, err := m.Add(ctx, project.AddInput{
		Path:   repoLink,
		Config: &domain.ProjectConfig{WorktreeRoot: filepath.Join(repo, "created-then-rejected")},
	})
	wantCode(t, err, "WORKTREE_ROOT_INSIDE_REPO")
	if _, statErr := os.Lstat(filepath.Join(repo, "created-then-rejected")); !os.IsNotExist(statErr) {
		t.Fatalf("created root survived rejection: %v", statErr)
	}
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Fatalf("junction removed by a rejected save: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(target, "keep.txt")); statErr != nil {
		t.Fatalf("junction target contents removed: %v", statErr)
	}
	if info, statErr := os.Stat(insideRepo); statErr != nil || !info.IsDir() {
		t.Fatalf("pre-existing directory removed: %v", statErr)
	}
}

// TestNewRootUnderJunctionedParentSucceeds covers the Medium finding: a parent
// reached through a Windows junction exists, so creating a not-yet-existing child
// root beneath it must succeed. Probing the parent with filepath.EvalSymlinks
// fails for junction sub-paths even though the folder is present, which used to
// report WORKTREE_ROOT_PARENT_MISSING for a perfectly valid folder.
func TestNewRootUnderJunctionedParentSucceeds(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows filesystem feature")
	}
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)

	realParent := t.TempDir()
	linkParent := filepath.Join(t.TempDir(), "parent-junction")
	if err := exec.Command("cmd", "/c", "mklink", "/J", linkParent, realParent).Run(); err != nil {
		t.Skipf("junctions unavailable on this host: %v", err)
	}

	// The child does not exist yet; its parent (the junction target) does.
	newRoot := filepath.Join(linkParent, "child-root")
	added, err := m.Add(ctx, project.AddInput{
		Path:   repo,
		Config: &domain.ProjectConfig{WorktreeRoot: newRoot},
	})
	if err != nil {
		t.Fatalf("Add with a new root under a junctioned parent: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realParent, "child-root")); statErr != nil {
		t.Fatalf("root was not created under the junction target: %v", statErr)
	}
	if added.Config == nil || added.Config.WorktreeRoot == "" {
		t.Fatalf("saved root is empty: %+v", added.Config)
	}
}

// TestNewRootUnderMissingParentStillRefused is the guard for the same check: a
// parent that genuinely does not exist is still reported, so the junction fix did
// not simply drop the rule.
func TestNewRootUnderMissingParentStillRefused(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithDataDir(t, t.TempDir())
	repo := gitRepo(t)
	_, err := m.Add(ctx, project.AddInput{
		Path:   repo,
		Config: &domain.ProjectConfig{WorktreeRoot: filepath.Join(t.TempDir(), "missing", "root")},
	})
	wantCode(t, err, "WORKTREE_ROOT_PARENT_MISSING")
}
