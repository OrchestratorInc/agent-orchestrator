package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
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
		if len(argv) > 0 && argv[0] == "ps" {
			_, err := io.WriteString(stdout, "1 /usr/local/bin/ao\n")
			return err
		}
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

func TestManagedAdvisoryAgreesWithAcceptedUpdate(t *testing.T) {
	f := newManagedCodexFixture(t, []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}})
	f.s.updateChecksDisabled = false
	f.s.managedVersion = func(context.Context, Plan) (string, error) { return "1.1.0", nil }
	advisory, err := f.s.UpdateAdvisory(context.Background(), TargetCodex, true)
	if err != nil || advisory.Status != UpdateStatusBehindLatest || advisory.MaintenanceMethod != "npm" ||
		advisory.InUse == nil || advisory.InUse.Level != UpdateWhileRunningSafe {
		t.Fatalf("ownership-checked advisory = %+v, %v", advisory, err)
	}
	if _, err := f.s.StartAgentOperation(context.Background(), TargetCodex, advisory.MaintenanceMethod, AgentOperationUpdate, ""); err != nil {
		t.Fatalf("advertised safe update was refused: %v", err)
	}
	f.s.workers.Wait()
	if job, _ := f.s.Status(context.Background(), TargetCodex); job.Status != StatusSucceeded {
		t.Fatalf("managed update = %+v", job)
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

func TestManagedUpdateFailureKeepsPathsOutOfTheNotice(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	// npm exits 0 without writing the executable.
	f.s.installCommands = installCommandRunnerFunc(func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error { return nil })
	job := f.update(t, "1.1.0")
	if job.Status != StatusFailed || job.Error != "Installed, but codex was not found." {
		t.Fatalf("job = %+v", job)
	}
	if missing := filepath.Join(f.root, "codex", "versions", "v1", "bin", "codex"); !strings.Contains(job.Output, missing+" is missing") {
		t.Fatalf("diagnostics lack the missing path:\n%s", job.Output)
	}
}

func TestManagedInstallCommandFailureIsSummarized(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	f.failInstall = true
	job := f.update(t, "1.1.0")
	if job.Status != StatusFailed || job.Error != "The install command failed." || !strings.Contains(job.Output, "npm failed") {
		t.Fatalf("job = %+v", job)
	}
}

func TestManagedProbeWithoutAVersionIsNotACrash(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	commands := f.s.commands
	f.s.commands = commandRunnerFunc(func(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(stdout, "usage: codex [options]\n")
			return err
		}
		return commands.Run(ctx, argv, stdout, stderr)
	})
	job := f.update(t, "1.1.0")
	if job.Status != StatusFailed || job.Error != "The new codex did not report its version." || !strings.Contains(job.Output, "usage: codex") {
		t.Fatalf("job = %+v", job)
	}
}

func TestSetupFailureAfterTheSwitchSaysTheSwitchHappened(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	// A directory where managed.json goes makes its write fail after current
	// has moved to the new release.
	if err := os.MkdirAll(filepath.Join(f.root, "codex", "managed.json", "blocker"), 0o755); err != nil {
		t.Fatal(err)
	}
	job := f.update(t, "1.1.0")
	if job.Status != StatusFailed || job.Error != setupFailed {
		t.Fatalf("job = %+v", job)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v1") {
		t.Fatalf("current = %q, want the new release", target)
	}
}

func TestFailedRecordSwitchesBackToThePreviousRelease(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	if job := f.update(t, "1.1.0"); job.Status != StatusSucceeded {
		t.Fatalf("first update = %+v", job)
	}
	// The switch to v2 happens, then recording it fails: launches would run
	// v2 while recording v1, so the update switches back.
	f.releases.failSet = true
	if job := f.update(t, "1.2.0"); job.Status != StatusFailed {
		t.Fatalf("update with a failed record = %+v", job)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v1") {
		t.Fatalf("current = %q, want v1 back", target)
	}
	if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", "v2")); !os.IsNotExist(err) {
		t.Fatalf("the unrecorded release was kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "codex", unsettledMarker)); !os.IsNotExist(err) {
		t.Fatalf("a switch that was undone left the target unsettled: %v", err)
	}
}

