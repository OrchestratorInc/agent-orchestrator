package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type installJobStoreFake struct {
	mu                 sync.Mutex
	records            map[string]ports.AgentInstallJobRecord
	history            []ports.AgentInstallJobRecord
	upsertErr          error
	upsertErrForStatus map[string]error
	upsert             func(context.Context, ports.AgentInstallJobRecord) error
}

func newInstallJobStoreFake() *installJobStoreFake {
	return &installJobStoreFake{records: make(map[string]ports.AgentInstallJobRecord)}
}

func (s *installJobStoreFake) UpsertAgentInstallJob(ctx context.Context, record ports.AgentInstallJobRecord) error {
	if s.upsert != nil {
		return s.upsert(ctx, record)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertErr != nil {
		return s.upsertErr
	}
	if err := s.upsertErrForStatus[record.Status]; err != nil {
		return err
	}
	s.records[record.Target] = record
	s.history = append(s.history, record)
	return nil
}

func TestAgentJobTransitionPersistenceIsBounded(t *testing.T) {
	store := newInstallJobStoreFake()
	store.upsert = func(ctx context.Context, _ ports.AgentInstallJobRecord) error {
		<-ctx.Done()
		return ctx.Err()
	}
	s := newTestService("darwin", "npm")
	s.jobStore = store
	s.persistenceTimeout = 20 * time.Millisecond
	now := time.Now().UTC()
	job := &Job{Target: TargetCodex, Status: StatusInstalling, StartedAt: &now, UpdatedAt: &now}

	started := time.Now()
	err := s.transitionAgentJob(job, StatusVerifying, "", "", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transitionAgentJob error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("transitionAgentJob blocked for %s", elapsed)
	}
}

func (s *installJobStoreFake) GetAgentInstallJob(_ context.Context, target string) (ports.AgentInstallJobRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[target]
	return record, ok, nil
}

func (s *installJobStoreFake) ListAgentInstallJobs(context.Context) ([]ports.AgentInstallJobRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.AgentInstallJobRecord, 0, len(s.records))
	for _, record := range s.records {
		out = append(out, record)
	}
	return out, nil
}

func (s *installJobStoreFake) InterruptActiveAgentInstallJobs(_ context.Context, interruptedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for target, record := range s.records {
		if record.Status != string(StatusInstalling) && record.Status != string(StatusVerifying) {
			continue
		}
		record.Status = string(StatusInterrupted)
		record.Error = "AO restarted before this job completed."
		record.FinishedAt = &interruptedAt
		record.UpdatedAt = interruptedAt
		s.records[target] = record
	}
	return nil
}

type harnessVerifierFunc func(context.Context, Target) (VerifyResult, error)

func (f harnessVerifierFunc) Verify(ctx context.Context, target Target) (VerifyResult, error) {
	return f(ctx, target)
}

type sessionListerStub struct {
	sessions []domain.SessionRecord
	err      error
}

func (s sessionListerStub) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return s.sessions, s.err
}

type sessionListerFunc func(context.Context) ([]domain.SessionRecord, error)

func (f sessionListerFunc) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	return f(ctx)
}

type commandRunnerFunc func(context.Context, []string, io.Writer, io.Writer) error

func (f commandRunnerFunc) Run(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	return f(ctx, argv, stdout, stderr)
}

type installScriptRunnerFunc func(context.Context, ports.InstallScriptCommand, io.Writer, io.Writer) (ports.InstallScriptResult, error)

func (f installScriptRunnerFunc) RunInstallScript(ctx context.Context, command ports.InstallScriptCommand, stdout, stderr io.Writer) (ports.InstallScriptResult, error) {
	return f(ctx, command, stdout, stderr)
}

func testCommandRunner(command func(context.Context, []string) *exec.Cmd) commandRunnerFunc {
	return func(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
		cmd := command(ctx, argv)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		return cmd.Run()
	}
}

// lookPathFound returns a lookPath fake that resolves only the names present
// in paths (defaulting each found name to "/usr/bin/<name>" when the map
// value is empty), and errors for everything else — mirroring the
// systemcheck test fake.
func lookPathFound(names ...string) func(string) (string, error) {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("exec: " + name + ": executable file not found in $PATH")
	}
}

func newTestService(goos string, found ...string) *Service {
	backgroundContext, stop := context.WithCancel(context.Background())
	return &Service{
		jobs:              make(map[Target]*Job),
		backgroundContext: backgroundContext,
		stop:              stop,
		executables:       executableFinderFunc(lookPathFound(found...)),
		installCapabilities: installCapabilitiesStub{
			prefix: "/Users/test/.npm", writable: true,
		},
		pathWritable:       pathWritableProbeFunc(func(context.Context, string) (bool, error) { return true, nil }),
		privateNPMPrefixes: map[Target]string{TargetOpencodeV2: "/Users/test/.local/share/opencode-v2-home/npm"},
		commands: testCommandRunner(func(ctx context.Context, argv []string) *exec.Cmd {
			return exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // test-only, deterministic argv
		}),
		goos:           goos,
		installTimeout: 2 * time.Second,
	}
}

type pathWritableProbeFunc func(context.Context, string) (bool, error)

func (f pathWritableProbeFunc) PathWritable(ctx context.Context, path string) (bool, error) {
	return f(ctx, path)
}

func TestPlanFor(t *testing.T) {
	tests := []struct {
		name            string
		target          Target
		goos            string
		found           []string
		wantUnsupported bool
		wantReasonHas   string
		wantNoticeHas   string
		wantCommand     []string
	}{
		{
			name: "tmux windows is unsupported", target: TargetTmux, goos: "windows",
			wantUnsupported: true, wantReasonHas: "not required on Windows",
		},
		{
			name: "tmux darwin uses brew", target: TargetTmux, goos: "darwin", found: []string{"brew"},
			wantCommand: []string{"brew", "install", "tmux"},
		},
		{
			name: "tmux darwin without brew is unsupported", target: TargetTmux, goos: "darwin",
			wantUnsupported: true, wantReasonHas: "Homebrew was not found",
		},
		{
			name: "tmux linux apt-get is unsupported with instructions", target: TargetTmux, goos: "linux", found: []string{"apt-get", "dnf"},
			wantUnsupported: true, wantReasonHas: "administrator password",
		},
		{
			name: "tmux linux dnf is unsupported with instructions", target: TargetTmux, goos: "linux", found: []string{"dnf", "zypper"},
			wantUnsupported: true, wantReasonHas: "administrator password",
		},
		{
			name: "tmux linux pacman is unsupported with instructions", target: TargetTmux, goos: "linux", found: []string{"pacman"},
			wantUnsupported: true, wantReasonHas: "administrator password",
		},
		{
			name: "tmux linux zypper is unsupported with instructions", target: TargetTmux, goos: "linux", found: []string{"zypper"},
			wantUnsupported: true, wantReasonHas: "administrator password",
		},
		{
			name: "tmux linux no package manager is unsupported", target: TargetTmux, goos: "linux",
			wantUnsupported: true, wantReasonHas: "No supported Linux package manager",
		},
		// cloudflared is what makes a phone reachable from outside the local
		// network. Nothing installed it before, so a machine without it showed a
		// normal QR that only ever worked on Wi-Fi.
		{
			name: "cloudflared darwin uses brew", target: TargetCloudflared, goos: "darwin", found: []string{"brew"},
			wantCommand: []string{"brew", "install", "cloudflared"},
		},
		{
			name: "cloudflared darwin without brew is unsupported", target: TargetCloudflared, goos: "darwin",
			wantUnsupported: true, wantReasonHas: "Homebrew",
		},
		{
			name: "cloudflared windows uses winget", target: TargetCloudflared, goos: "windows", found: []string{"winget"},
			wantCommand: []string{"winget", "install", "-e", "--id", "Cloudflare.cloudflared"},
		},
		{
			name: "cloudflared windows without winget is unsupported", target: TargetCloudflared, goos: "windows",
			wantUnsupported: true, wantReasonHas: "winget",
		},
		// Same rule as every other Linux target: these need root, and AO never
		// asks for a password, so it hands over the command instead.
		{
			name: "cloudflared linux is unsupported with instructions", target: TargetCloudflared, goos: "linux",
			found: []string{"apt-get"}, wantUnsupported: true, wantReasonHas: "administrator password",
			wantCommand: []string{"apt-get", "install", "-y", "cloudflared"},
		},
		{
			name: "gh windows uses winget", target: TargetGH, goos: "windows", found: []string{"winget"},
			wantCommand: []string{"winget", "install", "-e", "--id", "GitHub.cli"},
		},
		{
			name: "gh windows without winget is unsupported", target: TargetGH, goos: "windows",
			wantUnsupported: true, wantReasonHas: "winget was not found",
		},
		{
			name: "gh darwin uses brew", target: TargetGH, goos: "darwin", found: []string{"brew"},
			wantCommand: []string{"brew", "install", "gh"},
		},
		{
			name: "gh linux apt-get is unsupported with instructions for the gh package", target: TargetGH, goos: "linux", found: []string{"apt-get"},
			wantUnsupported: true, wantReasonHas: "administrator password",
		},
		{
			name: "gh linux pacman is unsupported with instructions for the github-cli package", target: TargetGH, goos: "linux", found: []string{"pacman"},
			wantUnsupported: true, wantReasonHas: "administrator password",
		},
		{
			name: "claude uses npm on every platform", target: TargetClaude, goos: "darwin", found: []string{"npm"},
			wantCommand: []string{"npm", "install", "-g", "@anthropic-ai/claude-code"},
		},
		{
			name: "codex without npm is unsupported", target: TargetCodex, goos: "linux",
			wantUnsupported: true, wantReasonHas: "npm was not found",
		},
		{
			name: "copilot uses npm", target: TargetCopilot, goos: "windows", found: []string{"npm"},
			wantCommand: []string{"npm", "install", "-g", "@github/copilot"},
		},
		{
			name: "opencode windows uses winget", target: TargetOpencode, goos: "windows", found: []string{"winget"},
			wantCommand: []string{"winget", "install", "-e", "--id", "SST.opencode", "--silent", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"},
		},
		{
			name: "opencode darwin remote script is manual", target: TargetOpencode, goos: "darwin", found: []string{"curl", "bash"},
			wantUnsupported: true, wantReasonHas: "does not automatically execute",
		},
		{
			name: "opencode without curl remains manual", target: TargetOpencode, goos: "linux", found: []string{"bash"},
			wantUnsupported: true, wantReasonHas: "does not automatically execute",
		},
		{
			name: "opencode with sh remains manual", target: TargetOpencode, goos: "linux", found: []string{"curl", "sh"},
			wantUnsupported: true, wantReasonHas: "does not automatically execute",
		},
		{
			name: "opencode v2 legacy route uses official installer", target: TargetOpencodeV2, goos: "linux", found: []string{"bash"},
			wantNoticeHas: "replaces the default OpenCode 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(tt.goos, tt.found...)
			plan := s.planFor(tt.target)
			if plan.Target != tt.target {
				t.Fatalf("Target = %q, want %q", plan.Target, tt.target)
			}
			if plan.Unsupported != tt.wantUnsupported {
				t.Fatalf("Unsupported = %v, want %v (reason=%q)", plan.Unsupported, tt.wantUnsupported, plan.Reason)
			}
			if tt.wantReasonHas != "" && !strings.Contains(plan.Reason, tt.wantReasonHas) {
				t.Fatalf("Reason = %q, want substring %q", plan.Reason, tt.wantReasonHas)
			}
			if tt.wantNoticeHas != "" && !strings.Contains(plan.Notice, tt.wantNoticeHas) {
				t.Fatalf("Notice = %q, want substring %q", plan.Notice, tt.wantNoticeHas)
			}
			if tt.wantCommand != nil {
				if strings.Join(plan.Command, " ") != strings.Join(tt.wantCommand, " ") {
					t.Fatalf("Command = %v, want %v", plan.Command, tt.wantCommand)
				}
			}
		})
	}
}

