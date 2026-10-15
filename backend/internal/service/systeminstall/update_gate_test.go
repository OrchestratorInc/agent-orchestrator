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

func TestUpdateWhileRunningLevelMatrix(t *testing.T) {
	for _, tt := range []struct {
		goos   string
		target Target
		method string
		cask   bool
		want   UpdateWhileRunning
	}{
		{"darwin", TargetClaudeCode, "official-installer", false, UpdateWhileRunningSafe},
		{"linux", TargetCursor, "official-installer", false, UpdateWhileRunningSafe},
		{"darwin", TargetGrok, "official-installer", false, UpdateWhileRunningSafe},
		{"darwin", TargetCodex, "official-installer", false, UpdateWhileRunningWarn},
		{"linux", TargetOpencode, "official-installer", false, UpdateWhileRunningWarn},
		{"darwin", TargetKimi, "homebrew", false, UpdateWhileRunningSafe},
		{"darwin", TargetOMP, "homebrew", false, UpdateWhileRunningSafe},
		{"darwin", TargetCodex, "homebrew", true, UpdateWhileRunningWarn},
		{"darwin", TargetClaudeCode, "homebrew", true, UpdateWhileRunningWarn},
		{"darwin", TargetAuggie, "npm", false, UpdateWhileRunningWarn},
		{"darwin", TargetOMP, "bun", false, UpdateWhileRunningWarn},
		{"linux", TargetAider, "uv", false, UpdateWhileRunningWarn},
		{"linux", TargetAider, "pipx", false, UpdateWhileRunningWarn},
		{"windows", TargetClaudeCode, "official-installer", false, UpdateWhileRunningBlocked},
		{"windows", TargetCodex, "winget", false, UpdateWhileRunningBlocked},
		{"windows", TargetCodex, "npm", false, UpdateWhileRunningBlocked},
		{"darwin", TargetCodex, "manual", false, UpdateWhileRunningBlocked},
	} {
		if got := updateWhileRunningLevel(tt.goos, tt.target, tt.method, tt.cask); got != tt.want {
			t.Errorf("%s %s %s cask=%t = %s, want %s", tt.goos, tt.target, tt.method, tt.cask, got, tt.want)
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
		{name: "npm runs too", sessions: live, method: "npm", wantRun: true},
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
	for _, level := range []UpdateWhileRunning{UpdateWhileRunningSafe, UpdateWhileRunningWarn} {
		plan := Plan{Target: TargetCodex, Method: "homebrew", PackageCask: true, UpdateWhileRunning: level}
		if withSessions, err := s.gateRunningSessions(context.Background(), TargetCodex, AgentOperationUpdate, plan); err != nil || !withSessions {
			t.Fatalf("%s update without consent = %t, %v; want it to run", level, withSessions, err)
		}
	}
}