func TestUnsettledSwitchKeepsEveryReleaseWhileSessionsRun(t *testing.T) {
	live := []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}}
	f := newManagedCodexFixture(t, live)
	if job := f.update(t, "1.1.0"); job.Status != StatusSucceeded {
		t.Fatalf("first update = %+v", job)
	}
	// Recording the switch to v2 fails, and so does switching back (a
	// directory blocks the temporary link the switch needs).
	if err := os.MkdirAll(filepath.Join(f.root, "codex", ".current-v1", "blocked"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.releases.failSet = true
	if job := f.update(t, "1.2.0"); job.Status != StatusFailed {
		t.Fatalf("update with a failed record and rollback = %+v", job)
	}
	if target, _ := os.Readlink(filepath.Join(f.root, "codex", "current")); target != filepath.Join("versions", "v2") {
		t.Fatalf("current = %q, want v2 after the failed rollback", target)
	}
	if _, err := os.Stat(filepath.Join(f.root, "codex", unsettledMarker)); err != nil {
		t.Fatalf("the unsettled switch was not marked: %v", err)
	}

	// A session launched meanwhile runs v2 but recorded v1. A later update
	// succeeds, and cleanup must still keep v2 for it.
	f.releases.failSet = false
	if job := f.update(t, "1.3.0"); job.Status != StatusSucceeded {
		t.Fatalf("later update = %+v", job)
	}
	f.releases.setInUse(domain.HarnessCodex, "v1")
	f.s.RemoveUnusedManagedReleases(context.Background())
	for _, name := range []string{"v1", "v2", "v3"} {
		if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", name)); err != nil {
			t.Fatalf("cleanup deleted %s while a session of the unsettled target ran: %v", name, err)
		}
	}

	// Once no session is left, the old releases go and the target settles.
	f.s.sessions = sessionListerStub{}
	f.releases.setInUse(domain.HarnessCodex)
	f.s.RemoveUnusedManagedReleases(context.Background())
	for name, want := range map[string]bool{"v1": false, "v2": false, "v3": true} {
		if _, err := os.Stat(filepath.Join(f.root, "codex", "versions", name)); (err == nil) != want {
			t.Fatalf("%s present = %t, want %t", name, err == nil, want)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "codex", unsettledMarker)); !os.IsNotExist(err) {
		t.Fatalf("the target stayed unsettled with no session left: %v", err)
	}
}

