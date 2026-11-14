package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// harnessHeld probes the launch gate without waiting the way a launch does.
func harnessHeld(s *Service, harness domain.AgentHarness) bool {
	gate := s.harnessGate(harness)
	if gate.TryRLock() {
		gate.RUnlock()
		return false
	}
	return true
}

type installCommandRunnerFunc func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error

func (f installCommandRunnerFunc) RunInstall(ctx context.Context, command ports.InstallCommand, stdout, stderr io.Writer) error {
	return f(ctx, command, stdout, stderr)
}

func TestLiveSession(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name    string
		session domain.SessionRecord
		want    bool
	}{
		{name: "working", session: domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityActive}}, want: true},
		{name: "idle", session: domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityIdle}}, want: true},
		{name: "waiting for input", session: domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityWaitingInput}}, want: true},
		{name: "no signal yet", session: domain.SessionRecord{}, want: true},
		{name: "terminated", session: domain.SessionRecord{IsTerminated: true}, want: false},
		{name: "hibernated", session: domain.SessionRecord{HibernatedAt: &now, Activity: domain.Activity{State: domain.ActivityIdle}}, want: false},
		{name: "exited", session: domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityExited}}, want: false},
		{name: "task preparation", session: domain.SessionRecord{IsTaskPreparation: true}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := liveSession(tt.session); got != tt.want {
				t.Fatalf("liveSession = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestOMPUpdateIgnoresArchivedAndExitedSessions(t *testing.T) {
	s := newTestService("windows")
	records := []domain.SessionRecord{
		{ID: "archived", Harness: domain.HarnessOMP, IsTerminated: true, Activity: domain.Activity{State: domain.ActivityIdle}},
		{ID: "exited", Harness: domain.HarnessOMP, Activity: domain.Activity{State: domain.ActivityExited}},
	}
	s.sessions = sessionListerStub{sessions: records}
	plan := Plan{Target: TargetOMP, UpdateWhileRunning: UpdateWhileRunningBlocked}
	if _, err := s.gateRunningSessions(context.Background(), TargetOMP, AgentOperationUpdate, plan); err != nil {
		t.Fatalf("archived or exited OMP session blocked update: %v", err)
	}
	// A live idle process still uses the executable and must be counted.
	s.sessions = sessionListerStub{sessions: append(records,
		domain.SessionRecord{ID: "live", Harness: domain.HarnessOMP, Activity: domain.Activity{State: domain.ActivityIdle}})}
	_, err := s.gateRunningSessions(context.Background(), TargetOMP, AgentOperationUpdate, plan)
	var active *HarnessActiveError
	if !errors.As(err, &active) || active.Count() != 1 || !slices.Equal(active.SessionIDs, []domain.SessionID{"live"}) {
		t.Fatalf("update blocker = %v, want only the live OMP session", err)
	}
}

func TestUpdateWhileRunningLevelMatrix(t *testing.T) {
	for _, tt := range []struct {
		goos   string
		target Target
		method string
		cask   bool
		pkg    string
		want   UpdateWhileRunning
	}{
		{"darwin", TargetClaudeCode, "official-installer", false, "", UpdateWhileRunningSafe},
		{"linux", TargetCursor, "official-installer", false, "", UpdateWhileRunningSafe},
		{"darwin", TargetGrok, "official-installer", false, "", UpdateWhileRunningSafe},
		{"darwin", TargetCodex, "official-installer", false, "", UpdateWhileRunningWarn},
		{"linux", TargetOpencode, "official-installer", false, "", UpdateWhileRunningWarn},
		{"darwin", TargetKimi, "homebrew", false, "kimi-cli", UpdateWhileRunningSafe},
		{"darwin", TargetOMP, "homebrew", false, "oh-my-pi", UpdateWhileRunningSafe},
		{"darwin", TargetCodex, "homebrew", true, "codex", UpdateWhileRunningWarn},
		{"darwin", TargetClaudeCode, "homebrew", true, "claude-code", UpdateWhileRunningSafe},
		{"darwin", TargetCopilot, "homebrew", true, "copilot-cli", UpdateWhileRunningSafe},
		{"darwin", TargetDroid, "homebrew", true, "droid", UpdateWhileRunningSafe},
		// Only the audited cask is known to ship a single binary.
		{"darwin", TargetClaudeCode, "homebrew", true, "claude-code@latest", UpdateWhileRunningWarn},
		{"darwin", TargetCopilot, "homebrew", true, "someone/tap/copilot-cli", UpdateWhileRunningWarn},
		{"darwin", TargetDroid, "homebrew", true, "", UpdateWhileRunningWarn},
		{"darwin", TargetAuggie, "npm", false, "", UpdateWhileRunningWarn},
		{"darwin", TargetOMP, "bun", false, "", UpdateWhileRunningWarn},
		{"linux", TargetAider, "uv", false, "", UpdateWhileRunningWarn},
		{"linux", TargetAider, "pipx", false, "", UpdateWhileRunningWarn},
		{"windows", TargetClaudeCode, "official-installer", false, "", UpdateWhileRunningBlocked},
		{"windows", TargetCodex, "winget", false, "", UpdateWhileRunningBlocked},
		{"windows", TargetCodex, "npm", false, "", UpdateWhileRunningBlocked},
		{"darwin", TargetCodex, "manual", false, "", UpdateWhileRunningBlocked},
	} {
		if got := updateWhileRunningLevel(tt.goos, tt.target, tt.method, tt.cask, tt.pkg); got != tt.want {
			t.Errorf("%s %s %s cask=%t %q = %s, want %s", tt.goos, tt.target, tt.method, tt.cask, tt.pkg, got, tt.want)
		}
	}
}

func TestUpdatePlansCarryTheirLevel(t *testing.T) {
	s := newTestService("darwin", "brew", "npm", "bash")
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	levels := map[string]UpdateWhileRunning{}
	for _, plan := range planner.agentMethodPlans(TargetCodex, AgentOperationUpdate) {
		levels[plan.Method] = plan.UpdateWhileRunning
	}
	if levels["homebrew"] != UpdateWhileRunningWarn || levels["npm"] != UpdateWhileRunningWarn {
		t.Fatalf("codex update levels = %v, want cask and npm warn", levels)
	}
	for _, plan := range planner.agentMethodPlans(TargetCodex, AgentOperationInstall) {
		if plan.UpdateWhileRunning != "" {
			t.Fatalf("install plan %s carries update level %q", plan.Method, plan.UpdateWhileRunning)
		}
	}
}

// updateWhileRunningService returns a darwin service whose Kimi Homebrew
// formula update succeeds, with live sessions given by sessions.
func updateWhileRunningService(t *testing.T, sessions []domain.SessionRecord) (*Service, *[]ports.InstallCommand) {
	t.Helper()
	s := newTestService("darwin", "bash", "brew", "npm")
	baseCommands := s.commands
	s.commands = commandRunnerFunc(func(ctx context.Context, argv []string, out, stderr io.Writer) error {
		if argv[0] == "ps" {
			_, err := io.WriteString(out, "1 /usr/local/bin/ao\n")
			return err
		}
		if len(argv) == 3 && argv[0] == "brew" && argv[1] == "--cellar" {
			_, err := io.WriteString(out, "/opt/homebrew/Cellar/"+argv[2]+"\n")
			return err
		}
		return baseCommands.Run(ctx, argv, out, stderr)
	})
	s.sessions = sessionListerStub{sessions: sessions}
	version := "1.2.0"
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/homebrew/Cellar/kimi-code/1.2.0/bin/kimi", Output: version}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	var mu sync.Mutex
	var ran []ports.InstallCommand
	s.installCommands = installCommandRunnerFunc(func(_ context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		mu.Lock()
		defer mu.Unlock()
		ran = append(ran, command)
		if command.Argv[1] == "upgrade" {
			version = "1.3.0"
		}
		return nil
	})
	return s, &ran
}

func TestUpdateWhileRunningDecisionTable(t *testing.T) {
	live := []domain.SessionRecord{{ID: "kimi-1", Harness: domain.AgentHarness(TargetKimi), Activity: domain.Activity{State: domain.ActivityIdle}}}
	for _, tt := range []struct {
		name         string
		sessions     []domain.SessionRecord
		method       string
		disabled     bool
		wantRun      bool
		wantLevel    UpdateWhileRunning
		wantStarting bool
		starting     bool
	}{
		{name: "no live sessions runs", sessions: nil, method: "homebrew", wantRun: true},
		{name: "safe runs without asking", sessions: live, method: "homebrew", wantRun: true},
		{name: "unmanaged npm cannot retain live releases", sessions: live, method: "npm", wantLevel: UpdateWhileRunningBlocked},
		{name: "starting session refuses", sessions: nil, method: "homebrew", starting: true, wantStarting: true, wantLevel: UpdateWhileRunningBlocked},
		{name: "kill switch blocks", sessions: live, method: "homebrew", disabled: true, wantLevel: UpdateWhileRunningBlocked},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := updateWhileRunningService(t, tt.sessions)
			s.updateWhileRunningDisabled = tt.disabled
			if tt.method == "npm" {
				// Kimi has no npm method; Auggie's npm update is the blocked case.
				s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "auggie-1", Harness: domain.AgentHarness(TargetAuggie)}}}
			}
			if tt.starting {
				release, ok := s.TryBeginHarnessUse(domain.AgentHarness(TargetKimi))
				if !ok {
					t.Fatal("launch lease rejected")
				}
				defer release()
			}
			target := TargetKimi
			if tt.method == "npm" {
				target = TargetAuggie
			}
			_, err := s.StartAgentOperation(context.Background(), target, tt.method, AgentOperationUpdate, "")
			s.workers.Wait()
			if tt.wantRun {
				if err != nil {
					t.Fatalf("update = %v, want it to run", err)
				}
				return
			}
			var active *HarnessActiveError
			if !errors.As(err, &active) || !errors.Is(err, ErrHarnessActive) {
				t.Fatalf("update = %v, want HarnessActiveError matching ErrHarnessActive", err)
			}
			if active.Level != tt.wantLevel || active.Starting != tt.wantStarting {
				t.Fatalf("error = %+v, want level=%s starting=%t", active, tt.wantLevel, tt.wantStarting)
			}
			if !tt.wantStarting && active.Count() != 1 {
				t.Fatalf("count = %d, want 1", active.Count())
			}
		})
	}
}