func TestValid(t *testing.T) {
	for _, target := range []Target{TargetTmux, TargetGH, TargetClaude, TargetCodex, TargetOpencode, TargetOpencodeV2, TargetCopilot} {
		if !Valid(target) {
			t.Errorf("Valid(%q) = false, want true", target)
		}
	}
	for _, target := range []Target{"", "rm -rf /", "../../etc/passwd", "TMUX", "tmux "} {
		if Valid(target) {
			t.Errorf("Valid(%q) = true, want false", target)
		}
	}
}

func TestStartAndStatus_Succeeded(t *testing.T) {
	s := newTestService("darwin", "brew", "tmux")
	s.commands = testCommandRunner(func(context.Context, []string) *exec.Cmd { return exec.Command("true") })

	job, err := s.Start(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if job.Status != StatusRunning {
		t.Fatalf("Status = %q, want %q", job.Status, StatusRunning)
	}
	if job.Command != "brew install tmux" {
		t.Fatalf("Command = %q, want %q", job.Command, "brew install tmux")
	}

	waitForStatus(t, s, TargetTmux, StatusSucceeded)

	final, err := s.Status(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if final.Error != "" {
		t.Fatalf("Error = %q, want empty", final.Error)
	}
	if final.FinishedAt == nil {
		t.Fatalf("FinishedAt is nil, want set")
	}
}

func TestStart_SuccessCallbackRunsAfterVerifiedInstall(t *testing.T) {
	s := newTestService("darwin", "npm", "codex")
	s.commands = testCommandRunner(func(context.Context, []string) *exec.Cmd { return exec.Command("true") })
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex"}, nil
	})
	succeeded := make(chan Target, 1)
	s.SetOnSucceeded(func(target Target) { succeeded <- target })

	if _, err := s.Start(context.Background(), TargetCodex); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)

	select {
	case target := <-succeeded:
		if target != TargetCodex {
			t.Fatalf("callback target = %q, want %q", target, TargetCodex)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for success callback")
	}
}

