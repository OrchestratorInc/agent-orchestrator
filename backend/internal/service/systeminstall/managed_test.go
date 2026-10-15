package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// managedCodexService updates Codex through npm into a managed root. The fake
// npm install writes a codex binary into the --prefix directory, and that
// binary reports the version it was installed at.
// releaseStoreStub stands in for the database: the current release per
// harness and the releases live sessions run.
type releaseStoreStub struct {
	mu      sync.Mutex
	current map[domain.AgentHarness]string
	inUse   map[domain.AgentHarness][]string
	failUse bool
	failSet bool
}

func (r *releaseStoreStub) SetManagedHarnessRelease(_ context.Context, harness domain.AgentHarness, release string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSet {
		return errors.New("database unavailable")
	}
	if r.current == nil {
		r.current = map[domain.AgentHarness]string{}
	}
	r.current[harness] = release
	return nil
}

func (r *releaseStoreStub) ListHarnessReleasesInUse(_ context.Context, harness domain.AgentHarness) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failUse {
		return nil, errors.New("database unavailable")
	}
	return slices.Clone(r.inUse[harness]), nil
}

func (r *releaseStoreStub) PruneSessionHarnessReleases(context.Context) error { return nil }

func (r *releaseStoreStub) setInUse(harness domain.AgentHarness, releases ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inUse == nil {
		r.inUse = map[domain.AgentHarness][]string{}
	}
	r.inUse[harness] = releases
}

type managedCodexFixture struct {
	s           *Service
	releases    *releaseStoreStub
	root        string
	installs    []ports.InstallCommand
	invalidated []Target
	failInstall bool
	mu          sync.Mutex
}

func newManagedCodexFixture(t *testing.T, sessions []domain.SessionRecord) *managedCodexFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("managed launchers are symlinks on Unix; Windows uses a junction and a .cmd launcher")
	}
	f := &managedCodexFixture{root: t.TempDir(), releases: &releaseStoreStub{}}
	s := newTestService("darwin", "npm")
	s.managedRoot = f.root
	s.releases = f.releases
	s.sessions = sessionListerStub{sessions: sessions}
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	s.SetBinaryInvalidator(func(target Target) {
		f.mu.Lock()
		f.invalidated = append(f.invalidated, target)
		f.mu.Unlock()
	})
	// The harness resolver prefers the managed launcher once it exists.
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		launcher := filepath.Join(f.root, "bin", "codex")
		if target, err := filepath.EvalSymlinks(launcher); err == nil {
			version := strings.TrimSpace(readFile(target))
			return VerifyResult{ResolvedPath: launcher, Output: "codex " + version}, nil
		}
		return VerifyResult{ResolvedPath: "/usr/local/bin/codex", Output: "codex 1.0.0"}, nil
	})
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		f.mu.Lock()
		f.installs = append(f.installs, command)
		fail := f.failInstall
		f.mu.Unlock()
		if fail {
			return errors.New("npm failed")
		}
		prefix := command.Argv[slices.Index(command.Argv, "--prefix")+1]
		spec := command.Argv[len(command.Argv)-1]
		version := spec[strings.LastIndex(spec, "@")+1:]
		if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
			return err
		}
		// The fake binary's content is its version; the probe below reads it.
		return os.WriteFile(filepath.Join(prefix, "bin", "codex"), []byte(version+"\n"), 0o755)
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(stdout, "codex "+strings.TrimSpace(readFile(argv[0]))+"\n")
			return err
		}
		return nil
	})
	f.s = s
	return f
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func (f *managedCodexFixture) update(t *testing.T, latest string) Job {
	t.Helper()
	f.s.mu.Lock()
	f.s.updateAdvisories = map[Target]UpdateAdvisory{TargetCodex: {
		AgentID: string(TargetCodex), Status: UpdateStatusBehindLatest, LatestVersion: latest,
		MaintenanceMethod: "npm", CheckedAt: time.Now(),
	}}
	f.s.mu.Unlock()
	if _, err := f.s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	f.s.workers.Wait()
	job, _ := f.s.Status(context.Background(), TargetCodex)
	return job
}

