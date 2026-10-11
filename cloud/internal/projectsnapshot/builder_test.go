package projectsnapshot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type fakeVMs struct {
	mu               sync.Mutex
	created          []sandbox.Spec
	deleted          []sandbox.ID
	commands         []string
	envs             []map[string]string
	snapshots        int
	deletedSnapshots []string
	execErr          error
}

func (f *fakeVMs) Create(_ context.Context, spec sandbox.Spec) (sandbox.Environment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, spec)
	return sandbox.Environment{ID: "vm-builder"}, nil
}

func (f *fakeVMs) Delete(_ context.Context, id sandbox.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeVMs) ExecRoot(_ context.Context, _ sandbox.ID, command string, env map[string]string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	f.envs = append(f.envs, env)
	return "", f.execErr
}

func (f *fakeVMs) Snapshot(context.Context, sandbox.ID, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots++
	return "sh-new", nil
}

func (f *fakeVMs) DeleteSnapshot(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedSnapshots = append(f.deletedSnapshots, id)
	return nil
}

type fakeStore struct {
	mu       sync.Mutex
	existing *domain.RepositorySandboxSnapshot
	replaced []domain.RepositorySandboxSnapshot
	used     int
	idle     []string
	cutoff   time.Time
}

func (f *fakeStore) lookup(orgID, repositoryKey, harness string) (domain.RepositorySandboxSnapshot, bool) {
	if f.existing == nil || f.existing.OrgID != orgID ||
		f.existing.RepositoryKey != repositoryKey || f.existing.Harness != harness {
		return domain.RepositorySandboxSnapshot{}, false
	}
	return *f.existing, true
}

func (f *fakeStore) UseRepositorySandboxSnapshot(_ context.Context, orgID, repositoryKey, _, harness string) (domain.RepositorySandboxSnapshot, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot, ok := f.lookup(orgID, repositoryKey, harness)
	if ok {
		f.used++
	}
	return snapshot, ok, nil
}

func (f *fakeStore) RepositorySandboxSnapshot(_ context.Context, orgID, repositoryKey, _, harness string) (domain.RepositorySandboxSnapshot, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot, ok := f.lookup(orgID, repositoryKey, harness)
	return snapshot, ok, nil
}

func (f *fakeStore) ReplaceRepositorySandboxSnapshot(_ context.Context, snapshot domain.RepositorySandboxSnapshot) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	previous := ""
	if f.existing != nil {
		previous = f.existing.SnapshotID
	}
	snapshot.CreatedAt = time.Now() // the store stamps now()
	f.replaced = append(f.replaced, snapshot)
	f.existing = &snapshot
	return previous, nil
}

func (f *fakeStore) DeleteIdleRepositorySandboxSnapshots(_ context.Context, _ string, cutoff time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoff = cutoff
	idle := f.idle
	f.idle = nil
	return idle, nil
}

type fakeGrants struct{ token string }

func (f fakeGrants) IssueCheckoutGrant(context.Context, string, string) (githubapp.CheckoutGrant, error) {
	return githubapp.CheckoutGrant{CloneURL: "https://github.com/acme/repo.git", Token: f.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

var repositoryID = int64(4242)

func project(id string) domain.Project {
	return domain.Project{ID: id, RepositoryURL: "https://github.com/Acme/Repo.git", GitHubRepositoryID: &repositoryID}
}

func request() Request {
	r, ok := NewRequest("org", project("11111111-2222-3333-4444-555555555555"), "claude-code", "sh-base")
	if !ok {
		panic("request")
	}
	r.SessionID = "session"
	return r
}

// recorded is a snapshot already built for request().
func recorded(id, base string, createdAt time.Time) *domain.RepositorySandboxSnapshot {
	return &domain.RepositorySandboxSnapshot{
		OrgID: "org", RepositoryKey: "github:4242", RepositoryIdentity: "acme/repo",
		Harness: "claude-code", SnapshotID: id, BaseSnapshotID: base, CreatedAt: createdAt,
	}
}

func newTestBuilder(vms *fakeVMs, store *fakeStore) *Builder {
	return New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: store,
		Grants: fakeGrants{token: "ghs_secret"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// waitIdle waits for Ensure's background build to finish.
func waitIdle(t *testing.T, b *Builder) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		idle := len(b.inflight) == 0
		b.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("build did not finish")
}

func TestMarkerMatchesTheWorker(t *testing.T) {
	if MarkerFile != worker.ProjectSnapshotMarker {
		t.Fatalf("MarkerFile %q != worker.ProjectSnapshotMarker %q", MarkerFile, worker.ProjectSnapshotMarker)
	}
}

func TestEnsureBuildsAndRecordsASnapshot(t *testing.T) {
	vms, store := &fakeVMs{}, &fakeStore{}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.created) != 1 || vms.created[0].RootFS != "sh-base" {
		t.Fatalf("builder VM created from %+v, want the harness snapshot", vms.created)
	}
	if len(store.replaced) != 1 || store.replaced[0].SnapshotID != "sh-new" || store.replaced[0].BaseSnapshotID != "sh-base" {
		t.Fatalf("recorded %+v", store.replaced)
	}
	if len(vms.deleted) != 1 {
		t.Fatalf("builder VM not deleted: %v", vms.deleted)
	}
	// The token travels only in the command's environment, never its text.
	if strings.Contains(vms.commands[0], "ghs_secret") {
		t.Fatal("clone token embedded in the command")
	}
	if vms.envs[0]["AO_GIT_TOKEN"] != "ghs_secret" {
		t.Fatalf("token not passed through env: %v", vms.envs[0])
	}
	if !strings.Contains(vms.commands[0], "--filter=blob:none") || !strings.Contains(vms.commands[0], MarkerFile) {
		t.Fatal("clone script must partial-clone and leave the worker marker")
	}
}

func TestEnsureReplacesAStaleSnapshotAndDeletesTheOldOne(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{existing: recorded("sh-old", "sh-base", time.Now().Add(-48*time.Hour))}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.deletedSnapshots) != 1 || vms.deletedSnapshots[0] != "sh-old" {
		t.Fatalf("replaced snapshot not deleted: %v", vms.deletedSnapshots)
	}
}

func TestEnsureSkipsAFreshSnapshot(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{existing: recorded("sh-fresh", "sh-base", time.Now())}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.created) != 0 {
		t.Fatalf("rebuilt a fresh snapshot: %+v", vms.created)
	}
}