// A daemon that dies after switching current and before recording the
// switch leaves its mark; startup records the release current points at, and
// cleanup keeps every release while a session that launched may still run it.
func TestStartupReconcilesASwitchTheDaemonDiedIn(t *testing.T) {
	live := []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}}
	f := newManagedCodexFixture(t, live)
	if job := f.update(t, "1.1.0"); job.Status != StatusSucceeded {
		t.Fatalf("first update = %+v", job)
	}
	targetDir := filepath.Join(f.root, "codex")
	if _, err := os.Stat(filepath.Join(targetDir, unsettledMarker)); !os.IsNotExist(err) {
		t.Fatalf("a recorded switch stayed marked: %v", err)
	}
	// The crash: v2 installed, marked, current switched, nothing recorded.
	if err := os.CopyFS(filepath.Join(targetDir, "versions", "v2"), os.DirFS(filepath.Join(targetDir, "versions", "v1"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, unsettledMarker), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.s.switchManagedCurrent(context.Background(), targetDir, "v2"); err != nil {
		t.Fatal(err)
	}

	if err := f.s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.releases.mu.Lock()
	recorded := f.releases.current[domain.HarnessCodex]
	f.releases.mu.Unlock()
	if recorded != "v2" {
		t.Fatalf("release new launches record after startup = %q, want v2 (what current runs)", recorded)
	}

	// A launch before the crash recorded v1; one after startup records v2.
	// The next update succeeds and its cleanup keeps both while they run.
	f.releases.setInUse(domain.HarnessCodex, "v1", "v2")
	if job := f.update(t, "1.3.0"); job.Status != StatusSucceeded {
		t.Fatalf("next update = %+v", job)
	}
	f.s.RemoveUnusedManagedReleases(context.Background())
	for _, name := range []string{"v1", "v2", "v3"} {
		if _, err := os.Stat(filepath.Join(targetDir, "versions", name)); err != nil {
			t.Fatalf("cleanup deleted %s after an interrupted switch while a session ran: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(targetDir, unsettledMarker)); err != nil {
		t.Fatalf("the interrupted switch lost its mark on the next update: %v", err)
	}
}

func TestFailedWindowsSwitchRestoresThePreviousJunction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture stands in for the junction with a symlink")
	}
	for _, tt := range []struct {
		name   string
		cancel bool
	}{{name: "mklink fails"}, {name: "update cancelled", cancel: true}} {
		t.Run(tt.name, func(t *testing.T) {
			targetDir := t.TempDir()
			previous := filepath.Join(targetDir, "versions", "v1")
			if err := os.MkdirAll(previous, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(previous, filepath.Join(targetDir, "current")); err != nil {
				t.Fatal(err)
			}
			var links []string
			s := newTestService("windows")
			s.commands = commandRunnerFunc(func(ctx context.Context, argv []string, _, _ io.Writer) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				links = append(links, argv[len(argv)-1])
				if strings.HasSuffix(argv[len(argv)-1], "v2") {
					return errors.New("mklink failed")
				}
				return nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			if err := s.switchManagedCurrent(ctx, targetDir, "v2"); err == nil {
				t.Fatal("a failed switch reported success")
			}
			if len(links) == 0 || links[len(links)-1] != previous {
				t.Fatalf("junctions created = %v, want the last to restore %s", links, previous)
			}
		})
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

func TestManagedBunInstallOverridesTheUsersGlobalDirectories(t *testing.T) {
	dir := filepath.Join("root", "versions", "v1")
	_, env := managedInstallCommand("bun", "@oh-my-pi/pi-coding-agent", "1.2.0", dir)
	for _, want := range []string{
		"BUN_INSTALL=" + dir,
		"BUN_INSTALL_GLOBAL_DIR=" + filepath.Join(dir, "install", "global"),
		"BUN_INSTALL_BIN=" + filepath.Join(dir, "bin"),
	} {
		if !slices.Contains(env, want) {
			t.Fatalf("env = %v, missing %s", env, want)
		}
	}
}

func TestNoManagedNPMCopyWhenTheDaemonNodeIsTooOld(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.managedRoot = t.TempDir()
	s.installCommands = installCommandRunnerFunc(func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error { return nil })
	plan := Plan{Target: TargetPi, Method: "npm", Package: "@earendil-works/pi-coding-agent"}
	if !s.managedEligible(plan) {
		t.Fatal("npm plan not eligible for a managed copy")
	}
	plan.daemonRuntimeRejected = true
	if s.managedEligible(plan) {
		t.Fatal("a managed copy would install with a Node that is too old")
	}
}

func TestStartupRepairsMissingWindowsJunction(t *testing.T) {
	f := newManagedCodexFixture(t, []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}})
	f.update(t, "1.1.0")
	targetDir := filepath.Join(f.root, "codex")
	if err := os.CopyFS(filepath.Join(targetDir, "versions", "v2"), os.DirFS(filepath.Join(targetDir, "versions", "v1"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, unsettledMarker), []byte("v2"), 0600); err != nil {
		t.Fatal(err)
	}
	// Kill a real child at mklink entry, after switchManagedCurrent physically
	// removed the prior junction. Recovery must repair the actual crash gap.
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsJunctionCrashHelper$")
	child.Env = append(os.Environ(), "AO_TEST_JUNCTION_CRASH_DIR="+targetDir)
	if err := child.Run(); err == nil {
		t.Fatal("updater did not crash")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 77 {
			t.Fatalf("unexpected child exit: %v", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(targetDir, "current")); !os.IsNotExist(err) {
		t.Fatalf("crash did not remove current: %v", err)
	}
	f.s.goos = "windows"
	f.s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
		if len(argv) != 6 || argv[2] != "mklink" {
			return errors.New("unexpected junction command")
		}
		return os.Symlink(argv[5], argv[4])
	})
	if err := f.s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.s.currentManagedRelease(targetDir); got != "v2" {
		t.Fatalf("recovered current = %q, want v2", got)
	}
	if got := f.releases.current[domain.HarnessCodex]; got != "v2" {
		t.Fatalf("recorded release = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.root, "bin", "codex")); err != nil {
		t.Fatalf("stable launcher still broken: %v", err)
	}
	f.releases.setInUse(domain.HarnessCodex, "v1")
	f.s.RemoveUnusedManagedReleases(context.Background())
	if _, err := os.Stat(filepath.Join(targetDir, "versions", "v1")); err != nil {
		t.Fatal("old process release removed")
	}
}

func TestStartupReportsUnrepairableManagedJunction(t *testing.T) {
	f := newManagedCodexFixture(t, nil)
	f.update(t, "1.1.0")
	targetDir := filepath.Join(f.root, "codex")
	if err := os.WriteFile(filepath.Join(targetDir, unsettledMarker), []byte("v2"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(targetDir, "current")); err != nil {
		t.Fatal(err)
	}
	f.s.goos = "windows"
	f.s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		return errors.New("junction permission denied")
	})
	if err := f.s.Recover(context.Background()); err == nil {
		t.Fatal("startup silently accepted broken managed launcher")
	}
}

func TestFailedWindowsSwitchAndRollbackPreserveRecoveryIntent(t *testing.T) {
	s := newTestService("windows")
	s.managedRoot = t.TempDir()
	s.releases = &releaseStoreStub{}
	targetDir := s.managedTargetDir(TargetCodex)
	record := managedRecord{Method: "npm", Package: "@openai/codex", CLI: "codex", Version: "1.1.0", Dir: "v1", BinRel: "codex.cmd"}
	oldBinary := filepath.Join(targetDir, "versions", record.Dir, record.BinRel)
	if err := os.MkdirAll(filepath.Dir(oldBinary), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldBinary, []byte("old release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(targetDir, "managed.json"), mustJSON(record)); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureManagedLauncher(TargetCodex, record); err != nil {
		t.Fatal(err)
	}
	failLinks := 0
	s.commands = commandRunnerFunc(func(ctx context.Context, argv []string, out, stderr io.Writer) error {
		if len(argv) == 2 && argv[1] == "--version" {
			_, err := io.WriteString(out, "codex 1.2.0\n")
			return err
		}
		if len(argv) != 6 || argv[2] != "mklink" {
			return errors.New("unexpected command")
		}
		if failLinks > 0 {
			failLinks--
			return errors.New("junction creation unavailable")
		}
		if runtime.GOOS == "windows" {
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Stdout, cmd.Stderr = out, stderr
			return cmd.Run()
		}
		return os.Symlink(argv[5], argv[4])
	})
	if err := s.switchManagedCurrent(context.Background(), targetDir, record.Dir); err != nil {
		t.Fatal(err)
	}
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		prefix := command.Argv[slices.Index(command.Argv, "--prefix")+1]
		return os.WriteFile(filepath.Join(prefix, record.BinRel), []byte("new release"), 0o600)
	})
	failLinks = 2
	plan := Plan{Target: TargetCodex, Method: "npm", Package: record.Package}
	baseline := &installedBaseline{path: "codex.cmd", expected: "1.2.0"}
	if err := s.runManagedInstall(context.Background(), plan, baseline, nil, io.Discard); err == nil {
		t.Fatal("failed replacement and rollback reported success")
	}
	if _, err := os.Lstat(filepath.Join(targetDir, "current")); !os.IsNotExist(err) {
		t.Fatalf("fixture must leave current absent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, unsettledMarker)); err != nil {
		t.Fatalf("failed rollback discarded recovery intent: %v", err)
	}
	recovered := NewWithDeps(s.executables, s.commands, Deps{ManagedRoot: s.managedRoot, Releases: s.releases})
	recovered.goos = "windows"
	t.Cleanup(func() { _ = recovered.Close(context.Background()) })
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := recovered.currentManagedRelease(targetDir); got != "v2" {
		t.Fatalf("recovered current = %q, want preserved v2", got)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "current", record.BinRel)); err != nil {
		t.Fatalf("recovered launcher target is broken: %v", err)
	}
}

func TestWindowsJunctionCrashHelper(t *testing.T) {
	targetDir := os.Getenv("AO_TEST_JUNCTION_CRASH_DIR")
	if targetDir == "" {
		t.Skip("isolated crash subprocess")
	}
	s := newTestService("windows")
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { os.Exit(77); return nil })
	if err := s.switchManagedCurrent(context.Background(), targetDir, "v2"); err != nil {
		t.Fatal(err)
	}
	t.Fatal("mklink boundary not reached")
}

// newManagedNPMExecService runs managed npm installs through install, which
// writes the release under the --prefix it gets, and runs real version probes.
func newManagedNPMExecService(t *testing.T, install func(prefix string) error) *Service {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX release fixtures")
	}
	s := newTestService("darwin", "npm")
	s.managedRoot = t.TempDir()
	s.releases = &releaseStoreStub{}
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		return install(command.Argv[slices.Index(command.Argv, "--prefix")+1])
	})
	s.commands = commandRunnerFunc(func(ctx context.Context, argv []string, out, stderr io.Writer) error {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdout = out
		cmd.Stderr = stderr
		return cmd.Run()
	})
	return s
}