func TestManagedUpdateInstallsSideBySideAndSwitches(t *testing.T) {
	live := []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}}
	f := newManagedCodexFixture(t, live)

	job := f.update(t, "1.1.0")
	if job.Status != StatusSucceeded {
		t.Fatalf("first managed update = %+v", job)
	}
	// Installed into its own directory, pinned to the release the user saw.
	if len(f.installs) != 1 || !slices.Contains(f.installs[0].Argv, "@openai/codex@1.1.0") ||
		!slices.Contains(f.installs[0].Argv, filepath.Join(f.root, "codex", "versions", "v1")) {
		t.Fatalf("install = %v", f.installs)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v1") {
		t.Fatalf("current = %q", target)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "bin", "codex")); target != filepath.Join("..", "codex", "current", "bin", "codex") {
		t.Fatalf("launcher = %q", target)
	}
	if record, ok := f.s.readManagedRecord(TargetCodex); !ok || record.Method != "npm" || record.Version != "1.1.0" || record.Dir != "v1" {
		t.Fatalf("record = %+v, %t", record, ok)
	}
	if !slices.Contains(f.invalidated, TargetCodex) {
		t.Fatal("the adapter was not told to resolve the managed launcher")
	}
	if f.releases.current[domain.HarnessCodex] != "v1" {
		t.Fatalf("current release recorded = %q, want v1", f.releases.current[domain.HarnessCodex])
	}

	// A second update gets a new directory; the first stays for the sessions
	// that still run it.
	if job = f.update(t, "1.2.0"); job.Status != StatusSucceeded {
		t.Fatalf("second managed update = %+v", job)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v2") {
		t.Fatalf("current after second update = %q", target)
	}
	if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", "v1", "bin", "codex")); err != nil {
		t.Fatalf("the previous release was removed while sessions may run it: %v", err)
	}
}

func TestManagedUpdateRunsAlongsideSessionsOnEveryPlatform(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		s := newTestService(goos, "npm")
		s.managedRoot = t.TempDir()
		s.installCommands = installCommandRunnerFunc(func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error { return nil })
		planner, err := s.newRequestPlanner(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, plan := range planner.agentMethodPlans(TargetCodex, AgentOperationUpdate) {
			if plan.Method != "npm" || plan.Unsupported {
				continue
			}
			if !plan.managed || plan.UpdateWhileRunning != UpdateWhileRunningSafe {
				t.Fatalf("%s npm update: managed=%t level=%s; want a safe managed update", goos, plan.managed, plan.UpdateWhileRunning)
			}
		}
	}
}

func TestFailedManagedInstallLeavesTheCurrentReleaseAlone(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	if job := f.update(t, "1.1.0"); job.Status != StatusSucceeded {
		t.Fatalf("first update = %+v", job)
	}
	f.failInstall = true
	if job := f.update(t, "1.2.0"); job.Status != StatusFailed {
		t.Fatalf("failed install = %+v", job)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v1") {
		t.Fatalf("current moved after a failed install: %q", target)
	}
	if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", "v2")); !os.IsNotExist(err) {
		t.Fatalf("the failed release directory was kept: %v", err)
	}
}

func TestCleanupKeepsTheReleaseCurrentPointsAtAfterAFailedRecord(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	if job := f.update(t, "1.1.0"); job.Status != StatusSucceeded {
		t.Fatalf("first update = %+v", job)
	}
	// The switch to v2 happens, then recording it fails: managed.json still
	// names v1 while current already points at v2.
	f.releases.failSet = true
	if job := f.update(t, "1.2.0"); job.Status != StatusFailed {
		t.Fatalf("update with a failed record = %+v", job)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v2") {
		t.Fatalf("current = %q, want the switched-to v2", target)
	}
	f.releases.failSet = false
	f.s.RemoveUnusedManagedReleases(context.Background())
	for _, name := range []string{"v1", "v2"} {
		if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", name)); err != nil {
			t.Fatalf("cleanup deleted %s, which managed.json or current names: %v", name, err)
		}
	}
}

