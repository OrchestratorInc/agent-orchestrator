package spawnpreflight

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentauth"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type fakeFacts struct {
	facts sessionmanager.SpawnPreflightFacts
	err   error
	got   sessionmanager.SpawnPreflightInput
}

func (f *fakeFacts) PreflightSpawn(_ context.Context, in sessionmanager.SpawnPreflightInput) (sessionmanager.SpawnPreflightFacts, error) {
	f.got = in
	return f.facts, f.err
}

type fakeReadiness struct {
	snapshot    domain.AgentReadinessSnapshot
	err         error
	invalidated *[]string
}

func (f fakeReadiness) EnsureAgentReadiness(context.Context, string, domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	return f.snapshot, f.err
}
func (f fakeReadiness) InvalidateAgentInstallation(id string) {
	if f.invalidated != nil {
		*f.invalidated = append(*f.invalidated, "installation:"+id)
	}
}
func (f fakeReadiness) InvalidateAgentAuthentication(id string) {
	if f.invalidated != nil {
		*f.invalidated = append(*f.invalidated, "authentication:"+id)
	}
}
func (fakeReadiness) RecheckAgent(string) {}

type fakeInstalls map[systeminstall.Target][2]string

func (f fakeInstalls) AgentInstallCommand(_ context.Context, target systeminstall.Target) (string, string, error) {
	plan, ok := f[target]
	if !ok {
		return "", "", errors.New("unknown target")
	}
	return plan[0], plan[1], nil
}

type fakeAuth struct{ plan agentauth.Plan }

func (f fakeAuth) Plan(context.Context, string) (agentauth.Plan, error) { return f.plan, nil }

type recordingSink struct{ events []ports.TelemetryEvent }

func (s *recordingSink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	s.events = append(s.events, ev)
}
func (s *recordingSink) Close(context.Context) error { return nil }

func ready() fakeReadiness {
	return fakeReadiness{snapshot: domain.AgentReadinessSnapshot{
		Installation:   domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled, Freshness: domain.AgentReadinessFresh},
		Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized, Freshness: domain.AgentReadinessFresh},
	}}
}

func noTmux(name string) (string, error) { return "", fmt.Errorf("exec: %q: not found", name) }

// brewOnly resolves Homebrew and apt-get but never tmux, so each OS gets its
// own package-manager plan.
func brewOnly(name string) (string, error) {
	switch name {
	case "brew":
		return "/opt/homebrew/bin/brew", nil
	case "apt-get":
		return "/usr/bin/apt-get", nil
	}
	return noTmux(name)
}

func newService(t *testing.T, d Deps) (*Service, *recordingSink) {
	t.Helper()
	t.Setenv("AO_TMUX_BINARY", "")
	sink := &recordingSink{}
	if d.Telemetry == nil {
		d.Telemetry = sink
	}
	if d.Executable == nil {
		d.Executable = func() (string, error) { return "", errors.New("no executable") }
	}
	return New(d), sink
}

func TestCheckPassesAndEmitsPass(t *testing.T) {
	facts := &fakeFacts{facts: sessionmanager.SpawnPreflightFacts{Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat}}
	svc, sink := newService(t, Deps{Facts: facts, Readiness: ready(), GOOS: "darwin", LookPath: noTmux})

	report, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "orchestrator"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Chat launches never need tmux, even on macOS.
	if !report.Ready || !report.CanProceed || len(report.Checks) != 0 || report.Harness != "claude-code" {
		t.Fatalf("report = %+v, want ready claude-code", report)
	}
	if facts.got.Kind != domain.KindOrchestrator || facts.got.ProjectID != "p1" {
		t.Fatalf("facts input = %+v", facts.got)
	}
	if len(sink.events) != 1 || sink.events[0].Name != "ao.session.preflight_checked" {
		t.Fatalf("events = %+v, want one preflight_checked", sink.events)
	}
	payload := sink.events[0].Payload
	if payload["result"] != "pass" || payload["blocker_count"] != 0 || payload["kind"] != "orchestrator" || payload["harness"] != "claude-code" {
		t.Fatalf("payload = %+v", payload)
	}
	if _, ok := payload["blockers"]; ok {
		t.Fatalf("payload carries blockers on pass: %+v", payload)
	}
}