func TestSleepingSessionsDoNotBlockUpdates(t *testing.T) {
	now := time.Now()
	s, _ := updateWhileRunningService(t, []domain.SessionRecord{
		{ID: "hibernated", Harness: domain.AgentHarness(TargetKimi), HibernatedAt: &now},
		{ID: "exited", Harness: domain.AgentHarness(TargetKimi), Activity: domain.Activity{State: domain.ActivityExited}},
	})
	// Even with the switch off, sessions without a process never block.
	s.updateWhileRunningDisabled = true
	if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatalf("update with only sleeping sessions = %v", err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetKimi, StatusSucceeded)
}

func TestReinstallNeverRunsAlongsideSessions(t *testing.T) {
	s, _ := updateWhileRunningService(t, []domain.SessionRecord{{ID: "kimi-1", Harness: domain.AgentHarness(TargetKimi)}})
	_, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationReinstall, "")
	var active *HarnessActiveError
	if !errors.As(err, &active) || active.Level != UpdateWhileRunningBlocked {
		t.Fatalf("reinstall alongside sessions = %v, want blocked", err)
	}
}

func TestHomebrewCleanupDeferredOnlyWithLiveSessions(t *testing.T) {
	for _, tt := range []struct {
		name     string
		sessions []domain.SessionRecord
		want     bool
	}{
		{name: "live sessions", sessions: []domain.SessionRecord{{ID: "kimi-1", Harness: domain.AgentHarness(TargetKimi)}}, want: true},
		{name: "no sessions", sessions: nil, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, ran := updateWhileRunningService(t, tt.sessions)
			if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
				t.Fatal(err)
			}
			s.workers.Wait()
			waitForStatus(t, s, TargetKimi, StatusSucceeded)
			for _, command := range *ran {
				if got := slices.Contains(command.Env, "HOMEBREW_NO_INSTALL_CLEANUP=1"); got != tt.want {
					t.Fatalf("%v env has cleanup deferral = %t, want %t", command.Argv, got, tt.want)
				}
			}
		})
	}
}