func TestStart_FailedInstallDoesNotRunSuccessCallback(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.commands = testCommandRunner(func(context.Context, []string) *exec.Cmd { return exec.Command("false") })
	called := make(chan Target, 1)
	s.SetOnSucceeded(func(target Target) { called <- target })

	if _, err := s.Start(context.Background(), TargetCodex); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, TargetCodex, StatusFailed)

	select {
	case target := <-called:
		t.Fatalf("success callback ran for failed target %q", target)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestStart_ExitZeroWithoutTargetOnPATHFails(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.commands = testCommandRunner(func(context.Context, []string) *exec.Cmd { return exec.Command("true") })

	if _, err := s.Start(context.Background(), TargetTmux); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForStatus(t, s, TargetTmux, StatusFailed)

	final, err := s.Status(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !strings.Contains(final.Error, "tmux is still not in PATH") {
		t.Fatalf("Error = %q, want failed PATH verification", final.Error)
	}
}

func TestStartAndStatus_Failed(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.commands = testCommandRunner(func(context.Context, []string) *exec.Cmd { return exec.Command("false") })

	if _, err := s.Start(context.Background(), TargetTmux); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	waitForStatus(t, s, TargetTmux, StatusFailed)

	final, _ := s.Status(context.Background(), TargetTmux)
	if final.Error == "" {
		t.Fatalf("Error is empty, want the exec failure")
	}
}

func TestStart_Unsupported(t *testing.T) {
	s := newTestService("windows") // no winget on PATH

	job, err := s.Start(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if job.Status != StatusUnsupported {
		t.Fatalf("Status = %q, want %q", job.Status, StatusUnsupported)
	}
	if job.Error == "" {
		t.Fatalf("Error is empty, want the Unsupported reason")
	}
	if job.FinishedAt == nil {
		t.Fatalf("FinishedAt is nil, want set immediately for an Unsupported job")
	}
}

func TestStart_UnknownTarget(t *testing.T) {
	s := newTestService("darwin")
	if _, err := s.Start(context.Background(), Target("bogus")); err == nil {
		t.Fatalf("Start(bogus) error = nil, want an error")
	}
}

func TestStatus_UnknownTarget(t *testing.T) {
	s := newTestService("darwin")
	if _, err := s.Status(context.Background(), Target("bogus")); err == nil {
		t.Fatalf("Status(bogus) error = nil, want an error")
	}
}

func TestStatus_NeverStartedIsIdle(t *testing.T) {
	s := newTestService("darwin", "brew")
	job, err := s.Status(context.Background(), TargetGH)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if job.Status != StatusIdle {
		t.Fatalf("Status = %q, want %q", job.Status, StatusIdle)
	}
	if job.Target != TargetGH {
		t.Fatalf("Target = %q, want %q", job.Target, TargetGH)
	}
	if job.Command != "brew install gh" {
		t.Fatalf("Command = %q, want install preview", job.Command)
	}
}

func TestStatus_LinuxReturnsManualCommandBeforeStart(t *testing.T) {
	s := newTestService("linux", "apt-get")
	job, err := s.Status(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if job.Status != StatusUnsupported {
		t.Fatalf("Status = %q, want %q", job.Status, StatusUnsupported)
	}
	if job.Command != "sudo apt-get install -y tmux" {
		t.Fatalf("Command = %q, want exact sudo command", job.Command)
	}
}

// TestStart_IdempotentWhileRunning gates the fake install on a channel so the
// test controls exactly when it finishes, then fires two concurrent Starts
// and confirms neither one starts a second run.
func TestStart_IdempotentWhileRunning(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)

	s := newTestService("darwin", "brew", "tmux")
	callCount := 0
	s.commands = testCommandRunner(func(context.Context, []string) *exec.Cmd {
		callCount++
		started <- struct{}{}
		<-release
		return exec.Command("true")
	})

	first, err := s.Start(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if first.Status != StatusRunning {
		t.Fatalf("first Status = %q, want %q", first.Status, StatusRunning)
	}

	<-started // the background goroutine has begun (and is blocked on release)

	second, err := s.Start(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if second.Status != StatusRunning {
		t.Fatalf("second Status = %q, want %q", second.Status, StatusRunning)
	}

	close(release)
	waitForStatus(t, s, TargetTmux, StatusSucceeded)

	if callCount != 1 {
		t.Fatalf("command runner called %d times, want 1 (Start must be idempotent while running)", callCount)
	}
}

// TestRun_Timeout confirms a stalled installer eventually surfaces as a
// failure instead of pinning its target in StatusRunning forever. The fake
// command actually respects ctx (exec.CommandContext, same as real installs),
// so the short installTimeout below kills it well before the real 5s sleep
// would return on its own.
func TestRun_Timeout(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.installTimeout = 50 * time.Millisecond
	s.commands = testCommandRunner(func(ctx context.Context, _ []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "5") //nolint:gosec // test-only, fixed argv
	})

	if _, err := s.Start(context.Background(), TargetTmux); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	waitForStatus(t, s, TargetTmux, StatusFailed)

	final, _ := s.Status(context.Background(), TargetTmux)
	if !strings.Contains(final.Error, "timed out") {
		t.Fatalf("Error = %q, want it to mention the timeout", final.Error)
	}
	if final.FinishedAt == nil {
		t.Fatalf("FinishedAt is nil, want set")
	}
}

func TestAgentInstallPersistsInstallVerifySuccessLifecycle(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("darwin", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	s.jobStore = store
	s.commands = commandRunnerFunc(func(_ context.Context, _ []string, stdout, _ io.Writer) error {
		_, _ = io.WriteString(stdout, "installed\n")
		return nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex", Output: "codex 1.2.3\n"}, nil
	})

	job, err := s.StartAgent(context.Background(), TargetCodex, "npm")
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if job.Status != StatusInstalling || job.Method != "npm" {
		t.Fatalf("initial job = %+v", job)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)

	final, err := s.Status(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if final.ExpectedDestination != "/Users/test/.npm/bin/codex" {
		t.Fatalf("expected destination = %q", final.ExpectedDestination)
	}
	if !strings.Contains(final.Output, "installed") || !strings.Contains(final.Output, "codex 1.2.3") {
		t.Fatalf("output = %q, want installer and verifier diagnostics", final.Output)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	statuses := make([]string, 0, len(store.history))
	for _, record := range store.history {
		statuses = append(statuses, record.Status)
	}
	if strings.Join(statuses, ",") != "installing,verifying,succeeded" {
		t.Fatalf("persisted statuses = %v", statuses)
	}
}

func TestStartAgentSuccessCallbackRunsAfterPersistedVerification(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("darwin", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	s.jobStore = store
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return nil })
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex"}, nil
	})
	succeeded := make(chan Target, 1)
	s.SetOnSucceeded(func(target Target) { succeeded <- target })

	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)

	select {
	case target := <-succeeded:
		if target != TargetCodex {
			t.Fatalf("callback target = %q, want %q", target, TargetCodex)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for durable agent-install success callback")
	}
}

func TestAgentVendorScriptInstallPersistsInstallVerifySuccessLifecycle(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("linux", "bash")
	s.jobStore = store
	captured := make(chan ports.InstallScriptCommand, 1)
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, stdout, _ io.Writer) (ports.InstallScriptResult, error) {
		captured <- command
		_, _ = io.WriteString(stdout, "installed\n")
		return ports.InstallScriptResult{SHA256: "abc123"}, nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/home/test/.local/bin/agent", Output: "cursor 1.2.3\n"}, nil
	})

	job, err := s.StartAgent(context.Background(), TargetCursor, "official-installer")
	if err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if job.Status != StatusInstalling || job.Method != "official-installer" {
		t.Fatalf("initial job = %+v", job)
	}
	waitForStatus(t, s, TargetCursor, StatusSucceeded)
	command := <-captured
	if command.URL != "https://cursor.com/install" {
		t.Fatalf("URL = %q", command.URL)
	}
	final, err := s.Status(context.Background(), TargetCursor)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(final.Output, "source: https://cursor.com/install") ||
		!strings.Contains(final.Output, "sha256: abc123") ||
		!strings.Contains(final.Output, "cursor 1.2.3") {
		t.Fatalf("output = %q", final.Output)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var statuses []string
	for _, record := range store.history {
		statuses = append(statuses, record.Status)
	}
	if strings.Join(statuses, ",") != "installing,verifying,succeeded" {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestAutohandStartupFailureExplainsRecoveryWithoutClaimingSuccess(t *testing.T) {
	const startupFailure = "Error: Downloaded Autohand CLI failed to start\nThe existing installation was not changed.\n"
	for _, tc := range []struct {
		name     string
		method   string
		output   string
		script   bool
		wantHint bool
	}{
		{name: "installer", method: "official-installer", output: startupFailure, script: true, wantHint: true},
		{name: "self update", method: "official-installer", output: startupFailure, wantHint: true},
		{name: "other failure", method: "official-installer", output: "Checksum verification failed\n"},
		{name: "other method", method: "homebrew", output: startupFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService("darwin", "sh")
			t.Cleanup(s.stop)
			s.commands = commandRunnerFunc(func(_ context.Context, _ []string, stdout, _ io.Writer) error {
				_, _ = io.WriteString(stdout, tc.output)
				return errors.New("exit status 1")
			})
			s.installScripts = installScriptRunnerFunc(func(_ context.Context, _ ports.InstallScriptCommand, stdout, _ io.Writer) (ports.InstallScriptResult, error) {
				_, _ = io.WriteString(stdout, tc.output)
				return ports.InstallScriptResult{}, errors.New("exit status 1")
			})
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				t.Fatal("failed installer must not verify the old binary as a successful update")
				return VerifyResult{}, nil
			})
			plan := Plan{Target: TargetAutohand, Method: tc.method, Command: []string{"autohand", "update"}}
			if tc.script {
				plan.Script = &ports.InstallScriptCommand{URL: "https://autohand.ai/install.sh", Interpreter: []string{"sh"}}
			}
			job := &Job{Target: TargetAutohand, Status: StatusInstalling}
			s.jobs[TargetAutohand] = job
			s.runAgentOperation(t.Context(), plan, AgentOperationUpdate, job, nil)
			if job.Status != StatusFailed || job.Output != tc.output {
				t.Fatalf("job = %+v, want failure with original diagnostics", job)
			}
			if tc.wantHint {
				for _, want := range []string{"existing installation was not changed", "startup check", "retry", "exit status 1"} {
					if !strings.Contains(job.Error, want) {
						t.Fatalf("error = %q, want %q", job.Error, want)
					}
				}
			} else if job.Error != "exit status 1" {
				t.Fatalf("unrelated error was rewritten: %q", job.Error)
			}
		})
	}
}

func TestAgentVendorScriptInstallPreservesPlanEnvironment(t *testing.T) {
	s := newTestService("windows", "pwsh.exe")
	captured := make(chan ports.InstallScriptCommand, 1)
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
		captured <- command
		return ports.InstallScriptResult{}, nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: `C:\Users\test\.local\bin\goose.exe`}, nil
	})

	if _, err := s.StartAgent(context.Background(), TargetGoose, "official-installer"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	waitForStatus(t, s, TargetGoose, StatusSucceeded)
	command := <-captured
	if !slices.Contains(command.Env, "CONFIGURE=false") {
		t.Fatalf("installer env = %v, want CONFIGURE=false", command.Env)
	}
	if !slices.Contains(command.Env, "NONINTERACTIVE=1") {
		t.Fatalf("installer env = %v, want AO noninteractive environment", command.Env)
	}
}

func TestAgentVendorScriptInstallFailsWithoutRunner(t *testing.T) {
	s := newTestService("linux", "bash")
	if _, err := s.StartAgent(context.Background(), TargetCursor, "official-installer"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, TargetCursor, StatusFailed)
	job, _ := s.Status(context.Background(), TargetCursor)
	if !strings.Contains(job.Error, "runner is not configured") {
		t.Fatalf("error = %q", job.Error)
	}
}

func TestAgentVendorScriptInstallPreservesDigestOnRunnerFailure(t *testing.T) {
	s := newTestService("linux", "bash")
	s.installScripts = installScriptRunnerFunc(func(context.Context, ports.InstallScriptCommand, io.Writer, io.Writer) (ports.InstallScriptResult, error) {
		return ports.InstallScriptResult{SHA256: "deadbeef"}, errors.New("installer exited 7")
	})
	if _, err := s.StartAgent(context.Background(), TargetCursor, "official-installer"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, TargetCursor, StatusFailed)
	job, _ := s.Status(context.Background(), TargetCursor)
	if job.Error != "installer exited 7" || !strings.Contains(job.Output, "sha256: deadbeef") {
		t.Fatalf("job = %+v", job)
	}
}

func TestDevinInstallerFailureAfterInstallConfirmationVerifiesBinary(t *testing.T) {
	s := newTestService("darwin", "bash")
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, _ ports.InstallScriptCommand, stdout, _ io.Writer) (ports.InstallScriptResult, error) {
		_, _ = io.WriteString(stdout, "\x1b[0;32m✓\x1b[0m Installed devin v3000.")
		_, _ = io.WriteString(stdout, "11.3 to ~/.local/bin/devin.\n")
		_, _ = io.WriteString(stdout, strings.Repeat("setup output\n", maxOutputBytes))
		_, _ = io.WriteString(stdout, "Error: setup failed\n")
		return ports.InstallScriptResult{SHA256: "abc123"}, errors.New("exit status 1")
	})
	verified := make(chan Target, 1)
	s.verifier = harnessVerifierFunc(func(_ context.Context, target Target) (VerifyResult, error) {
		verified <- target
		return VerifyResult{ResolvedPath: "/home/test/.local/bin/devin", Output: "devin 3000.11.3\n"}, nil
	})

	if _, err := s.StartAgent(context.Background(), TargetDevin, "official-installer"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, TargetDevin, StatusSucceeded)
	job, err := s.Status(context.Background(), TargetDevin)
	if err != nil {
		t.Fatal(err)
	}
	if target := <-verified; target != TargetDevin {
		t.Fatalf("verified target = %q, want devin", target)
	}
	if job.Error != "" || job.ExpectedDestination != "/home/test/.local/bin/devin" || !strings.Contains(job.Output, "sha256: abc123") {
		t.Fatalf("job = %+v, want verified install", job)
	}
	if strings.Contains(job.Output, "Installed devin") {
		t.Fatalf("output = %q, want confirmation evicted from bounded diagnostics", job.Output)
	}
}

