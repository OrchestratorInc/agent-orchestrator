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
	existing *domain.ProjectSandboxSnapshot
	replaced []domain.ProjectSandboxSnapshot
}

func (f *fakeStore) ProjectSandboxSnapshot(context.Context, string, string, string, string) (domain.ProjectSandboxSnapshot, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.existing == nil {
		return domain.ProjectSandboxSnapshot{}, false, nil
	}
	return *f.existing, true, nil
}

func (f *fakeStore) ReplaceProjectSandboxSnapshot(_ context.Context, snapshot domain.ProjectSandboxSnapshot) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	previous := ""
	if f.existing != nil {
		previous = f.existing.SnapshotID
	}
	f.replaced = append(f.replaced, snapshot)
	f.existing = &snapshot
	return previous, nil
}

type fakeGrants struct{ token string }

func (f fakeGrants) IssueCheckoutGrant(context.Context, string, string) (githubapp.CheckoutGrant, error) {
	return githubapp.CheckoutGrant{CloneURL: "https://github.com/acme/repo.git", Token: f.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func request() Request {
	return Request{
		OrgID: "org", ProjectID: "11111111-2222-3333-4444-555555555555", SessionID: "session",
		Harness: "claude-code", BaseSnapshotID: "sh-base", RepositoryURL: "https://github.com/acme/repo",
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
	store := &fakeStore{existing: &domain.ProjectSandboxSnapshot{
		SnapshotID: "sh-old", BaseSnapshotID: "sh-base", CreatedAt: time.Now().Add(-48 * time.Hour),
	}}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.deletedSnapshots) != 1 || vms.deletedSnapshots[0] != "sh-old" {
		t.Fatalf("replaced snapshot not deleted: %v", vms.deletedSnapshots)
	}
}

func TestEnsureSkipsAFreshSnapshot(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{existing: &domain.ProjectSandboxSnapshot{
		SnapshotID: "sh-fresh", BaseSnapshotID: "sh-base", CreatedAt: time.Now(),
	}}
	builder := newTestBuilder(vms, store)
	builder.Ensure(request())
	waitIdle(t, builder)

	if len(vms.created) != 0 {
		t.Fatalf("rebuilt a fresh snapshot: %+v", vms.created)
	}
}

func TestEnsureRebuildsWhenTheHarnessSnapshotChanged(t *testing.T) {
	vms := &fakeVMs{}
	store := &fakeStore{existing: &domain.ProjectSandboxSnapshot{
		SnapshotID: "sh-fresh", BaseSnapshotID: "sh-older-base", CreatedAt: time.Now(),
	}}
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
	store := &fakeStore{existing: &domain.ProjectSandboxSnapshot{
		SnapshotID: "sh-project", BaseSnapshotID: "sh-base", CreatedAt: time.Now(),
	}}
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