func TestAdvisoryReportsSessionsInUse(t *testing.T) {
	s := newTestService("darwin")
	s.updateChecksDisabled = false
	s.sessions = sessionListerStub{sessions: []domain.SessionRecord{
		{ID: "b", Harness: domain.HarnessClaudeCode},
		{ID: "a", Harness: domain.HarnessClaudeCode},
		{ID: "other", Harness: domain.HarnessCodex},
	}}
	s.reviewers = reviewerListerStub{handles: []domain.ReviewerHandle{{SessionID: "a", Harness: domain.ReviewerClaudeCode}}}
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetClaudeCode: {
		AgentID: string(TargetClaudeCode), Status: UpdateStatusBehindLatest, CurrentVersion: "2.1.0", LatestVersion: "2.1.1",
		MaintenanceMethod: "official-installer", CheckedAt: time.Now(),
	}}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetClaudeCode, false)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.InUse == nil || advisory.InUse.LiveSessions != 2 || !slices.Equal(advisory.InUse.SessionIDs, []string{"a", "b"}) || advisory.InUse.Level != UpdateWhileRunningSafe {
		t.Fatalf("inUse = %+v, want 2 safe sessions a,b", advisory.InUse)
	}
	if cached := s.updateAdvisories[TargetClaudeCode]; cached.InUse != nil {
		t.Fatal("inUse was cached with the release check")
	}
	s.sessions = sessionListerStub{}
	s.reviewers = nil
	if advisory, _ = s.UpdateAdvisory(context.Background(), TargetClaudeCode, false); advisory.InUse != nil {
		t.Fatalf("inUse = %+v after sessions ended, want none", advisory.InUse)
	}
}