func TestDevinInstallerDoesNotIgnoreOtherFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		target Target
		output string
	}{
		{name: "no install confirmation", target: TargetDevin, output: "Error: Login canceled\n"},
		{name: "different target", target: TargetCursor, output: "Installed devin v3000.11.3 to ~/.local/bin/devin.\nError: Login canceled\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService("darwin", "bash")
			s.installScripts = installScriptRunnerFunc(func(_ context.Context, _ ports.InstallScriptCommand, stdout, _ io.Writer) (ports.InstallScriptResult, error) {
				_, _ = io.WriteString(stdout, test.output)
				return ports.InstallScriptResult{}, errors.New("exit status 1")
			})
			verified := false
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				verified = true
				return VerifyResult{ResolvedPath: "/home/test/.local/bin/devin"}, nil
			})
			if _, err := s.StartAgent(context.Background(), test.target, "official-installer"); err != nil {
				t.Fatal(err)
			}
			waitForStatus(t, s, test.target, StatusFailed)
			job, _ := s.Status(context.Background(), test.target)
			if job.Error != "exit status 1" || verified {
				t.Fatalf("job = %+v, verified = %t; want original failure without verification", job, verified)
			}
		})
	}
}

func TestDevinInstallerFailureAfterInstallConfirmationFailsWhenBinaryVerificationFails(t *testing.T) {
	s := newTestService("darwin", "bash")
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, _ ports.InstallScriptCommand, stdout, _ io.Writer) (ports.InstallScriptResult, error) {
		_, _ = io.WriteString(stdout, "Installed devin v3000.11.3 to ~/.local/bin/devin.\nError: setup failed\n")
		return ports.InstallScriptResult{}, errors.New("exit status 1")
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{}, errors.New("version probe failed")
	})
	if _, err := s.StartAgent(context.Background(), TargetDevin, "official-installer"); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, TargetDevin, StatusFailed)
	job, _ := s.Status(context.Background(), TargetDevin)
	if !strings.Contains(job.Error, "version probe failed") {
		t.Fatalf("job = %+v, want failed verification", job)
	}
}

func TestAgentVendorScriptInstallTimeoutAndShutdown(t *testing.T) {
	for _, test := range []struct {
		name       string
		stop       bool
		wantStatus Status
		wantError  string
	}{
		{name: "timeout", wantStatus: StatusFailed, wantError: "timed out"},
		{name: "shutdown", stop: true, wantStatus: StatusInterrupted, wantError: "shutdown interrupted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService("linux", "bash")
			s.installTimeout = 50 * time.Millisecond
			started := make(chan struct{})
			s.installScripts = installScriptRunnerFunc(func(ctx context.Context, _ ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
				close(started)
				<-ctx.Done()
				return ports.InstallScriptResult{}, ctx.Err()
			})
			if _, err := s.StartAgent(context.Background(), TargetCursor, "official-installer"); err != nil {
				t.Fatal(err)
			}
			<-started
			if test.stop {
				if err := s.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			waitForStatus(t, s, TargetCursor, test.wantStatus)
			job, _ := s.Status(context.Background(), TargetCursor)
			if !strings.Contains(job.Error, test.wantError) {
				t.Fatalf("error = %q", job.Error)
			}
		})
	}
}

func TestAgentInstallRejectsConcurrentWorkForSameHarness(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	s := newTestService("darwin", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		close(started)
		<-release
		return nil
	})

	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	<-started
	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); !errors.Is(err, ErrInstallActive) {
		t.Fatalf("concurrent StartAgent error = %v, want ErrInstallActive", err)
	}
	if _, err := s.Verify(context.Background(), TargetCodex); !errors.Is(err, ErrInstallActive) {
		t.Fatalf("concurrent Verify error = %v, want ErrInstallActive", err)
	}
	close(release)
}

func TestCloseCancelsAndDrainsActiveAgentInstall(t *testing.T) {
	started := make(chan struct{})
	s := newTestService("darwin", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	s.commands = commandRunnerFunc(func(ctx context.Context, _ []string, _, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	job, err := s.Status(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if job.Status != StatusInterrupted {
		t.Fatalf("status after Close = %q, want interrupted", job.Status)
	}
}

func TestCloseCancelsAndDrainsLegacyClaudeInstall(t *testing.T) {
	started := make(chan struct{})
	s := newTestService("darwin", "npm")
	s.commands = commandRunnerFunc(func(ctx context.Context, _ []string, _, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})

	if _, err := s.Start(context.Background(), TargetClaude); err != nil {
		t.Fatalf("Start Claude: %v", err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	job, err := s.Status(context.Background(), TargetClaude)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if job.Status != StatusInterrupted {
		t.Fatalf("status after Close = %q, want interrupted", job.Status)
	}
}

func TestAgentVerificationGetsFreshDaemonContext(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return nil })
	s.verifier = harnessVerifierFunc(func(ctx context.Context, _ Target) (VerifyResult, error) {
		if _, hasDeadline := ctx.Deadline(); hasDeadline {
			return VerifyResult{}, errors.New("verification inherited installer deadline")
		}
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex"}, nil
	})

	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)
}

func TestAgentInstallVerificationFailureIsTerminalFailure(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("darwin", "brew")
	s.jobStore = store
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return nil })
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/homebrew/bin/codex", Output: "bad version output"}, errors.New("version probe failed")
	})

	if _, err := s.StartAgent(context.Background(), TargetCodex, "homebrew"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	waitForStatus(t, s, TargetCodex, StatusFailed)
	job, _ := s.Status(context.Background(), TargetCodex)
	if !strings.Contains(job.Error, "version probe failed") || !strings.Contains(job.Output, "bad version output") {
		t.Fatalf("failed job = %+v", job)
	}
}

func TestVerifyAgainDoesNotRunInstaller(t *testing.T) {
	store := newInstallJobStoreFake()
	s := newTestService("darwin", "brew")
	s.jobStore = store
	installCalls := 0
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		installCalls++
		return nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/homebrew/bin/codex", Output: "codex 1.2.3"}, nil
	})

	job, err := s.Verify(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if job.Status != StatusVerifying {
		t.Fatalf("initial status = %q, want verifying", job.Status)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)
	if installCalls != 0 {
		t.Fatalf("installer calls = %d, want 0", installCalls)
	}
}

func TestVerifyPersistenceFailureDoesNotLeavePhantomActiveJob(t *testing.T) {
	store := newInstallJobStoreFake()
	store.upsertErr = errors.New("database unavailable")
	s := newTestService("darwin", "brew")
	s.jobStore = store
	verifyCalls := 0
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		verifyCalls++
		return VerifyResult{ResolvedPath: "/opt/homebrew/bin/codex", Output: "codex 1.2.3"}, nil
	})

	if _, err := s.Verify(context.Background(), TargetCodex); err == nil {
		t.Fatal("Verify error = nil, want persistence failure")
	}
	store.mu.Lock()
	store.upsertErr = nil
	store.mu.Unlock()

	job, err := s.Verify(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("retry Verify: %v", err)
	}
	if job.Status != StatusVerifying {
		t.Fatalf("retry status = %q, want verifying", job.Status)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)
	if verifyCalls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifyCalls)
	}
}

func TestRecoverInterruptsAndHydratesDurableJobs(t *testing.T) {
	store := newInstallJobStoreFake()
	started := time.Now().Add(-time.Minute).UTC()
	store.records[string(TargetCodex)] = ports.AgentInstallJobRecord{
		Target: string(TargetCodex), Status: string(StatusVerifying), Method: "npm", StartedAt: started, UpdatedAt: started,
	}
	s := newTestService("darwin", "npm")
	s.jobStore = store
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	installCalls := 0
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		installCalls++
		return nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex"}, nil
	})
	if err := s.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	job, err := s.Status(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if job.Status != StatusInterrupted || job.FinishedAt == nil || job.Error == "" {
		t.Fatalf("recovered job = %+v", job)
	}
	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("retry interrupted install: %v", err)
	}
	waitForStatus(t, s, TargetCodex, StatusSucceeded)
	if installCalls != 1 {
		t.Fatalf("retry installer calls = %d, want 1", installCalls)
	}
}

func TestListAgentJobsReturnsDurableJobs(t *testing.T) {
	store := newInstallJobStoreFake()
	now := time.Now().UTC()
	store.records[string(TargetCodex)] = ports.AgentInstallJobRecord{Target: string(TargetCodex), Status: string(StatusFailed), StartedAt: now, UpdatedAt: now}
	s := newTestService("darwin", "npm")
	s.jobStore = store
	jobs, err := s.AgentJobs(context.Background())
	if err != nil {
		t.Fatalf("AgentJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Target != TargetCodex || jobs[0].Status != StatusFailed {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestTerminalPersistenceFailureOverridesStaleActiveDurableJob(t *testing.T) {
	store := newInstallJobStoreFake()
	store.upsertErrForStatus = map[string]error{string(StatusSucceeded): errors.New("database unavailable")}
	s := newTestService("darwin", "npm")
	s.installCapabilities = installCapabilitiesStub{prefix: "/Users/test/.npm", writable: true}
	s.jobStore = store
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return nil })
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.npm/bin/codex"}, nil
	})

	if _, err := s.StartAgent(context.Background(), TargetCodex, "npm"); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		terminal := s.jobs[TargetCodex] != nil && !activeStatus(s.jobs[TargetCodex].Status)
		s.mu.Unlock()
		if terminal || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}

	job, err := s.Status(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if job.Status != StatusFailed || !strings.Contains(job.Error, "persist terminal operation state") {
		t.Fatalf("Status returned stale durable job: %+v", job)
	}
	jobs, err := s.AgentJobs(context.Background())
	if err != nil {
		t.Fatalf("AgentJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != StatusFailed || !strings.Contains(jobs[0].Error, "persist terminal operation state") {
		t.Fatalf("AgentJobs returned stale durable jobs: %+v", jobs)
	}
}

func TestDroidInstallIsBlockedWhileDroidSessionIsActive(t *testing.T) {
	s := newTestService("darwin", "brew")
	s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{Harness: domain.HarnessDroid, IsTerminated: false}}}
	_, err := s.StartAgent(context.Background(), TargetDroid, "homebrew")
	if !errors.Is(err, ErrHarnessActive) {
		t.Fatalf("StartAgent error = %v, want ErrHarnessActive", err)
	}
}