func TestReleasesAreDeletedOnlyOnceNoLiveSessionRunsThem(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	f.update(t, "1.1.0")
	f.update(t, "1.2.0")
	f.update(t, "1.3.0")
	release := func(name string) bool {
		_, err := os.Stat(filepath.Join(f.root, "codex", "versions", name))
		return err == nil
	}

	// One session still runs v1 (for example a Chat that reconnected to its
	// old process after a daemon restart); v2 is unused.
	f.releases.setInUse(domain.HarnessCodex, "v1")
	f.s.RemoveUnusedManagedReleases(context.Background())
	if !release("v1") || release("v2") || !release("v3") {
		t.Fatalf("v1=%t v2=%t v3=%t; want only the unused v2 removed", release("v1"), release("v2"), release("v3"))
	}

	// If the database cannot say what is in use, nothing is deleted.
	f.releases.failUse = true
	f.releases.setInUse(domain.HarnessCodex)
	f.s.RemoveUnusedManagedReleases(context.Background())
	if !release("v1") {
		t.Fatal("deleted a release while sessions in use could not be read")
	}

	f.releases.failUse = false
	f.s.RemoveUnusedManagedReleases(context.Background())
	if release("v1") || !release("v3") {
		t.Fatalf("v1=%t v3=%t; want v1 removed once no session runs it and the current kept", release("v1"), release("v3"))
	}
}

func TestNoReleaseIsDeletedWithoutTheReleaseStore(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	f.update(t, "1.1.0")
	f.update(t, "1.2.0")
	f.s.releases = nil
	f.s.RemoveUnusedManagedReleases(context.Background())
	if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", "v1")); err != nil {
		t.Fatalf("deleted a release with no record of what sessions run: %v", err)
	}
}

func TestManagedCopyCountsAsOwnedByItsMethod(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	f.update(t, "1.1.0")
	launcher := filepath.Join(f.root, "bin", "codex")
	f.s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	if !f.s.methodOwnsBinary(context.Background(), Plan{Target: TargetCodex, Method: "npm", Package: "@openai/codex"}, launcher) {
		t.Fatal("npm does not own its own managed install")
	}
	if f.s.methodOwnsBinary(context.Background(), Plan{Target: TargetCodex, Method: "homebrew", Package: "codex"}, launcher) {
		t.Fatal("another method claimed the managed install")
	}
}

func TestReleasesAreKeptWhileAReviewerRunsTheHarness(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	f.update(t, "1.1.0")
	f.update(t, "1.2.0")
	// A reviewer is not a session, so no release row says which copy it runs.
	f.s.reviewers = reviewerListerStub{handles: []domain.ReviewerHandle{{SessionID: "worker-1", Harness: domain.ReviewerCodex, HandleID: "pane-1"}}}
	f.s.RemoveUnusedManagedReleases(context.Background())
	if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", "v1")); err != nil {
		t.Fatalf("deleted a release while a reviewer could run it: %v", err)
	}
	f.s.reviewers = nil
	f.s.RemoveUnusedManagedReleases(context.Background())
	if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", "v1")); !os.IsNotExist(err) {
		t.Fatalf("the unused release was kept once no reviewer ran: %v", err)
	}
}

func TestManagedReinstallRepairsTheManagedCopy(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	if job := f.update(t, "1.1.0"); job.Status != StatusSucceeded {
		t.Fatalf("update = %+v", job)
	}
	if _, err := f.s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationReinstall, ""); err != nil {
		t.Fatal(err)
	}
	f.s.workers.Wait()
	if job, _ := f.s.Status(context.Background(), TargetCodex); job.Status != StatusSucceeded {
		t.Fatalf("reinstall = %+v", job)
	}
	// The same release, installed fresh beside the copy sessions run.
	last := f.installs[len(f.installs)-1]
	if !slices.Contains(last.Argv, "@openai/codex@1.1.0") || !slices.Contains(last.Argv, filepath.Join(f.root, "codex", "versions", "v2")) {
		t.Fatalf("reinstall ran %v, want the managed release 1.1.0 into v2", last.Argv)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v2") {
		t.Fatalf("current after reinstall = %q", target)
	}
}
