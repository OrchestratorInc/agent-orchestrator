// Package spawnpreflight checks, before a session spawn, the launch facts that
// otherwise surface as dead-end spawn failures on a new install: the selected
// agent harness is installed and signed in, the terminal runtime prerequisite
// exists, and the project's default branch resolves. Each check reuses the
// detection the spawn path itself uses; this package only composes them and
// attaches the reviewed, OS-specific fix the desktop shows.
package spawnpreflight

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reqid"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentauth"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Blocker is a stable, enumerated pre-flight failure. Values are telemetry
// enums and API contract; never derive one from free text.
type Blocker string

const (
	// BlockerAgentNotInstalled maps to the spawn error AGENT_BINARY_NOT_FOUND.
	BlockerAgentNotInstalled Blocker = "agent_not_installed"
	// BlockerAgentUnauthorized means the harness's own probe reports it signed out.
	BlockerAgentUnauthorized Blocker = "agent_unauthorized"
	// BlockerRuntimePrerequisiteMissing maps to RUNTIME_PREREQUISITE_MISSING.
	BlockerRuntimePrerequisiteMissing Blocker = "runtime_prerequisite_missing"
	// BlockerDefaultBranchUnresolved maps to DEFAULT_BRANCH_UNRESOLVED.
	BlockerDefaultBranchUnresolved Blocker = "default_branch_unresolved"
	// BlockerAgentRequired means neither the request nor the project names a harness.
	BlockerAgentRequired Blocker = "agent_required"
)

var knownBlockers = map[Blocker]bool{
	BlockerAgentNotInstalled: true, BlockerAgentUnauthorized: true,
	BlockerRuntimePrerequisiteMissing: true, BlockerDefaultBranchUnresolved: true,
	BlockerAgentRequired: true,
}

// ResolutionMethod is how the user cleared a blocker from the pre-flight UI.
type ResolutionMethod string

var knownMethods = map[ResolutionMethod]bool{
	"install": true, "login": true, "switch_agent": true, "set_default_branch": true, "recheck": true,
}

// Request is the body of POST /api/v1/sessions/preflight.
type Request struct {
	ProjectID string `json:"projectId" description:"Project the spawn targets."`
	Kind      string `json:"kind" enum:"orchestrator,worker" description:"Session kind the caller is about to spawn."`
	Harness   string `json:"harness,omitempty" description:"Harness the spawn will request. Empty resolves the project's role override, as the spawn does."`
	Mode      string `json:"mode,omitempty" enum:"chat,tui" description:"Controller mode the spawn will request. Empty resolves the daemon default, as the spawn does."`
	// Resolution reports that this check follows a blocked one, so the daemon
	// can count which blockers the user cleared and how.
	Resolution *Resolution `json:"resolution,omitempty"`
}

// Resolution names the blockers a previous check reported and the fix the
// user applied before re-checking.
type Resolution struct {
	Blockers []string `json:"blockers" maxItems:"8" description:"Blocker codes the previous check reported."`
	Method   string   `json:"method" enum:"install,login,switch_agent,set_default_branch,recheck" description:"What the user did before re-checking."`
}

// Check is one failed pre-flight requirement and its fix.
type Check struct {
	Code Blocker `json:"code" enum:"agent_not_installed,agent_unauthorized,runtime_prerequisite_missing,default_branch_unresolved,agent_required"`
	// Blocking is true when the spawn is certain to fail on this check. A soft
	// check (a signed-out probe the spawn itself does not enforce) may be
	// overridden by the user.
	Blocking bool `json:"blocking"`
	// Command is the display-only install or sign-in command for this OS.
	Command          string `json:"command,omitempty"`
	DocumentationURL string `json:"documentationUrl,omitempty"`
}

// Report is the response of POST /api/v1/sessions/preflight.
type Report struct {
	Ready bool `json:"ready" description:"True when no check failed."`
	// CanProceed is true when every failed check is non-blocking.
	CanProceed bool    `json:"canProceed"`
	Kind       string  `json:"kind" enum:"orchestrator,worker"`
	Harness    string  `json:"harness,omitempty" description:"Harness the spawn would launch."`
	Mode       string  `json:"mode,omitempty" enum:"chat,tui"`
	Checks     []Check `json:"checks"`
}