func TestDroidInstallIsBlockedWhileDroidSessionIsStarting(t *testing.T) {
	s := newTestService("darwin", "brew")
	release, ok := s.TryBeginHarnessUse(domain.HarnessDroid)
	if !ok {
		t.Fatal("TryBeginHarnessUse unexpectedly rejected without an install")
	}
	defer release()

	if _, err := s.StartAgent(context.Background(), TargetDroid, "homebrew"); !errors.Is(err, ErrHarnessActive) {
		t.Fatalf("StartAgent error = %v, want ErrHarnessActive", err)
	}
}

func TestDroidInstallAllowsTerminatedSessionAndOtherHarnesses(t *testing.T) {
	for _, tt := range []struct {
		name     string
		target   Target
		sessions []domain.SessionRecord
	}{
		{name: "terminated droid", target: TargetDroid, sessions: []domain.SessionRecord{{Harness: domain.HarnessDroid, IsTerminated: true}}},
		{name: "active codex does not block droid", target: TargetDroid, sessions: []domain.SessionRecord{{Harness: domain.HarnessCodex, IsTerminated: false}}},
		{name: "active droid does not block codex", target: TargetCodex, sessions: []domain.SessionRecord{{Harness: domain.HarnessDroid, IsTerminated: false}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "brew")
			s.sessions = sessionListerStub{sessions: tt.sessions}
			s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return errors.New("stop after guard") })
			if _, err := s.StartAgent(context.Background(), tt.target, "homebrew"); err != nil {
				t.Fatalf("StartAgent: %v", err)
			}
		})
	}
}

func TestHarnessLaunchGateBlocksCodexMaintenanceBeforeSessionEnumeration(t *testing.T) {
	s := newTestService("darwin", "npm")
	sessionCalls := 0
	s.sessions = sessionListerFunc(func(context.Context) ([]domain.SessionRecord, error) {
		sessionCalls++
		return nil, nil
	})
	release, ok := s.TryBeginHarnessUse(domain.HarnessCodex)
	if !ok {
		t.Fatal("Codex launch lease was unexpectedly rejected")
	}
	defer release()

	for _, operation := range []AgentOperation{AgentOperationReinstall, AgentOperationUpdate} {
		method := "npm"
		if _, err := s.StartAgentOperation(context.Background(), TargetCodex, method, operation, ""); !errors.Is(err, ErrHarnessActive) {
			t.Fatalf("%s error = %v, want ErrHarnessActive", operation, err)
		}
	}
	if sessionCalls != 0 {
		t.Fatalf("session enumeration calls = %d, want 0 before acquiring maintenance gate", sessionCalls)
	}
}

func TestHarnessMaintenanceGateBlocksCodexLaunchThroughWorkerLifetime(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	started := make(chan struct{})
	releaseCommand := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(releaseCommand) }) }
	defer unblock()
	s.commands = commandRunnerFunc(func(ctx context.Context, _ []string, _, _ io.Writer) error {
		close(started)
		select {
		case <-releaseCommand:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/verified/codex", Output: "codex 2.0.0"}, nil
	})

	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, ""); err != nil {
		t.Fatalf("StartAgentOperation: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance worker did not start")
	}
	if !harnessHeld(s, domain.HarnessCodex) {
		t.Fatal("Codex launch could acquire the gate while maintenance worker was running")
	}
	unblock()
	s.workers.Wait()
	waitForStatus(t, s, TargetCodex, StatusSucceeded)
	if release, ok := s.TryBeginHarnessUse(domain.HarnessCodex); !ok {
		t.Fatal("Codex launch remained blocked after maintenance completed")
	} else {
		release()
	}
}

func TestHarnessMaintenanceSeesSessionCreatedBeforeLaunchGateRelease(t *testing.T) {
	s := newTestService("darwin", "npm")
	// With AO_UPDATE_WHILE_RUNNING off, the update must see the new session.
	s.updateWhileRunningDisabled = true
	// Sessions are checked once the update proves which method owns the copy.
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/verified/codex", Output: "codex 1.0.0"}, nil
	})
	release, ok := s.TryBeginHarnessUse(domain.HarnessCodex)
	if !ok {
		t.Fatal("Codex launch lease was unexpectedly rejected")
	}
	s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex}}}
	release()

	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, ""); !errors.Is(err, ErrHarnessActive) {
		t.Fatalf("StartAgentOperation error = %v, want ErrHarnessActive", err)
	}
}

func TestUpdatePinsRequestedVersionEvenWithoutCachedAdvisory(t *testing.T) {
	s := newTestService("darwin", "npm")
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return nil })
	probes := 0
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		probes++
		version := "1.2.3"
		if probes > 1 {
			version = "1.2.4"
		}
		return VerifyResult{ResolvedPath: "/verified/codex", Output: "codex " + version}, nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetCodex, StatusFailed)
	job, err := s.Status(context.Background(), TargetCodex)
	if err != nil || !strings.Contains(job.Error, "requested version 1.3.0") {
		t.Fatalf("job=%+v err=%v", job, err)
	}
}

func TestUpdateRejectsInvalidRequestedVersion(t *testing.T) {
	s := newTestService("darwin", "npm")
	for _, operation := range []AgentOperation{AgentOperationUpdate, AgentOperationInstall} {
		if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", operation, "not-a-version"); !errors.Is(err, ErrUpdateVersion) {
			t.Fatalf("operation=%s err=%v", operation, err)
		}
	}
}

func TestHarnessMaintenanceAllowsTerminatedAndOtherHarnessSessions(t *testing.T) {
	for _, tt := range []struct {
		name     string
		sessions []domain.SessionRecord
	}{
		{name: "terminated Codex", sessions: []domain.SessionRecord{{ID: "codex-1", Harness: domain.HarnessCodex, IsTerminated: true}}},
		{name: "active Claude", sessions: []domain.SessionRecord{{ID: "claude-1", Harness: domain.HarnessClaudeCode}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			s.sessions = sessionListerStub{sessions: tt.sessions}
			s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return errors.New("stop after guard") })

			if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, ""); err != nil {
				t.Fatalf("StartAgentOperation: %v", err)
			}
			waitForStatus(t, s, TargetCodex, StatusFailed)
		})
	}
}

func TestHarnessGatesAllowDifferentHarnessesConcurrently(t *testing.T) {
	s := newTestService("darwin")
	releaseCodex, ok := s.TryBeginHarnessUse(domain.HarnessCodex)
	if !ok {
		t.Fatal("Codex launch lease was unexpectedly rejected")
	}
	defer releaseCodex()

	releaseClaude, ok := s.TryBeginHarnessUse(domain.HarnessClaudeCode)
	if !ok {
		t.Fatal("Claude launch should not share the Codex gate")
	}
	releaseClaude()
}

func TestHarnessDroidGateBehaviorRemainsProtected(t *testing.T) {
	s := newTestService("darwin", "brew")
	release, ok := s.TryBeginHarnessUse(domain.HarnessDroid)
	if !ok {
		t.Fatal("Droid launch lease was unexpectedly rejected")
	}
	defer release()

	if _, err := s.StartAgent(context.Background(), TargetDroid, "homebrew"); !errors.Is(err, ErrHarnessActive) {
		t.Fatalf("Droid install error = %v, want ErrHarnessActive", err)
	}
}

func waitForStatus(t *testing.T, s *Service, target Target, want Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := s.Status(context.Background(), target)
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if job.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to reach status %q", target, want)
}

// A Linux package-manager plan must now carry the resolved argv even though it
// stays Unsupported. That pairing is the whole point of the split: the daemon
// refuses to elevate, while the CLI (which has a terminal and can prompt for a
// sudo password) runs the very same command. Before this, the argv existed only
// inside the Reason string, so the CLI had to re-derive it and the two surfaces
// could drift.
func TestLinuxPlanExposesArgvWhileStayingUnsupported(t *testing.T) {
	for _, tt := range []struct {
		name        string
		found       string
		wantCommand string
		wantManager string
	}{
		{"apt-get", "apt-get", "apt-get install -y tmux", "apt-get"},
		{"dnf", "dnf", "dnf install -y tmux", "dnf"},
		{"pacman", "pacman", "pacman -S --noconfirm tmux", "pacman"},
		{"zypper", "zypper", "zypper install -y tmux", "zypper"},
		{"apk", "apk", "apk add tmux", "apk"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := newTestService("linux", tt.found).planFor(TargetTmux)
			if got := strings.Join(plan.Command, " "); got != tt.wantCommand {
				t.Fatalf("Command = %q, want %q", got, tt.wantCommand)
			}
			if plan.Manager != tt.wantManager {
				t.Fatalf("Manager = %q, want %q", plan.Manager, tt.wantManager)
			}
			if !plan.NeedsRoot {
				t.Fatal("NeedsRoot = false, want true: every Linux package manager needs root")
			}
			// The security invariant. Exposing the argv must not have made the
			// daemon willing to run it.
			if !plan.Unsupported {
				t.Fatal("Unsupported = false, want true: the daemon must never run a root install itself")
			}
		})
	}
}