func TestRuntimePrerequisitePerOS(t *testing.T) {
	cases := []struct {
		goos        string
		wantBlocked bool
		wantCommand string
	}{
		{goos: "windows"},
		{goos: "linux"},
		{goos: "darwin", wantBlocked: true, wantCommand: "brew install tmux"},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			facts := &fakeFacts{facts: sessionmanager.SpawnPreflightFacts{Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI}}
			svc, _ := newService(t, Deps{Facts: facts, Readiness: ready(), GOOS: tc.goos, LookPath: brewOnly})
			report, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "worker"})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if !tc.wantBlocked {
				if !report.Ready {
					t.Fatalf("report = %+v, want ready on %s", report, tc.goos)
				}
				return
			}
			if report.Ready || report.CanProceed || len(report.Checks) != 1 {
				t.Fatalf("report = %+v, want one blocking check", report)
			}
			check := report.Checks[0]
			if check.Code != BlockerRuntimePrerequisiteMissing || !check.Blocking || !strings.Contains(check.Command, tc.wantCommand) {
				t.Fatalf("check = %+v, want tmux install via %q", check, tc.wantCommand)
			}
		})
	}
}

func TestAgentNotInstalledCarriesInstallerCommand(t *testing.T) {
	facts := &fakeFacts{facts: sessionmanager.SpawnPreflightFacts{Harness: domain.HarnessCodex, Mode: domain.SessionModeChat}}
	readiness := fakeReadiness{snapshot: domain.AgentReadinessSnapshot{
		Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled, Freshness: domain.AgentReadinessFresh},
	}}
	installs := fakeInstalls{
		"claude-code": {"npm install -g @anthropic-ai/claude-code", ""},
		"codex":       {"npm install -g @openai/codex", "https://example.test/codex"},
	}
	svc, sink := newService(t, Deps{Facts: facts, Readiness: readiness, Installs: installs, GOOS: "windows", LookPath: noTmux})

	report, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "orchestrator"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Checks) != 1 {
		t.Fatalf("checks = %+v", report.Checks)
	}
	check := report.Checks[0]
	if check.Code != BlockerAgentNotInstalled || !check.Blocking || check.Command != "npm install -g @openai/codex" || check.DocumentationURL == "" {
		t.Fatalf("check = %+v", check)
	}
	if sink.events[0].Payload["blockers"] != "agent_not_installed" || sink.events[0].Payload["result"] != "blocked" {
		t.Fatalf("payload = %+v", sink.events[0].Payload)
	}
}

func TestSignedOutProbeBlocksOnlyCodex(t *testing.T) {
	signedOut := fakeReadiness{snapshot: domain.AgentReadinessSnapshot{
		Installation:   domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled, Freshness: domain.AgentReadinessFresh},
		Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh},
	}}
	auth := fakeAuth{plan: agentauth.Plan{DisplayCommand: "claude /login"}}
	for _, tc := range []struct {
		harness      domain.AgentHarness
		wantBlocking bool
	}{{domain.HarnessClaudeCode, false}, {domain.HarnessCodex, true}} {
		facts := &fakeFacts{facts: sessionmanager.SpawnPreflightFacts{Harness: tc.harness, Mode: domain.SessionModeChat}}
		svc, _ := newService(t, Deps{Facts: facts, Readiness: signedOut, Auth: auth, GOOS: "linux", LookPath: noTmux})
		report, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "orchestrator"})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if len(report.Checks) != 1 || report.Checks[0].Code != BlockerAgentUnauthorized {
			t.Fatalf("%s: checks = %+v", tc.harness, report.Checks)
		}
		check := report.Checks[0]
		if check.Blocking != tc.wantBlocking || report.CanProceed == tc.wantBlocking || check.Command != "claude /login" {
			t.Fatalf("%s: check = %+v canProceed=%v", tc.harness, check, report.CanProceed)
		}
	}
}

func TestStaleOrFailedReadinessNeverBlocks(t *testing.T) {
	facts := &fakeFacts{facts: sessionmanager.SpawnPreflightFacts{Harness: domain.HarnessCodex, Mode: domain.SessionModeChat}}
	stale := fakeReadiness{snapshot: domain.AgentReadinessSnapshot{
		Installation:   domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
		Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessStale},
	}}
	for _, readiness := range []fakeReadiness{stale, {err: errors.New("probe failed")}} {
		svc, _ := newService(t, Deps{Facts: facts, Readiness: readiness, GOOS: "linux", LookPath: noTmux})
		report, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "worker"})
		if err != nil || !report.Ready {
			t.Fatalf("report, err = %+v, %v; want ready", report, err)
		}
	}
}

