package projectsnapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type fakeVMs struct {
	mu                sync.Mutex
	created           []sandbox.Spec
	deleted           []sandbox.ID
	commands          []string
	envs              []map[string]string
	snapshots         int
	deletedSnapshots  []string
	execErr           error
	createErr         error
	deleteSnapshotErr error
}

func (f *fakeVMs) Create(_ context.Context, spec sandbox.Spec) (sandbox.Environment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, spec)
	if f.createErr != nil {
		return sandbox.Environment{}, f.createErr
	}
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
	if f.deleteSnapshotErr != nil {
		return f.deleteSnapshotErr
	}
	f.deletedSnapshots = append(f.deletedSnapshots, id)
	return nil
}

// fakeStore models the snapshot table: idle holds the collectable records
// (idle past the cutoff, or retired by a rebuild), referenced the snapshots a
// live session's plan still names, and buildHeld a build another replica holds.
type fakeStore struct {
	mu         sync.Mutex
	existing   *domain.RepositorySandboxSnapshot
	replaced   []domain.RepositorySandboxSnapshot
	used       int
	idle       []string
	referenced map[string]bool
	cutoff     time.Time
	buildHeld  bool
	claims     int
	releases   int
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
		// Retired, never dropped: the collector deletes it when unreferenced.
		f.idle = append(f.idle, previous)
	}
	snapshot.CreatedAt = time.Now() // the store stamps now()
	f.replaced = append(f.replaced, snapshot)
	f.existing = &snapshot
	return previous, nil
}

func (f *fakeStore) CollectIdleRepositorySandboxSnapshots(
	ctx context.Context, _ string, cutoff time.Time,
	remove func(context.Context, string) error,
) ([]string, error) {
	f.mu.Lock()
	f.cutoff = cutoff
	candidates := append([]string(nil), f.idle...)
	f.mu.Unlock()
	var collected []string
	var failures []error
	for _, id := range candidates {
		f.mu.Lock()
		referenced := f.referenced[id]
		f.mu.Unlock()
		if referenced {
			continue
		}
		if err := remove(ctx, id); err != nil {
			failures = append(failures, err)
			continue // the record stays for the next run
		}
		f.mu.Lock()
		for i, idle := range f.idle {
			if idle == id {
				f.idle = append(f.idle[:i], f.idle[i+1:]...)
				break
			}
		}
		f.mu.Unlock()
		collected = append(collected, id)
	}
	return collected, errors.Join(failures...)
}

func (f *fakeStore) AcquireRepositorySnapshotBuild(context.Context, string, string, string, string) (func(), bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buildHeld {
		return nil, false, nil
	}
	f.claims++
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.releases++
	}, true, nil
}

type fakeGrants struct{ token string }

func (f fakeGrants) IssueCheckoutGrant(context.Context, string, string) (githubapp.CheckoutGrant, error) {
	return githubapp.CheckoutGrant{CloneURL: "https://github.com/acme/repo.git", Token: f.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f fakeGrants) IssueProjectCheckoutGrant(ctx context.Context, orgID, _ string) (githubapp.CheckoutGrant, error) {
	return f.IssueCheckoutGrant(ctx, orgID, "")
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

// A rebuild must not delete the snapshot a running session booted from: its
// recreate boots that same snapshot. It is deleted once nothing references it.
func TestEnsureKeepsAReplacedSnapshotALiveSessionReferences(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{
		existing:   recorded("sh-old", "sh-base", time.Now().Add(-48*time.Hour)),
		referenced: map[string]bool{"sh-old": true},
	}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.deletedSnapshots) != 0 {
		t.Fatalf("deleted %v while a live session references it", vms.deletedSnapshots)
	}
	store.mu.Lock()
	store.referenced = nil
	store.mu.Unlock()
	builder.Collect(context.Background())
	if len(vms.deletedSnapshots) != 1 || vms.deletedSnapshots[0] != "sh-old" {
		t.Fatalf("retired snapshot not collected once unreferenced: %v", vms.deletedSnapshots)
	}
}

// A provider delete that fails keeps the record, so the next run retries it
// rather than leaking the provider copy.
func TestCollectRetriesAFailedProviderDelete(t *testing.T) {
	vms := &fakeVMs{deleteSnapshotErr: errors.New("freestyle down")}
	store := &fakeStore{idle: []string{"sh-idle"}}
	builder := newTestBuilder(vms, store)
	builder.Collect(context.Background())
	if len(store.idle) != 1 {
		t.Fatalf("record dropped after a failed provider delete: %v", store.idle)
	}
	vms.deleteSnapshotErr = nil
	builder.Collect(context.Background())
	if len(vms.deletedSnapshots) != 1 || len(store.idle) != 0 {
		t.Fatalf("retry did not collect: deleted=%v idle=%v", vms.deletedSnapshots, store.idle)
	}
}

// A provider at its VM limit (builder VMs count against it) defers the build
// rather than failing loudly or recording anything.
func TestEnsureDefersABuildAtProviderCapacity(t *testing.T) {
	vms, store := &fakeVMs{createErr: fmt.Errorf("%w: LIMIT_EXCEEDED", sandbox.ErrAtCapacity)}, &fakeStore{}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)
	if len(store.replaced) != 0 || vms.snapshots != 0 {
		t.Fatal("recorded a snapshot from a build that never got a VM")
	}
	if store.releases != 1 {
		t.Fatalf("build claim not released: releases=%d", store.releases)
	}
}