// The daemon must still refuse to execute a Linux plan, argv or not. This is
// the behavioural half of the invariant asserted above.
func TestStartRefusesLinuxRootInstall(t *testing.T) {
	s := newTestService("linux", "apt-get")
	s.commands = commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		t.Fatal("the daemon must not execute a root install")
		return nil
	})
	job, err := s.Start(context.Background(), TargetTmux)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if job.Status != StatusUnsupported {
		t.Fatalf("Status = %q, want %q", job.Status, StatusUnsupported)
	}
}

// Resolve is the seam the CLI consumes; it must agree with the Service's own
// planner rather than being a second implementation of the same table.
func TestResolveMatchesServicePlan(t *testing.T) {
	lookPath := lookPathFound("brew", "curl", "bash")
	service := &Service{goos: "darwin", executables: executableFinderFunc(lookPath)}
	for _, target := range []Target{TargetTmux, TargetCursor} {
		got := Resolve("darwin", lookPath, target)
		want := service.resolvePlan(target)
		if strings.Join(got.Command, " ") != strings.Join(want.Command, " ") {
			t.Fatalf("Resolve(%q) Command = %v, want %v", target, got.Command, want.Command)
		}
		if got.Unsupported != want.Unsupported {
			t.Fatalf("Resolve(%q) Unsupported = %v, want %v", target, got.Unsupported, want.Unsupported)
		}
		if got.Method != want.Method {
			t.Fatalf("Resolve(%q) Method = %q, want %q", target, got.Method, want.Method)
		}
	}
	if unknown := Resolve("linux", lookPath, Target("nope")); !unknown.Unsupported {
		t.Fatal("Resolve of an unknown target must be Unsupported")
	}
}

