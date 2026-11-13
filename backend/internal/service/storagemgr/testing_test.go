package storagemgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeStore is an in-memory Store.
type fakeStore struct {
	mu       sync.Mutex
	settings domain.StorageSettings
	facts    map[domain.SessionID]domain.SessionStorageFacts
	actions  []domain.StorageAction
	ended    map[domain.SessionID]time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{settings: domain.DefaultStorageSettings(), facts: map[domain.SessionID]domain.SessionStorageFacts{}, ended: map[domain.SessionID]time.Time{}}
}

func (f *fakeStore) GetStorageSettings(context.Context) (domain.StorageSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *fakeStore) SaveStorageSettings(_ context.Context, s domain.StorageSettings, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s.UpdatedAt = now
	f.settings = s
	return nil
}

func (f *fakeStore) GetSessionStorageFacts(_ context.Context, id domain.SessionID) (domain.SessionStorageFacts, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	facts, ok := f.facts[id]
	return facts, ok, nil
}

func (f *fakeStore) ListSessionStorageFacts(context.Context) (map[domain.SessionID]domain.SessionStorageFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[domain.SessionID]domain.SessionStorageFacts{}
	for k, v := range f.facts {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) RecordSessionOffload(_ context.Context, facts domain.SessionStorageFacts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	prev := f.facts[facts.SessionID]
	facts.RestoredAt = prev.RestoredAt
	f.facts[facts.SessionID] = facts
	return nil
}

func (f *fakeStore) RecordSessionRestore(_ context.Context, id domain.SessionID, at time.Time, restoreErr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	facts := f.facts[id]
	if restoreErr == "" {
		facts.RestoredAt, facts.RestoreError = at, ""
	} else {
		facts.RestoreError = restoreErr
	}
	f.facts[id] = facts
	return nil
}

func (f *fakeStore) InsertStorageActions(_ context.Context, actions []domain.StorageAction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, actions...)
	return nil
}

func (f *fakeStore) ListStorageActions(_ context.Context, limit int) ([]domain.StorageAction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]domain.StorageAction(nil), f.actions...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) PruneStorageActionsBefore(context.Context, time.Time) (int64, error) { return 0, nil }

func (f *fakeStore) ListSessionEndTimes(context.Context) (map[domain.SessionID]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended, nil
}

func (f *fakeStore) DatabasePageStats(context.Context) (pageSize, pageCount, freePages int64, err error) {
	return 4096, 10, 0, nil
}

func (f *fakeStore) DatabaseTableSizes(context.Context, int) ([]domain.StorageTableSize, bool, error) {
	return nil, false, nil
}

func (f *fakeStore) IncrementalVacuum(context.Context, int64) error { return nil }

func (f *fakeStore) snapshotActions() []domain.StorageAction {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.StorageAction(nil), f.actions...)
}

type fakeSessions struct {
	sessions []domain.Session
	cleaned  []domain.SessionID
}

func (f *fakeSessions) ListAll(context.Context) ([]domain.Session, error) { return f.sessions, nil }

func (f *fakeSessions) CleanupSessions(_ context.Context, ids []domain.SessionID) ([]domain.SessionID, map[domain.SessionID]string, error) {
	f.cleaned = append(f.cleaned, ids...)
	return ids, nil, nil
}

type fakeProjects map[domain.ProjectID]domain.ProjectRecord

func (f fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	record, ok := f[domain.ProjectID(id)]
	return record, ok, nil
}

// passQuiet runs fn with a record whose workspace is path.
type passQuiet struct{ records map[domain.SessionID]domain.SessionRecord }

func (p passQuiet) WithQuietSession(_ context.Context, id domain.SessionID, fn func(domain.SessionRecord) error) error {
	return fn(p.records[id])
}

func newTestService(t *testing.T, store *fakeStore, sessions *fakeSessions, projects fakeProjects, quiet QuietGuard) (*Service, Layout) {
	t.Helper()
	root := t.TempDir()
	layout := Layout{StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "state", "data")}
	if err := os.MkdirAll(layout.Worktrees(), 0o750); err != nil {
		t.Fatal(err)
	}
	if sessions == nil {
		sessions = &fakeSessions{}
	}
	if projects == nil {
		projects = fakeProjects{}
	}
	if quiet == nil {
		quiet = passQuiet{}
	}
	svc := New(Options{Layout: layout, Store: store, Sessions: sessions, Quiet: quiet, Projects: projects, AutoCleanAllowed: true})
	return svc, layout
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newRepoWorktree builds a git repo at dir with tracked, untracked, ignored and
// protected content.
func newRepoWorktree(t *testing.T, dir string) {
	t.Helper()
	requireGit(t)
	writeFile(t, filepath.Join(dir, ".gitignore"), "node_modules/\ndist/\n.env\nout-custom/\n.ao/\n")
	writeFile(t, filepath.Join(dir, "build", "tracked.txt"), "tracked build file")
	writeFile(t, filepath.Join(dir, "packages", "x", "index.js"), "module.exports = 1")
	writeFile(t, filepath.Join(dir, "package-lock.json"), `{"lockfileVersion":3}`)
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "dependency code")
	writeFile(t, filepath.Join(dir, "packages", "x", "node_modules", "y", "index.js"), "nested dependency")
	writeFile(t, filepath.Join(dir, "dist", "bundle.js"), "built output")
	writeFile(t, filepath.Join(dir, "out-custom", "artifact.bin"), "custom build output")
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=1")
	writeFile(t, filepath.Join(dir, ".ao", "state.json"), "{}")
	writeFile(t, filepath.Join(dir, "notes", "todo.txt"), "untracked work")
}