func TestEnsureRebuildsWhenTheHarnessSnapshotChanged(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{existing: recorded("sh-fresh", "sh-older-base", time.Now())}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.created) != 1 {
		t.Fatal("did not rebuild after the harness snapshot changed")
	}
}

func TestFailedBuildStillDeletesTheBuilderVM(t *testing.T) {
	vms, store := &fakeVMs{execErr: errors.New("clone failed")}, &fakeStore{}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.deleted) != 1 {
		t.Fatalf("builder VM leaked after a failed build: %v", vms.deleted)
	}
	if len(store.replaced) != 0 || vms.snapshots != 0 {
		t.Fatal("a failed clone must not produce or record a snapshot")
	}
}

func TestLookupOnlyReturnsASnapshotBuiltOnTheCurrentHarness(t *testing.T) {
	store := &fakeStore{existing: recorded("sh-project", "sh-base", time.Now())}
	builder := newTestBuilder(&fakeVMs{}, store)

	if id, ok := builder.Lookup(context.Background(), request()); !ok || id != "sh-project" {
		t.Fatalf("Lookup = %q, %v; want sh-project", id, ok)
	}
	changed := request()
	changed.BaseSnapshotID = "sh-newer-base"
	if _, ok := builder.Lookup(context.Background(), changed); ok {
		t.Fatal("used a project snapshot built on an outdated harness snapshot")
	}
}

func TestNewRequestKeysByRepositoryID(t *testing.T) {
	r := request()
	if r.RepositoryKey != "github:4242" || r.RepositoryIdentity != "acme/repo" {
		t.Fatalf("request = %+v", r)
	}
	anonymous := project("p")
	anonymous.GitHubRepositoryID = nil
	if r, ok := NewRequest("org", anonymous, "codex", "sh-base"); !ok || r.RepositoryKey != "url:acme/repo" {
		t.Fatalf("anonymous request = %+v, %v; want keyed by owner/name", r, ok)
	}
	notGitHub := project("p")
	notGitHub.RepositoryURL = "https://gitlab.com/acme/repo"
	if _, ok := NewRequest("org", notGitHub, "codex", "sh-base"); ok {
		t.Fatal("a non-GitHub repository got a snapshot request")
	}
}

// A project deleted and added again gets a new id but the same repository, so
// its first session boots warm from the snapshot the old project built.
func TestAReAddedProjectUsesTheRepositorysSnapshot(t *testing.T) {
	vms, store := &fakeVMs{}, &fakeStore{}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	readded, _ := NewRequest("org", project("99999999-8888-7777-6666-555555555555"), "claude-code", "sh-base")
	if id, ok := builder.Lookup(context.Background(), readded); !ok || id != "sh-new" {
		t.Fatalf("Lookup = %q, %v; want the snapshot built for the old project", id, ok)
	}
	builder.Ensure(readded)
	waitIdle(t, builder)
	if len(vms.created) != 1 {
		t.Fatalf("rebuilt for the re-added project: %d builds", len(vms.created))
	}
}

// The worker refuses a checkout whose origin is not the session's repository,
// so a snapshot cloned before a rename is rebuilt, never booted.
func TestARenamedRepositoryRebuildsInsteadOfBootingTheOldClone(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{existing: recorded("sh-old-name", "sh-base", time.Now())}
	builder := newTestBuilder(vms, store)
	renamed := project("p")
	renamed.RepositoryURL = "https://github.com/acme/new-name"
	r, _ := NewRequest("org", renamed, "claude-code", "sh-base")
	if _, ok := builder.Lookup(context.Background(), r); ok {
		t.Fatal("booted a snapshot cloned under the old repository name")
	}
	builder.Ensure(r)
	waitIdle(t, builder)
	if len(store.replaced) != 1 || store.replaced[0].RepositoryIdentity != "acme/new-name" {
		t.Fatalf("recorded %+v, want a rebuild under the new name", store.replaced)
	}
}

func TestCollectDeletesIdleSnapshotsAtTheProvider(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{idle: []string{"sh-idle-1", "sh-idle-2"}}
	builder := newTestBuilder(vms, store)
	now := time.Now()
	builder.now = func() time.Time { return now }
	builder.Collect(context.Background())
	if len(vms.deletedSnapshots) != 2 {
		t.Fatalf("deleted %v, want both idle snapshots", vms.deletedSnapshots)
	}
	if !store.cutoff.Equal(now.Add(-IdleRetention)) {
		t.Fatalf("cutoff = %v, want %v", store.cutoff, now.Add(-IdleRetention))
	}
}