// SpawnFacts resolves what the spawn would decide; *sessionmanager.Manager in production.
type SpawnFacts interface {
	PreflightSpawn(ctx context.Context, in sessionmanager.SpawnPreflightInput) (sessionmanager.SpawnPreflightFacts, error)
}

// InstallPlans is the agent installer catalog; *systeminstall.Service in production.
type InstallPlans interface {
	AgentPlans(ctx context.Context) ([]systeminstall.AgentPlan, error)
}

// AuthPlans resolves sign-in plans; *agentauth.Service in production.
type AuthPlans interface {
	Plan(ctx context.Context, agentID string) (agentauth.Plan, error)
}

// Deps wires the service. Facts and Readiness are required.
type Deps struct {
	Facts     SpawnFacts
	Readiness ports.AgentReadinessProvider
	Installs  InstallPlans
	Auth      AuthPlans
	Telemetry ports.EventSink
	// GOOS, Executable, and LookPath default to the running process; tests
	// override them to cover every platform.
	GOOS       string
	Executable func() (string, error)
	LookPath   func(string) (string, error)
	Now        func() time.Time
	// BranchTimeout bounds default-branch resolution, which may contact the
	// remote. A timed-out probe is unknown and never blocks.
	BranchTimeout time.Duration
}

// Service runs spawn pre-flight checks.
type Service struct{ d Deps }

const defaultBranchTimeout = 15 * time.Second

// New returns a Service.
func New(d Deps) *Service {
	if d.GOOS == "" {
		d.GOOS = runtime.GOOS
	}
	if d.Executable == nil {
		d.Executable = os.Executable
	}
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.BranchTimeout <= 0 {
		d.BranchTimeout = defaultBranchTimeout
	}
	return &Service{d: d}
}

// Check runs the pre-flight for one prospective spawn and records the outcome.
func (s *Service) Check(ctx context.Context, req Request) (Report, error) {
	kind := domain.SessionKind(req.Kind)
	if kind != domain.KindOrchestrator && kind != domain.KindWorker {
		return Report{}, apierr.Invalid("INVALID_KIND", "kind must be orchestrator or worker", nil)
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return Report{}, apierr.Invalid("PROJECT_REQUIRED", "projectId is required", nil)
	}
	mode := domain.SessionMode(req.Mode)
	if req.Mode != "" && !mode.Valid() {
		return Report{}, apierr.Invalid("INVALID_MODE", "mode must be chat or tui", nil)
	}
	if s.d.Facts == nil {
		return Report{}, apierr.Internal("SPAWN_PREFLIGHT_UNAVAILABLE", "Spawn pre-flight is unavailable")
	}
	branchCtx, cancel := context.WithTimeout(ctx, s.d.BranchTimeout)
	defer cancel()
	facts, err := s.d.Facts.PreflightSpawn(branchCtx, sessionmanager.SpawnPreflightInput{
		ProjectID: domain.ProjectID(req.ProjectID), Kind: kind,
		Harness: domain.AgentHarness(req.Harness), Mode: mode,
	})
	if err != nil {
		if errors.Is(err, sessionmanager.ErrProjectNotResolvable) {
			return Report{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
		}
		// A probe that ran out of time says nothing about the spawn.
		if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return Report{}, err
		}
	}
	report := Report{Kind: req.Kind, Harness: string(facts.Harness), Mode: string(facts.Mode), Checks: []Check{}}
	if facts.Harness == "" {
		report.Checks = append(report.Checks, Check{Code: BlockerAgentRequired, Blocking: true})
	} else {
		report.Checks = append(report.Checks, s.agentChecks(ctx, facts.Harness)...)
	}
	if facts.Mode == domain.SessionModeTUI {
		if err := sessionmanager.RuntimePrerequisiteError(s.d.GOOS, s.d.Executable, s.d.LookPath); err != nil {
			report.Checks = append(report.Checks, s.tmuxCheck())
		}
	}
	if facts.DefaultBranchUnresolved {
		report.Checks = append(report.Checks, Check{Code: BlockerDefaultBranchUnresolved, Blocking: true})
	}
	report.Ready = len(report.Checks) == 0
	report.CanProceed = !slices.ContainsFunc(report.Checks, func(c Check) bool { return c.Blocking })
	s.emit(ctx, req, report)
	return report, nil
}