func TestDefaultBranchAndMissingHarness(t *testing.T) {
	facts := &fakeFacts{facts: sessionmanager.SpawnPreflightFacts{DefaultBranchUnresolved: true}}
	svc, sink := newService(t, Deps{Facts: facts, Readiness: ready(), GOOS: "darwin", LookPath: noTmux})
	report, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "orchestrator"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Checks) != 2 || report.Checks[0].Code != BlockerAgentRequired || report.Checks[1].Code != BlockerDefaultBranchUnresolved {
		t.Fatalf("checks = %+v", report.Checks)
	}
	if got := sink.events[0].Payload["blockers"]; got != "agent_required,default_branch_unresolved" {
		t.Fatalf("blockers = %v, want sorted enum list", got)
	}
	if _, ok := sink.events[0].Payload["harness"]; ok {
		t.Fatalf("payload has harness without one: %+v", sink.events[0].Payload)
	}
}

func TestBranchProbeTimeoutIsUnknown(t *testing.T) {
	facts := &fakeFacts{
		facts: sessionmanager.SpawnPreflightFacts{Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat},
		err:   context.DeadlineExceeded,
	}
	svc, _ := newService(t, Deps{Facts: facts, Readiness: ready(), GOOS: "linux", LookPath: noTmux})
	report, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "worker"})
	if err != nil || !report.Ready {
		t.Fatalf("report, err = %+v, %v; want a timed-out probe to pass", report, err)
	}
}

func TestResolutionEmitsOnlyClearedKnownBlockers(t *testing.T) {
	facts := &fakeFacts{facts: sessionmanager.SpawnPreflightFacts{Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, DefaultBranchUnresolved: true}}
	var invalidated []string
	readiness := ready()
	readiness.invalidated = &invalidated
	svc, sink := newService(t, Deps{Facts: facts, Readiness: readiness, GOOS: "windows", LookPath: noTmux})
	_, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "orchestrator", Resolution: &Resolution{
		Method:   "switch_agent",
		Blockers: []string{"agent_not_installed", "agent_not_installed", "default_branch_unresolved", "free text"},
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(sink.events) != 2 || sink.events[1].Name != "ao.session.preflight_resolved" {
		t.Fatalf("events = %+v, want checked + one resolved", sink.events)
	}
	resolved := sink.events[1].Payload
	if resolved["blocker"] != "agent_not_installed" || resolved["method"] != "switch_agent" || resolved["harness"] != "claude-code" || resolved["kind"] != "orchestrator" {
		t.Fatalf("resolved = %+v", resolved)
	}
	// A re-check must not be answered by the observation from before the fix.
	if strings.Join(invalidated, ",") != "installation:claude-code,authentication:claude-code" {
		t.Fatalf("invalidated = %v, want fresh install and auth checks", invalidated)
	}

	sink.events = nil
	if _, err := svc.Check(context.Background(), Request{ProjectID: "p1", Kind: "orchestrator", Resolution: &Resolution{Method: "bogus", Blockers: []string{"agent_not_installed"}}}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("unknown method emitted %+v", sink.events)
	}
}

func TestCheckValidatesRequest(t *testing.T) {
	svc, _ := newService(t, Deps{Facts: &fakeFacts{}, GOOS: "linux", LookPath: noTmux})
	for _, req := range []Request{
		{ProjectID: "p1", Kind: "reviewer"},
		{Kind: "worker"},
		{ProjectID: "p1", Kind: "worker", Mode: "gui"},
	} {
		_, err := svc.Check(context.Background(), req)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("%+v: err = %v, want API error", req, err)
		}
	}
	missing := &fakeFacts{err: fmt.Errorf("preflight: %w", sessionmanager.ErrProjectNotResolvable)}
	svc, _ = newService(t, Deps{Facts: missing, GOOS: "linux", LookPath: noTmux})
	_, err := svc.Check(context.Background(), Request{ProjectID: "gone", Kind: "worker"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want PROJECT_NOT_FOUND", err)
	}
}