func TestVendorInstallerUpdateUsesHarnessSelfUpdateCommand(t *testing.T) {
	s := newTestService("darwin", "bash", "sh")
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[Target][]string{
		TargetClaudeCode: {"claude", "update"},
		TargetCodex:      {"codex", "update"},
		TargetOpencode:   {"opencode", "upgrade", "--method", "curl"},
		TargetGoose:      {"goose", "update"},
		TargetKimchi:     {"kimchi", "update", "self", "--force"},
		TargetOMP:        {"omp", "update"},
		TargetAutohand:   {"autohand", "update"},
		TargetCursor:     {"cursor-agent", "update"},
		TargetAmp:        {"amp", "update", "--porcelain"},
		TargetGrok:       {"grok", "update"},
		TargetDroid:      {"droid", "update"},
		TargetQwen:       {"qwen", "update"},
		TargetFX:         {"fx", "upgrade"},
	} {
		plan, err := planner.resolveAgentMethod(target, "official-installer", AgentOperationUpdate)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if !slices.Equal(plan.Command, want) || plan.Script != nil {
			t.Fatalf("%s update = %v script=%v, want %v", target, plan.Command, plan.Script, want)
		}
	}
	if _, err := planner.resolveAgentMethod(TargetMuse, "official-installer", AgentOperationUpdate); !errors.Is(err, ErrInstallMethod) {
		t.Fatalf("Muse update error = %v, want ErrInstallMethod for a vendor without a self-update command", err)
	}
	kimi, err := planner.resolveAgentMethod(TargetKimi, "official-installer", AgentOperationUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if kimi.Unsupported || len(kimi.Command) != 0 || kimi.Script == nil || kimi.Script.URL != "https://code.kimi.com/kimi-code/install.sh" {
		t.Fatalf("Kimi update = %+v, want the official installer, not kimi upgrade", kimi)
	}
}

func TestWindowsVendorUpdatesUsePowerShellInstallers(t *testing.T) {
	// A pwsh.exe alias may exist in WindowsApps but refuse execution. The system
	// powershell.exe must win when both names resolve.
	s := newTestService("windows", "pwsh.exe", "powershell.exe")
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for target, url := range map[Target]string{
		TargetAutohand: "https://autohand.ai/install.ps1",
		TargetGrok:     "https://x.ai/cli/install.ps1",
	} {
		t.Run(string(target), func(t *testing.T) {
			plan, err := planner.resolveAgentMethod(target, "official-installer", AgentOperationUpdate)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Unsupported || plan.Script == nil || plan.Script.URL != url || len(plan.Command) != 0 {
				t.Fatalf("Windows update plan = %+v, want PowerShell installer %s", plan, url)
			}
			if want := []string{"/usr/bin/powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}; !slices.Equal(plan.Script.Interpreter, want) {
				t.Fatalf("interpreter = %v, want %v", plan.Script.Interpreter, want)
			}
		})
	}
	kimi, err := planner.resolveAgentMethod(TargetKimi, "official-installer", AgentOperationUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if kimi.Unsupported || len(kimi.Command) != 0 || kimi.Script == nil || kimi.Script.URL != "https://code.kimi.com/kimi-code/install.ps1" {
		t.Fatalf("Kimi update = %+v, want the PowerShell installer, not kimi upgrade", kimi)
	}
}

func TestWindowsGrokUpdateRefusesUnknownDestination(t *testing.T) {
	for _, binary := range []string{"", "grok.exe", `D:grok.exe`, `tools\grok.exe`} {
		plan := targetInstalledCopy("windows", Plan{Method: "official-installer", Script: &ports.InstallScriptCommand{}}, TargetGrok, binary)
		if !plan.Unsupported || plan.Reason == "" {
			t.Fatalf("path %q: update was not refused: %+v", binary, plan)
		}
	}
}

func TestWindowsGrokUpdateReplacesPlanDestinationWithoutMutation(t *testing.T) {
	script := &ports.InstallScriptCommand{Env: []string{`grok_bin_dir=C:/alternate`, "KEEP=yes"}}
	plan := targetInstalledCopy("windows", Plan{Method: "official-installer", Script: script}, TargetGrok, `D:\grok.exe`)
	if !slices.Equal(plan.Script.Env, []string{"KEEP=yes", `GROK_BIN_DIR=D:\`}) {
		t.Fatalf("environment = %v", plan.Script.Env)
	}
	if !slices.Equal(script.Env, []string{`grok_bin_dir=C:/alternate`, "KEEP=yes"}) {
		t.Fatalf("original script was mutated: %v", script.Env)
	}
}

func TestWindowsGrokUpdateTargetsConfirmedDirectory(t *testing.T) {
	for _, binary := range []string{`D:\tools\grok\grok.exe`, `D:\tools with spaces\grok.exe`, `\\server\tools\grok\grok.exe`} {
		t.Run(binary, func(t *testing.T) {
			s := newTestService("windows", "powershell.exe")
			t.Cleanup(s.stop)
			t.Setenv("GROK_BIN_DIR", `C:/alternate/grok`)
			var version atomic.Int32
			version.Store(13)
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: binary, Output: fmt.Sprintf("1.0.%d", version.Load())}, nil
			})
			captured := make(chan ports.InstallScriptCommand, 1)
			s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
				captured <- command
				version.Store(14)
				return ports.InstallScriptResult{}, nil
			})
			if _, err := s.StartAgentOperation(t.Context(), TargetGrok, "official-installer", AgentOperationUpdate, ""); err != nil {
				t.Fatal(err)
			}
			waitForStatus(t, s, TargetGrok, StatusSucceeded)
			command := <-captured
			want := "GROK_BIN_DIR=" + binary[:strings.LastIndex(binary, `\`)]
			if !slices.Contains(command.Env, want) {
				t.Fatalf("installer environment = %v, want %q", command.Env, want)
			}
		})
	}
}

func TestAutohandWindowsInstallerVerifiesCLIAfterComputerControlFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		version    string
		output     string
		wantStatus Status
	}{
		{name: "CLI updated", version: "0.9.8", output: "OK Installed to C:\\autohand\\autohand.exe\nInstallation is incomplete: Computer control could not be installed.\n", wantStatus: StatusSucceeded},
		{name: "CLI unchanged", version: "0.9.7", output: "OK Installed to C:\\autohand\\autohand.exe\nInstallation is incomplete: Computer control could not be installed.\n", wantStatus: StatusFailed},
		{name: "other failure", version: "0.9.8", output: "OK Installed to C:\\autohand\\autohand.exe\nChecksum verification failed\n", wantStatus: StatusFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestService("windows", "powershell.exe")
			s.installScripts = installScriptRunnerFunc(func(_ context.Context, _ ports.InstallScriptCommand, stdout, _ io.Writer) (ports.InstallScriptResult, error) {
				_, _ = io.WriteString(stdout, test.output)
				return ports.InstallScriptResult{}, errors.New("exit status 1")
			})
			s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
				return VerifyResult{ResolvedPath: `C:\autohand\autohand.exe`, Output: test.version}, nil
			})
			job := &Job{Target: TargetAutohand, Method: "official-installer", Status: StatusInstalling}
			plan := Plan{Target: TargetAutohand, Method: "official-installer", Script: &ports.InstallScriptCommand{URL: "https://autohand.ai/install.ps1"}}
			baseline := &installedBaseline{version: "0.9.7", latest: "0.9.8", scheme: versionSchemeFor(TargetAutohand)}
			s.runAgentOperation(context.Background(), plan, AgentOperationUpdate, job, baseline)
			if job.Status != test.wantStatus {
				t.Fatalf("status = %s, error = %q, output = %q; want %s", job.Status, job.Error, job.Output, test.wantStatus)
			}
			if test.wantStatus == StatusSucceeded && !strings.Contains(job.Output, "Computer control needs a separate repair") {
				t.Fatalf("missing Computer control warning: %q", job.Output)
			}
		})
	}
}

func TestKimiUpdateReinstallsAdvisoryReleaseIntoConfirmedDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "/usr/bin:"+home+"/.kimi-code/bin:/bin")
	binary := home + "/.kimi-code/bin/kimi"
	s := newTestService("darwin", "bash")
	t.Cleanup(s.stop)
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetKimi: {AgentID: string(TargetKimi), Status: UpdateStatusBehindLatest, CurrentVersion: "2.0.2", LatestVersion: "2.2.0", CheckedAt: time.Now()}}
	var version atomic.Value
	version.Store("2.0.2")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: binary, Output: version.Load().(string)}, nil
	})
	captured := make(chan ports.InstallScriptCommand, 1)
	s.installScripts = installScriptRunnerFunc(func(_ context.Context, command ports.InstallScriptCommand, _, _ io.Writer) (ports.InstallScriptResult, error) {
		captured <- command
		version.Store("2.2.0")
		return ports.InstallScriptResult{}, nil
	})
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
		t.Errorf("update ran %v; Kimi updates must re-run the installer", argv)
		return nil
	})
	if _, err := s.StartAgentOperation(t.Context(), TargetKimi, "official-installer", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s, TargetKimi, StatusSucceeded)
	command := <-captured
	if command.URL != "https://code.kimi.com/kimi-code/install.sh" {
		t.Fatalf("installer = %s", command.URL)
	}
	for _, want := range []string{"KIMI_INSTALL_DIR=" + home + "/.kimi-code", "KIMI_NO_MODIFY_PATH=1", "KIMI_VERSION=2.2.0", "PATH=/usr/bin:/bin"} {
		if !slices.Contains(command.Env, want) {
			t.Fatalf("installer environment = %v, want %q", command.Env, want)
		}
	}
}

func TestKimiUpdateBindsOnlyKimiCodeDefaultInstall(t *testing.T) {
	for _, test := range []struct {
		goos, binary, home, root string
	}{
		{"darwin", "/Users/test/.kimi-code/bin/kimi", "/Users/test", "/Users/test/.kimi-code"},
		{"linux", "/home/u/.kimi-code/bin/kimi", "/home/u/", "/home/u/.kimi-code"},
		{"windows", `C:\Users\Test\.Kimi-Code\BIN\Kimi.exe`, `C:\Users\test`, `C:\Users\Test\.Kimi-Code`},
		// Another tool's kimi: the legacy Python kimi-cli, a cargo build, a system copy.
		{"darwin", "/Users/test/.local/bin/kimi", "/Users/test", ""},
		{"darwin", "/Users/test/.cargo/bin/kimi", "/Users/test", ""},
		{"darwin", "/usr/local/bin/kimi", "/Users/test", ""},
		{"windows", `C:\Users\test\.local\bin\kimi.exe`, `C:\Users\test`, ""},
		// Case and executable names are exact outside Windows.
		{"linux", "/home/u/.kimi-code/BIN/kimi", "/home/u", ""},
		{"darwin", "/Users/test/.kimi-code/bin/kimi.exe", "/Users/test", ""},
		{"darwin", "/Users/test/.kimi-code/bin/kimi-legacy", "/Users/test", ""},
		{"darwin", "", "/Users/test", ""},
		{"darwin", "/Users/test/.kimi-code/bin/kimi", "", ""},
	} {
		script := &ports.InstallScriptCommand{Env: []string{"kimi_install_dir=/elsewhere", "KEEP=yes"}}
		plan := bindKimiInstallerUpdate(test.goos, Plan{Target: TargetKimi, Method: "official-installer", Script: script}, test.binary, test.home, "")
		if test.root == "" {
			if !plan.Unsupported || plan.Reason == "" {
				t.Errorf("%s %q: update was not refused: %+v", test.goos, test.binary, plan)
			}
			continue
		}
		want := []string{"KEEP=yes", "KIMI_INSTALL_DIR=" + test.root, "KIMI_NO_MODIFY_PATH=1"}
		if plan.Unsupported || !slices.Equal(plan.Script.Env, want) || plan.ExpectedDestination != test.binary {
			t.Errorf("%s %q: plan = %+v env = %v, want %v", test.goos, test.binary, plan, plan.Script.Env, want)
		}
		if !slices.Equal(script.Env, []string{"kimi_install_dir=/elsewhere", "KEEP=yes"}) {
			t.Errorf("original script was mutated: %v", script.Env)
		}
	}
	brew := Plan{Target: TargetKimi, Method: "homebrew", Command: []string{"brew", "upgrade", "kimi-code"}}
	if got := bindKimiInstallerUpdate("darwin", brew, "/opt/homebrew/bin/kimi", "/Users/test", "/usr/bin"); !reflect.DeepEqual(got, brew) {
		t.Fatalf("Homebrew plan changed: %+v", got)
	}
}

func TestKimiUpdateAdvisoryOffersUpdateOnlyForDefaultInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for binary, want := range map[string]string{
		home + "/.kimi-code/bin/kimi": "official-installer",
		// The legacy Python kimi-cli and a cargo build are not Kimi Code's install.
		home + "/.local/bin/kimi": "",
		home + "/.cargo/bin/kimi": "",
	} {
		s := newTestService("darwin", "bash")
		s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
			return VerifyResult{ResolvedPath: binary, Output: "2.0.2"}, nil
		})
		s.officialVersion = func(context.Context, Target) (string, error) { return "2.2.0", nil }
		advisory, err := s.UpdateAdvisory(t.Context(), TargetKimi, false)
		if err != nil {
			t.Fatal(err)
		}
		if advisory.MaintenanceMethod != want {
			t.Errorf("%s: maintenance method = %q, want %q (advisory %+v)", binary, advisory.MaintenanceMethod, want, advisory)
		}
	}
}

// The installer skips its legacy-shim migration, which renames or deletes
// other kimi commands on PATH, only while its own bin directory is off PATH.
func TestKimiUpdateKeepsItsBinDirectoryOffInstallerPath(t *testing.T) {
	for _, test := range []struct {
		goos, binary, home, pathList, want string
	}{
		{"darwin", "/Users/test/.kimi-code/bin/kimi", "/Users/test", "/usr/bin:/Users/test/.kimi-code/bin/:/Users/test/.local/bin:/bin", "PATH=/usr/bin:/Users/test/.local/bin:/bin"},
		{"windows", `C:\Users\Test\.kimi-code\bin\kimi.exe`, `C:\Users\Test`, `C:\Windows;c:\users\test\.KIMI-CODE\Bin;C:\tools`, `PATH=C:\Windows;C:\tools`},
	} {
		plan := bindKimiInstallerUpdate(test.goos, Plan{Target: TargetKimi, Method: "official-installer", Script: &ports.InstallScriptCommand{}}, test.binary, test.home, test.pathList)
		if plan.Unsupported || !slices.Contains(plan.Script.Env, test.want) {
			t.Errorf("%s: env = %v, want %q", test.goos, plan.Script.Env, test.want)
		}
	}
}

func TestLastOutputLineShowsWhatTheTerminalWouldShow(t *testing.T) {
	long := strings.Repeat("é", maxUpdaterReportRunes+50)
	for output, want := range map[string]string{
		"":            "",
		" \n\r\n\t\n": "",
		"Checking...\nDroid is already up to date.\n\n": "Droid is already up to date.",
		"\x1b[32m✓\x1b[0m Already up to date!\r\n":      "✓ Already up to date!",
		"Downloading 10%\rDownloading 100%\rDone\n":     "Done",
		"Checking...\nDownloading 50%\rDone\r":          "Done",
		"\x1b]0;title\x07Updated to 2.2.0\n":            "Updated to 2.2.0",
		long:                                            strings.Repeat("é", maxUpdaterReportRunes) + "…",
	} {
		if got := lastOutputLine(output); got != want {
			t.Errorf("lastOutputLine(%q) = %q, want %q", output, got, want)
		}
	}
}

func TestFailedUpdateQuotesWhatTheUpdaterReported(t *testing.T) {
	s := newTestService("darwin", "sh")
	s.commands = commandRunnerFunc(func(_ context.Context, _ []string, stdout, _ io.Writer) error {
		_, _ = io.WriteString(stdout, "Checking for updates...\nDroid is already up to date.\n\n")
		return nil
	})
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.local/bin/droid", Output: "0.159.1"}, nil
	})
	job := &Job{Target: TargetDroid, Method: "official-installer", Status: StatusInstalling}
	plan := Plan{Target: TargetDroid, Method: "official-installer", Command: []string{"/Users/test/.local/bin/droid", "update"}}
	baseline := &installedBaseline{path: "/Users/test/.local/bin/droid", version: "0.159.1", expected: "0.242.0", scheme: versionSchemeFor(TargetDroid)}
	s.runAgentOperation(context.Background(), plan, AgentOperationUpdate, job, baseline)
	want := `the update finished, but the installed version 0.159.1 did not reach the requested version 0.242.0; the update command reported: "Droid is already up to date."`
	if job.Status != StatusFailed || job.Error != want {
		t.Fatalf("status = %s, error = %q; want failed with %q", job.Status, job.Error, want)
	}
}

func TestOpenCodeVendorUpdatePinsAdvisoryRelease(t *testing.T) {
	s := newTestService("darwin", "bash")
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetOpencode: {AgentID: string(TargetOpencode), Status: UpdateStatusBehindLatest, CurrentVersion: "1.18.34", LatestVersion: "1.18.35", CheckedAt: time.Now()}}
	version := "1.18.34"
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.opencode/bin/opencode", Output: version}, nil
	})
	var ran []string
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
		ran = argv
		version = "1.18.35"
		return nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetOpencode, "official-installer", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetOpencode, StatusSucceeded)
	if want := []string{"/Users/test/.opencode/bin/opencode", "upgrade", "1.18.35", "--method", "curl"}; !slices.Equal(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
}

func TestAiderUpdatesThroughUVThatItsInstallerUses(t *testing.T) {
	s := newTestService("darwin", "sh", "uv")
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.resolveAgentMethod(TargetAider, "uv", AgentOperationUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"uv", "tool", "upgrade", "aider-chat"}; !slices.Equal(plan.Command, want) {
		t.Fatalf("Aider update = %v, want %v", plan.Command, want)
	}
	// The installer's binary lives in uv's tool layout, which the official
	// method can never own; uv is the method that can update it.
	if s.methodOwnsBinary(context.Background(), Plan{Method: "official-installer"}, "/Users/test/.local/share/uv/tools/aider-chat/bin/aider") {
		t.Fatal("official installer claimed Aider's uv-managed binary")
	}
}

func TestKimiListsHomebrewFormulaOnMacOS(t *testing.T) {
	s := newTestService("darwin", "bash", "brew")
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plans := planner.agentMethodPlans(TargetKimi, AgentOperationInstall)
	if len(plans) != 2 || plans[0].Method != "official-installer" || plans[1].Method != "homebrew" || plans[1].Package != "kimi-code" || plans[1].PackageCask {
		t.Fatalf("Kimi plans = %+v, want official installer then the kimi-code formula", plans)
	}
	update, err := planner.resolveAgentMethod(TargetKimi, "homebrew", AgentOperationUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"brew", "upgrade", "kimi-code"}; !slices.Equal(update.Command, want) {
		t.Fatalf("Kimi brew update = %v, want %v", update.Command, want)
	}
}

func TestOpenCodeVendorUpdatePinsApprovedReleaseOverAdvisory(t *testing.T) {
	s := newTestService("darwin", "bash")
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetOpencode: {AgentID: string(TargetOpencode), Status: UpdateStatusBehindLatest, CurrentVersion: "1.18.34", LatestVersion: "1.18.36", CheckedAt: time.Now()}}
	version := "1.18.34"
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.opencode/bin/opencode", Output: version}, nil
	})
	var ran []string
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
		ran = argv
		version = "1.18.35"
		return nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetOpencode, "official-installer", AgentOperationUpdate, "1.18.35"); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	if want := []string{"/Users/test/.opencode/bin/opencode", "upgrade", "1.18.35", "--method", "curl"}; !slices.Equal(ran, want) {
		t.Fatalf("ran %v, want the approved release %v", ran, want)
	}
	// Reaching the approved release is success, though 1.18.36 was announced.
	waitForStatus(t, s, TargetOpencode, StatusSucceeded)
}

func TestOpenCodeVendorUpdateNeverPinsAStaleCachedRelease(t *testing.T) {
	s := newTestService("darwin", "bash")
	// The cache still announces 1.18.35, but 1.18.36 was installed since.
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetOpencode: {AgentID: string(TargetOpencode), Status: UpdateStatusBehindLatest, CurrentVersion: "1.18.34", LatestVersion: "1.18.35", CheckedAt: time.Now()}}
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/Users/test/.opencode/bin/opencode", Output: "1.18.36"}, nil
	})
	var ran []string
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
		ran = argv
		return nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetOpencode, "official-installer", AgentOperationUpdate, ""); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	if slices.Contains(ran, "1.18.35") {
		t.Fatalf("ran %v, which installs the stale cached release over 1.18.36", ran)
	}
}

func TestOpenCodeVendorUpdateNeverPinsUnvettedApprovedRelease(t *testing.T) {
	newService := func(ran *[]string) *Service {
		s := newTestService("darwin", "bash")
		s.updateAdvisories = map[Target]UpdateAdvisory{TargetOpencode: {AgentID: string(TargetOpencode), Status: UpdateStatusBehindLatest, CurrentVersion: "1.18.34", LatestVersion: "1.18.36", CheckedAt: time.Now()}}
		s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
			return VerifyResult{ResolvedPath: "/Users/test/.opencode/bin/opencode", Output: "1.18.34"}, nil
		})
		s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
			*ran = argv
			return nil
		})
		return s
	}
	for _, downgrade := range []string{"0.1.0", "1.18.34"} {
		var ran []string
		s := newService(&ran)
		if _, err := s.StartAgentOperation(context.Background(), TargetOpencode, "official-installer", AgentOperationUpdate, downgrade); !errors.Is(err, ErrUpdateVersion) {
			t.Fatalf("expectedVersion %s: err = %v, want ErrUpdateVersion", downgrade, err)
		}
		s.workers.Wait()
		if ran != nil {
			t.Fatalf("expectedVersion %s ran %v", downgrade, ran)
		}
	}
	// A release the advisory has not seen is a verification floor only.
	var ran []string
	s := newService(&ran)
	if _, err := s.StartAgentOperation(context.Background(), TargetOpencode, "official-installer", AgentOperationUpdate, "9.9.9"); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	if want := []string{"/Users/test/.opencode/bin/opencode", "upgrade", "1.18.36", "--method", "curl"}; !slices.Equal(ran, want) {
		t.Fatalf("ran %v, want the advisory release %v", ran, want)
	}
}

type reviewerListerStub struct {
	handles []domain.ReviewerHandle
	chats   []domain.Review
}

func (s reviewerListerStub) ListLiveReviewerHandles(context.Context) ([]domain.ReviewerHandle, error) {
	return s.handles, nil
}

func (s reviewerListerStub) ListRecoverableChatReviews(context.Context) ([]domain.Review, error) {
	return s.chats, nil
}

func TestHarnessUpdateIsBlockedWhileItsReviewerRuns(t *testing.T) {
	for name, reviewers := range map[string]reviewerListerStub{
		"terminal reviewer": {handles: []domain.ReviewerHandle{{SessionID: "claude-worker", Harness: domain.ReviewerCodex, HandleID: "pane-1"}}},
		"chat reviewer":     {chats: []domain.Review{{SessionID: "claude-worker", Harness: domain.ReviewerCodex, InterfaceMode: domain.ReviewerInterfaceChat, ControllerGeneration: "gen-1"}}},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestService("darwin", "npm")
			// Updates run alongside live work unless AO_UPDATE_WHILE_RUNNING is off;
			// with it off a reviewer still counts as live work.
			s.updateWhileRunningDisabled = true
			// The worker runs Claude Code; only its reviewer uses Codex.
			s.sessions = sessionListerStub{sessions: []domain.SessionRecord{{ID: "claude-worker", Harness: domain.HarnessClaudeCode}}}
			s.reviewers = reviewers
			if _, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, ""); !errors.Is(err, ErrHarnessActive) {
				t.Fatalf("update error = %v, want ErrHarnessActive", err)
			}
		})
	}
	// A reviewer on another harness, or a reviewer chat with no live
	// controller, leaves the update to its other checks.
	s := newTestService("darwin", "npm")
	s.reviewers = reviewerListerStub{
		handles: []domain.ReviewerHandle{{Harness: domain.ReviewerClaudeCode, HandleID: "pane-1"}},
		chats:   []domain.Review{{Harness: domain.ReviewerCodex, InterfaceMode: domain.ReviewerInterfaceChat}},
	}
	if sessions, reviewers, err := s.harnessInUse(context.Background(), TargetCodex); err != nil || len(sessions)+len(reviewers) != 0 {
		t.Fatalf("harnessInUse = %v, %v, %v; want nothing in use", sessions, reviewers, err)
	}
}

func TestHomebrewUpdateRefreshesMetadataBeforeUpgrading(t *testing.T) {
	s := newTestService("darwin", "bash", "brew")
	s.updateAdvisories = map[Target]UpdateAdvisory{TargetKimi: {AgentID: string(TargetKimi), Status: UpdateStatusBehindLatest, CurrentVersion: "1.2.0", LatestVersion: "1.3.0", CheckedAt: time.Now()}}
	version := "1.2.0"
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: "/opt/homebrew/Cellar/kimi-code/1.2.0/bin/kimi", Output: version}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
	var ran [][]string
	s.commands = commandRunnerFunc(func(_ context.Context, argv []string, _, _ io.Writer) error {
		ran = append(ran, argv)
		if argv[1] == "update" {
			return errors.New("a tap failed to update")
		}
		version = "1.3.0"
		return nil
	})
	job, err := s.StartAgentOperation(context.Background(), TargetKimi, "homebrew", AgentOperationUpdate, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.Command != "brew update; brew upgrade kimi-code" {
		t.Fatalf("command = %q", job.Command)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetKimi, StatusSucceeded)
	if want := [][]string{{"brew", "update"}, {"brew", "upgrade", "kimi-code"}}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("ran %v, want %v", ran, want)
	}
	final, _ := s.Status(context.Background(), TargetKimi)
	if !strings.Contains(final.Output, "brew update failed") {
		t.Fatalf("output %q does not note the failed refresh", final.Output)
	}
}