func TestAdvisoryUsesManagedUpdatePlan(t *testing.T) {
	for _, method := range []struct {
		name   string
		target Target
	}{{"npm", TargetCodex}, {"bun", TargetOMP}, {"uv", TargetVibe}, {"pipx", TargetVibe}} {
		for _, control := range []string{"managed", "disabled", "unmanaged", "old npm runtime", "missing installer runner", "missing script runner", "missing shell", "capability probe failure"} {
			t.Run(method.name+"/"+control, func(t *testing.T) {
				s := newTestService("linux", "npm", "bun", "uv", "pipx", "bash", "sh")
				s.updateChecksDisabled = false
				s.managedRoot = t.TempDir()
				s.installCommands = installCommandRunnerFunc(func(context.Context, ports.InstallCommand, io.Writer, io.Writer) error { return nil })
				s.installScripts = installScriptRunnerFunc(func(context.Context, ports.InstallScriptCommand, io.Writer, io.Writer) (ports.InstallScriptResult, error) {
					return ports.InstallScriptResult{}, nil
				})
				s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "live", Harness: domain.AgentHarness(method.target)}}}
				s.updateAdvisories = map[Target]UpdateAdvisory{method.target: {AgentID: string(method.target), Status: UpdateStatusBehindLatest, MaintenanceMethod: method.name, CheckedAt: time.Now()}}
				want := UpdateWhileRunningSafe
				switch control {
				case "disabled":
					s.updateWhileRunningDisabled = true
					want = UpdateWhileRunningBlocked
				case "unmanaged":
					s.managedRoot = ""
					want = UpdateWhileRunningBlocked
				case "missing installer runner":
					s.installCommands = nil
					s.installScripts = nil
					want = UpdateWhileRunningBlocked
				case "missing script runner":
					s.installScripts = nil
					if method.name == "bun" {
						want = UpdateWhileRunningBlocked
					}
				case "missing shell":
					s.executables = executableFinderFunc(lookPathFound("npm", "bun", "uv", "pipx", "bash"))
					if method.name == "bun" {
						want = UpdateWhileRunningBlocked
					}
				case "capability probe failure":
					s.installCapabilities = installCapabilitiesStub{probe: func(context.Context) error { return errors.New("unavailable") }}
					want = UpdateWhileRunningBlocked
				case "old npm runtime":
					s.installCapabilities = installCapabilitiesStub{prefix: "/prefix", nodeVersion: "v14.0.0", writable: true}
					if method.name == "npm" {
						want = UpdateWhileRunningBlocked
					}
				}
				advisory, err := s.UpdateAdvisory(context.Background(), method.target, false)
				if err != nil || advisory.InUse == nil || advisory.InUse.Level != want {
					t.Fatalf("in-use advisory = %+v, %v; want %s", advisory.InUse, err, want)
				}
			})
		}
	}
}