func (s *Service) agentChecks(ctx context.Context, harness domain.AgentHarness) []Check {
	if s.d.Readiness == nil {
		return nil
	}
	snapshot, err := s.d.Readiness.EnsureAgentReadiness(ctx, string(harness), domain.AgentReadinessPurposeLaunch)
	if err != nil {
		// An unavailable readiness check is not proof the harness is missing.
		return nil
	}
	switch {
	case snapshot.Installation.State == domain.AgentInstallationNotInstalled:
		return []Check{s.installCheck(ctx, harness)}
	case snapshot.Installation.State == domain.AgentInstallationInstalled &&
		snapshot.Authentication.State == domain.AgentAuthenticationUnauthorized &&
		snapshot.Authentication.Freshness == domain.AgentReadinessFresh:
		return []Check{s.loginCheck(ctx, harness)}
	}
	return nil
}

func (s *Service) installCheck(ctx context.Context, harness domain.AgentHarness) Check {
	check := Check{Code: BlockerAgentNotInstalled, Blocking: true}
	if s.d.Installs == nil {
		return check
	}
	plans, err := s.d.Installs.AgentPlans(ctx)
	if err != nil {
		return check
	}
	for _, plan := range plans {
		if plan.AgentID != string(harness) {
			continue
		}
		check.Command = plan.Command
		check.DocumentationURL = plan.DocumentationURL
		break
	}
	return check
}

func (s *Service) loginCheck(ctx context.Context, harness domain.AgentHarness) Check {
	// Spawn itself refuses only a signed-out Codex; every other harness may
	// still authenticate from the environment, so a signed-out probe is advice.
	check := Check{Code: BlockerAgentUnauthorized, Blocking: harness == domain.HarnessCodex}
	if s.d.Auth == nil {
		return check
	}
	plan, err := s.d.Auth.Plan(ctx, string(harness))
	if err != nil {
		return check
	}
	check.Command = plan.DisplayCommand
	check.DocumentationURL = plan.DocumentationURL
	return check
}

func (s *Service) tmuxCheck() Check {
	plan := systeminstall.Resolve(s.d.GOOS, s.d.LookPath, systeminstall.TargetTmux)
	check := Check{Code: BlockerRuntimePrerequisiteMissing, Blocking: true, DocumentationURL: plan.DocsURL}
	if !plan.Unsupported {
		check.Command = systeminstall.DisplayCommand(plan)
	}
	return check
}

func (s *Service) emit(ctx context.Context, req Request, report Report) {
	if s.d.Telemetry == nil {
		return
	}
	projectID := domain.ProjectID(req.ProjectID)
	blockers := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		blockers = append(blockers, string(check.Code))
	}
	slices.Sort(blockers)
	result := "pass"
	if !report.Ready {
		result = "blocked"
	}
	payload := map[string]any{
		"result":        result,
		"blocker_count": len(blockers),
		"kind":          report.Kind,
	}
	if len(blockers) > 0 {
		payload["blockers"] = strings.Join(blockers, ",")
	}
	if report.Harness != "" {
		payload["harness"] = report.Harness
	}
	s.d.Telemetry.Emit(context.Background(), ports.TelemetryEvent{
		Name: "ao.session.preflight_checked", Source: "spawn_preflight", OccurredAt: s.d.Now(),
		Level: ports.TelemetryLevelInfo, ProjectID: &projectID, RequestID: reqid.FromContext(ctx), Payload: payload,
	})
	if req.Resolution == nil || !knownMethods[ResolutionMethod(req.Resolution.Method)] {
		return
	}
	remaining := make(map[Blocker]bool, len(report.Checks))
	for _, check := range report.Checks {
		remaining[check.Code] = true
	}
	seen := map[Blocker]bool{}
	for _, raw := range req.Resolution.Blockers {
		blocker := Blocker(raw)
		if !knownBlockers[blocker] || remaining[blocker] || seen[blocker] {
			continue
		}
		seen[blocker] = true
		resolved := map[string]any{"blocker": string(blocker), "method": req.Resolution.Method, "kind": report.Kind}
		if report.Harness != "" {
			resolved["harness"] = report.Harness
		}
		s.d.Telemetry.Emit(context.Background(), ports.TelemetryEvent{
			Name: "ao.session.preflight_resolved", Source: "spawn_preflight", OccurredAt: s.d.Now(),
			Level: ports.TelemetryLevelInfo, ProjectID: &projectID, RequestID: reqid.FromContext(ctx), Payload: resolved,
		})
	}
}