func writeVersionScript(path, version string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("#!/bin/sh\necho "+version+"\n"), 0755)
}

func TestManagedOpenCodeMajorsKeepSeparateLaunchers(t *testing.T) {
	version := "1.2.27"
	s := newManagedNPMExecService(t, func(prefix string) error {
		return writeVersionScript(filepath.Join(prefix, "bin", "opencode"), version)
	})
	for _, step := range []struct {
		target            Target
		version, baseline string
	}{{TargetOpencode, "1.2.27", "/usr/local/bin/opencode"}, {TargetOpencodeV2, "2.0.0", "/usr/local/bin/opencode"}, {TargetOpencodeV2, "2.0.1", filepath.Join(s.ManagedBinDir(), "opencode-v2")}} {
		version = step.version
		plan := Plan{Target: step.target, Method: "npm", Package: "opencode-ai"}
		if step.target == TargetOpencodeV2 {
			plan.Package = "@opencode/cli"
		}
		if !s.managedEligible(plan) {
			t.Fatalf("%s cannot install side by side", step.target)
		}
		if err := s.runManagedInstall(context.Background(), plan, &installedBaseline{path: step.baseline, expected: version}, nil, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{"opencode": "1.2.27", "opencode-v2": "2.0.1"} {
		out, err := exec.Command(filepath.Join(s.ManagedBinDir(), name), "--version").Output()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Fatalf("%s launcher=%q,%v; want %s", name, out, err, want)
		}
	}
}

func TestManagedCopilotInstallsTheLauncherNotThePlatformBinary(t *testing.T) {
	// npm's layout: bin/copilot links to the package's Node loader, and the
	// platform package's binary sits under the loader's node_modules/.bin.
	s := newManagedNPMExecService(t, func(prefix string) error {
		pkg := filepath.Join(prefix, "lib", "node_modules", "@github", "copilot")
		if err := writeVersionScript(filepath.Join(pkg, "npm-loader.js"), "1.0.101"); err != nil {
			return err
		}
		if err := writeVersionScript(filepath.Join(pkg, "node_modules", ".bin", "copilot-darwin-arm64"), "1.0.101"); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0755); err != nil {
			return err
		}
		return os.Symlink(filepath.Join("..", "lib", "node_modules", "@github", "copilot", "npm-loader.js"), filepath.Join(prefix, "bin", "copilot"))
	})
	// The resolver returns the native binary behind the npm loader.
	native := "/opt/homebrew/lib/node_modules/@github/copilot/node_modules/.bin/copilot-darwin-arm64"
	plan := Plan{Target: TargetCopilot, Method: "npm", Package: "@github/copilot"}
	if err := s.runManagedInstall(context.Background(), plan, &installedBaseline{path: native, expected: "1.0.101"}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if record, ok := s.readManagedRecord(TargetCopilot); !ok || record.CLI != "copilot" || record.BinRel != filepath.Join("bin", "copilot") {
		t.Fatalf("managed record = %+v, %v", record, ok)
	}
	out, err := exec.Command(filepath.Join(s.ManagedBinDir(), "copilot"), "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "1.0.101" {
		t.Fatalf("copilot launcher=%q,%v; want 1.0.101", out, err)
	}
}