func TestLaunchDropsAdvisoryForBinaryReplacedOutsideAO(t *testing.T) {
	dir := t.TempDir()
	versions := filepath.Join(dir, "versions")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(versions, "2.1.0")
	if err := os.WriteFile(old, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "claude")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	s := newTestService("darwin")
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetClaudeCode: {AgentID: string(TargetClaudeCode), Status: UpdateStatusBehindLatest, binaryIdentity: identifyBinary(link)}}

	release, ok := s.TryBeginHarnessUse(domain.HarnessClaudeCode)
	if !ok {
		t.Fatal("launch rejected")
	}
	release()
	if _, cached := s.updateAdvisories[TargetClaudeCode]; !cached {
		t.Fatal("unchanged binary dropped its advisory")
	}

	// `claude update` in a terminal installs a new release and moves the link.
	newer := filepath.Join(versions, "2.1.1")
	if err := os.WriteFile(newer, []byte("newer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newer, link); err != nil {
		t.Fatal(err)
	}
	release, ok = s.TryBeginHarnessUse(domain.HarnessClaudeCode)
	if !ok {
		t.Fatal("launch rejected")
	}
	release()
	if _, cached := s.updateAdvisories[TargetClaudeCode]; cached {
		t.Fatal("advisory for a replaced binary was kept")
	}
}

func TestLaunchesWaitForAnUpdateInsteadOfFailing(t *testing.T) {
	s, _ := updateWhileRunningService(t, []domain.SessionRecord{{ID: "kimi-1", Harness: domain.AgentHarness(TargetKimi)}})
	started := make(chan struct{})
	finish := make(chan struct{})
	s.installCommands = installCommandRunnerFunc(func(ctx context.Context, command ports.InstallCommand, _, _ io.Writer) error {
		if command.Argv[1] != "upgrade" {
			return nil
		}
		close(started)
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	<-started
	if !harnessHeld(s, domain.AgentHarness(TargetKimi)) {
		t.Fatal("a launch could start on a binary being replaced")
	}
	launched := make(chan bool, 1)
	go func() {
		release, ok := s.TryBeginHarnessUse(domain.AgentHarness(TargetKimi))
		if ok {
			release()
		}
		launched <- ok
	}()
	select {
	case <-launched:
		t.Fatal("a launch started during the update")
	case <-time.After(100 * time.Millisecond):
	}
	close(finish)
	select {
	case ok := <-launched:
		if !ok {
			t.Fatal("the launch failed instead of starting once the update ended")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the launch never started after the update")
	}
	s.workers.Wait()
}

func TestUpdatesRunAlongsideSessionsWithoutAsking(t *testing.T) {
	s := newTestService("darwin")
	s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}}}
	for _, level := range []UpdateWhileRunning{UpdateWhileRunningSafe} {
		plan := Plan{Target: TargetCodex, Method: "homebrew", PackageCask: true, UpdateWhileRunning: level}
		if withSessions, err := s.gateRunningSessions(context.Background(), TargetCodex, AgentOperationUpdate, plan); err != nil || !withSessions {
			t.Fatalf("%s update without consent = %t, %v; want it to run", level, withSessions, err)
		}
	}
}

func TestInPlaceUpdatesNeverRewriteLiveProcesses(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			s := newTestService(goos)
			s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}}}
			plan := Plan{Target: TargetCodex, Method: "official-installer", UpdateWhileRunning: UpdateWhileRunningWarn}
			withSessions, err := s.gateRunningSessions(context.Background(), TargetCodex, AgentOperationUpdate, plan)
			var active *HarnessActiveError
			if withSessions || !errors.As(err, &active) || active.Level != UpdateWhileRunningBlocked {
				t.Fatalf("in-place update can remove files a live process needs: %t, %v", withSessions, err)
			}
			// Once no process uses the executable, ordinary in-place updates remain valid.
			s.sessions = sessionListerStub{}
			if _, err := s.gateRunningSessions(context.Background(), TargetCodex, AgentOperationUpdate, plan); err != nil {
				t.Fatal(err)
			}
		})
	}
}