// One replica runs at most MaxConcurrentBuilds builder VMs at once.
func TestEnsureCapsConcurrentBuilds(t *testing.T) {
	builder := newTestBuilder(&fakeVMs{}, &fakeStore{})
	builder.config.MaxConcurrentBuilds = 1
	builder.mu.Lock()
	builder.inflight["other/repo/codex"] = true
	builder.mu.Unlock()
	builder.Ensure(request())
	builder.mu.Lock()
	defer builder.mu.Unlock()
	if builder.inflight[request().key()] {
		t.Fatal("started a build past the concurrency cap")
	}
}

// Another control-plane replica already building the same snapshot wins; this
// one does not start a second builder VM.
func TestEnsureSkipsABuildAnotherReplicaHolds(t *testing.T) {
	vms, store := &fakeVMs{}, &fakeStore{buildHeld: true}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)
	if len(vms.created) != 0 {
		t.Fatalf("built while another replica held the claim: %+v", vms.created)
	}

	store.mu.Lock()
	store.buildHeld = false
	store.mu.Unlock()
	builder.Ensure(request())
	waitIdle(t, builder)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(vms.created) != 1 || store.claims != 1 || store.releases != 1 {
		t.Fatalf("created=%d claims=%d releases=%d, want one claimed and released build",
			len(vms.created), store.claims, store.releases)
	}
}

// projectGrants issues only project grants, so a session grant fails loudly.
type projectGrants struct{ projects *[]string }

func (projectGrants) IssueCheckoutGrant(context.Context, string, string) (githubapp.CheckoutGrant, error) {
	return githubapp.CheckoutGrant{}, errors.New("no session grant")
}

func (g projectGrants) IssueProjectCheckoutGrant(_ context.Context, _, projectID string) (githubapp.CheckoutGrant, error) {
	*g.projects = append(*g.projects, projectID)
	return githubapp.CheckoutGrant{CloneURL: "https://github.com/acme/repo.git", Token: "ghs_project", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestEnsureWithoutASessionClonesWithTheProjectsGrant(t *testing.T) {
	vms, store := &fakeVMs{}, &fakeStore{}
	var projects []string
	builder := New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: store,
		Grants: projectGrants{projects: &projects},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	connect := request()
	connect.SessionID = ""
	builder.Ensure(connect)
	waitIdle(t, builder)

	if len(projects) != 1 || projects[0] != connect.ProjectID || vms.envs[0]["AO_GIT_TOKEN"] != "ghs_project" {
		t.Fatalf("project grants = %v, env = %v", projects, vms.envs)
	}
	if len(store.replaced) != 1 {
		t.Fatalf("recorded %+v", store.replaced)
	}
}

// refreshStore lists recently used snapshots for Refresh.
type refreshStore struct {
	*fakeStore
	recent []postgres.RepositorySnapshotRefresh
}

func (f *refreshStore) RecentRepositorySandboxSnapshots(context.Context, string, time.Time) ([]postgres.RepositorySnapshotRefresh, error) {
	return f.recent, nil
}

func TestRefreshRebuildsSnapshotsLeftOnAnOlderHarnessSnapshot(t *testing.T) {
	stale := recorded("sh-old", "sh-before-deploy", time.Now())
	store := &refreshStore{fakeStore: &fakeStore{existing: stale}, recent: []postgres.RepositorySnapshotRefresh{{
		Snapshot: *stale, Project: project("11111111-2222-3333-4444-555555555555"),
	}}}
	store.recent[0].Project.OrgID = "org"
	vms := &fakeVMs{}
	var projects []string
	builder := New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: store,
		Grants:       projectGrants{projects: &projects},
		BaseSnapshot: func(string) string { return "sh-base" },
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	builder.Refresh(context.Background())
	waitIdle(t, builder)

	if len(vms.created) != 1 || vms.created[0].RootFS != "sh-base" {
		t.Fatalf("builder VM created from %+v, want the current harness snapshot", vms.created)
	}
	if len(store.replaced) != 1 || store.replaced[0].BaseSnapshotID != "sh-base" || len(projects) != 1 {
		t.Fatalf("recorded %+v with grants for %v", store.replaced, projects)
	}

	// A snapshot already on the current harness snapshot is left alone.
	store.fakeStore.existing = &store.replaced[0]
	store.recent[0].Snapshot = store.replaced[0]
	builder.Refresh(context.Background())
	waitIdle(t, builder)
	if len(vms.created) != 1 {
		t.Fatalf("rebuilt a current snapshot: %+v", vms.created)
	}
}

// warmingVMs returns the VMs booted to warm a snapshot, and how many VMs were
// deleted, once n warming VMs have been booted and deleted.
func warmingVMs(t *testing.T, vms *fakeVMs, n int) []sandbox.Spec {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		vms.mu.Lock()
		var warming []sandbox.Spec
		for _, spec := range vms.created {
			if spec.Labels["ao.purpose"] == "snapshot-warm" {
				warming = append(warming, spec)
			}
		}
		deleted := len(vms.deleted)
		vms.mu.Unlock()
		if len(warming) >= n && deleted >= len(vms.created) {
			return warming
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d warming VMs, booted and deleted", n)
	return nil
}

// A freshly built snapshot is booted once and read, then the VM is deleted,
// so the first real session does not pay the slow first boot.
func TestABuiltSnapshotIsWarmed(t *testing.T) {
	vms, store := &fakeVMs{}, &fakeStore{}
	builder := New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: store,
		Grants: fakeGrants{token: "ghs_secret"}, WarmSnapshots: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	builder.Ensure(request())
	waitIdle(t, builder)

	warming := warmingVMs(t, vms, 1)
	if len(warming) != 1 || warming[0].RootFS != "sh-new" {
		t.Fatalf("warming VMs = %+v, want one booted from the new snapshot", warming)
	}
	vms.mu.Lock()
	defer vms.mu.Unlock()
	last := vms.commands[len(vms.commands)-1]
	if !strings.Contains(last, "/usr/local/bin/ao-worker") || !strings.Contains(last, repositoryDir) {
		t.Fatalf("warm script %q must read the worker and the repository", last)
	}
}

// Each harness snapshot is warmed once per process, whatever the number of
// refresh passes; without WarmSnapshots nothing is warmed.
func TestWarmBasesOncePerSnapshot(t *testing.T) {
	vms := &fakeVMs{}
	builder := New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: &fakeStore{}, WarmSnapshots: true,
		BaseSnapshot:  func(harness string) string { return "sh-" + harness },
		BaseHarnesses: []string{"claude-code", "codex"},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	builder.warmBases(context.Background())
	builder.warmBases(context.Background())
	if warming := warmingVMs(t, vms, 2); len(warming) != 2 || warming[0].RootFS != "sh-claude-code" || warming[1].RootFS != "sh-codex" {
		t.Fatalf("warming VMs = %+v, want one per harness snapshot", warming)
	}

	cold := &fakeVMs{}
	New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: cold, Store: &fakeStore{},
		BaseSnapshot:  func(harness string) string { return "sh-" + harness },
		BaseHarnesses: []string{"claude-code"},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).warmBases(context.Background())
	if len(cold.created) != 0 {
		t.Fatalf("warmed without WarmSnapshots: %+v", cold.created)
	}
}

// The warm script reads through a pipe: GNU tar skips reading files when its
// archive is /dev/null, which would warm nothing.
func TestWarmScriptReadsThroughAPipe(t *testing.T) {
	if strings.Contains(warmScript, "-f /dev/null") || !strings.Contains(warmScript, "| cat >/dev/null") {
		t.Fatalf("warm script must stream tar through a pipe:\n%s", warmScript)
	}
}

// leftoverVMs is fakeVMs that can find VMs by name (as Freestyle finds one by
// slug) and fails the first deletes it is told to.
type leftoverVMs struct {
	fakeVMs
	leftovers   map[string]bool
	failDeletes int
}

func (f *leftoverVMs) Get(_ context.Context, id sandbox.ID) (sandbox.Environment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.leftovers[string(id)] {
		return sandbox.Environment{}, sandbox.ErrNotFound
	}
	return sandbox.Environment{ID: id}, nil
}

func (f *leftoverVMs) Delete(ctx context.Context, id sandbox.ID) error {
	f.mu.Lock()
	if f.failDeletes > 0 {
		f.failDeletes--
		f.mu.Unlock()
		return errors.New("provider unavailable")
	}
	delete(f.leftovers, string(id))
	f.mu.Unlock()
	return f.fakeVMs.Delete(ctx, id)
}

// keyedStore is fakeStore that lists the repository snapshots on record.
type keyedStore struct {
	*fakeStore
	keys []domain.RepositorySandboxSnapshot
}

func (f *keyedStore) RepositorySandboxSnapshotKeys(context.Context, string) ([]domain.RepositorySandboxSnapshot, error) {
	return f.keys, nil
}

// A builder VM holds a clone of a private repository, so a failed delete is
// retried, and one still left behind is found by its name and deleted before
// the next build of the same snapshot and by the collector's sweep.
func TestBuilderAndWarmingVMsAreNeverLeftBehind(t *testing.T) {
	previous := deleteRetryDelays
	deleteRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { deleteRetryDelays = previous })

	r := request()
	buildName, warmName := vmName("build", r.key()), vmName("warm", r.key())
	if !strings.HasPrefix(buildName, "ao-snapshot-build-") || len(buildName) > 63 || buildName == warmName {
		t.Fatalf("VM names %q and %q must be distinct, short, slug-safe", buildName, warmName)
	}

	// A delete that fails twice still succeeds on its last retry.
	vms := &leftoverVMs{leftovers: map[string]bool{}, failDeletes: 2}
	builder := New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: &fakeStore{},
		Grants: fakeGrants{token: "ghs_secret"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	builder.Ensure(r)
	waitIdle(t, builder)
	if len(vms.created) != 1 || vms.created[0].Name != buildName || len(vms.deleted) != 1 {
		t.Fatalf("created %+v, deleted %v: want the named builder VM deleted after retries", vms.created, vms.deleted)
	}

	// A builder VM an earlier build left behind is deleted before the next one.
	vms = &leftoverVMs{leftovers: map[string]bool{buildName: true}}
	builder = New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: &fakeStore{},
		Grants: fakeGrants{token: "ghs_secret"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	builder.Ensure(r)
	waitIdle(t, builder)
	if vms.leftovers[buildName] || len(vms.deleted) != 2 || vms.deleted[0] != sandbox.ID(buildName) {
		t.Fatalf("deleted %v: want the leftover builder VM deleted first", vms.deleted)
	}

	// The collector's sweep deletes leftovers for every snapshot on record and
	// the harnesses' warming VMs.
	baseWarm := vmName("warm", "base/claude-code")
	vms = &leftoverVMs{leftovers: map[string]bool{buildName: true, warmName: true, baseWarm: true}}
	store := &keyedStore{fakeStore: &fakeStore{}, keys: []domain.RepositorySandboxSnapshot{
		{OrgID: r.OrgID, RepositoryKey: r.RepositoryKey, Harness: r.Harness},
	}}
	New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: store, BaseHarnesses: []string{"claude-code"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Collect(context.Background())
	if len(vms.leftovers) != 0 {
		t.Fatalf("leftover VMs %v survived the sweep", vms.leftovers)
	}
}

// A warming VM of a repository snapshot is labelled with its organization and
// project, like the builder VM, since it holds a copy of the repository.
func TestARepositorySnapshotsWarmingVMIsLabelled(t *testing.T) {
	vms, store := &fakeVMs{}, &fakeStore{}
	builder := New(Config{
		Provider: sandbox.ProviderFreestyle, VMs: vms, Store: store,
		Grants: fakeGrants{token: "ghs_secret"}, WarmSnapshots: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	r := request()
	builder.Ensure(r)
	waitIdle(t, builder)
	warming := warmingVMs(t, vms, 1)
	if warming[0].Labels["ao.org_id"] != r.OrgID || warming[0].Labels["ao.project_id"] != r.ProjectID ||
		warming[0].Name != vmName("warm", r.key()) {
		t.Fatalf("warming VM = %+v, want it named after the snapshot and labelled with its org and project", warming[0])
	}
}
