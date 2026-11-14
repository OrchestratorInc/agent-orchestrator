package systeminstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	ErrHarnessActive = errors.New("systeminstall: harness has an active session")
	ErrInstallMethod = errors.New("systeminstall: invalid install method")
	ErrInstallActive = errors.New("systeminstall: harness install operation already active")
	ErrInstallOwner  = errors.New("systeminstall: installation owner not confirmed")
	ErrUpdateVersion = errors.New("systeminstall: invalid expected update version")
)

type Target string

const (
	TargetTmux        Target = "tmux"
	TargetGH          Target = "gh"
	TargetClaude      Target = "claude"
	TargetClaudeCode  Target = "claude-code"
	TargetCodex       Target = "codex"
	TargetCursor      Target = "cursor"
	TargetOpencode    Target = "opencode"
	TargetOpencodeV2  Target = "opencode-v2"
	TargetAider       Target = "aider"
	TargetCopilot     Target = "copilot"
	TargetGrok        Target = "grok"
	TargetKimi        Target = "kimi"
	TargetPi          Target = "pi"
	TargetAmp         Target = "amp"
	TargetAuggie      Target = "auggie"
	TargetDroid       Target = "droid"
	TargetCrush       Target = "crush"
	TargetCline       Target = "cline"
	TargetGoose       Target = "goose"
	TargetQwen        Target = "qwen"
	TargetGemini      Target = "gemini"
	TargetContinue    Target = "continue"
	TargetDevin       Target = "devin"
	TargetKiro        Target = "kiro"
	TargetKilocode    Target = "kilocode"
	TargetVibe        Target = "vibe"
	TargetMuse        Target = "muse"
	TargetAgy         Target = "agy"
	TargetAutohand    Target = "autohand"
	TargetKimchi      Target = "kimchi"
	TargetPrimeAgent  Target = "prime-agent"
	TargetOMP         Target = "omp"
	TargetFX          Target = "fx"
	TargetUnreal      Target = "unreal-agent"
	TargetCodewhale   Target = "codewhale"
	TargetMiMoCode    Target = "mimo-code"
	TargetMiniMaxCode Target = "minimax-code"
	TargetDeepSeek    Target = "deepseek-harness"
	TargetOpenHands   Target = "openhands"
	TargetCommandCode Target = "command-code"
	TargetDockerAgent Target = "docker-agent"
	TargetMemcode     Target = "memcode"
	TargetHermes      Target = "hermes"
	TargetStakpak     Target = "stakpak"
	TargetCloudflared Target = "cloudflared"
	TargetCuaDriver   Target = "cua-driver"
)

var agentTargets = []Target{
	TargetClaudeCode, TargetCodex, TargetCursor, TargetOpencode, TargetOpencodeV2, TargetAider,
	TargetCopilot, TargetGrok, TargetKimi, TargetPi, TargetAmp, TargetAuggie,
	TargetDroid, TargetCrush, TargetCline, TargetGoose, TargetQwen, TargetGemini,
	TargetContinue, TargetDevin, TargetKiro, TargetKilocode, TargetVibe,
	TargetMuse, TargetAgy, TargetAutohand, TargetKimchi, TargetPrimeAgent,
	TargetOMP, TargetFX, TargetUnreal, TargetCodewhale, TargetMiMoCode, TargetMiniMaxCode, TargetDeepSeek, TargetOpenHands,
	TargetCommandCode, TargetDockerAgent, TargetMemcode, TargetHermes, TargetStakpak,
}

var agentTargetSet = func() map[Target]bool {
	out := make(map[Target]bool, len(agentTargets))
	for _, target := range agentTargets {
		out[target] = true
	}
	return out
}()

var systemTargetSet = map[Target]bool{
	TargetTmux: true, TargetGH: true, TargetClaude: true, TargetCloudflared: true,
	TargetCuaDriver: true, TargetCodex: true, TargetOpencode: true, TargetOpencodeV2: true, TargetCopilot: true,
}

var knownTargets = func() map[Target]bool {
	out := make(map[Target]bool, len(systemTargetSet)+len(agentTargets))
	for target := range systemTargetSet {
		out[target] = true
	}
	for _, target := range agentTargets {
		out[target] = true
	}
	return out
}()

func IsAgentTarget(target Target) bool { return agentTargetSet[target] }

func IsSystemTarget(target Target) bool { return systemTargetSet[target] }

func Valid(target Target) bool {
	return knownTargets[target]
}

func Resolve(goos string, lookPath func(string) (string, error), target Target) Plan {
	if !Valid(target) {
		return Plan{Target: target, Unsupported: true, Reason: "unknown install target"}
	}
	return (&Service{goos: goos, executables: executableFinderFunc(lookPath)}).resolvePlan(target)
}

type executableFinderFunc func(string) (string, error)

func (f executableFinderFunc) LookPath(file string) (string, error) { return f(file) }

type Plan struct {
	Target              Target
	Command             []string
	Prepare             [][]string
	Cleanup             [][]string
	Script              *ports.InstallScriptCommand
	Package             string
	PackagePrefix       string
	PackageCask         bool
	Manager             string
	NeedsRoot           bool
	Unsupported         bool
	Reason              string
	Notice              string
	Method              string
	DocsURL             string
	ExpectedDestination string
	UpdateWhileRunning  UpdateWhileRunning
	keepPreviousRelease bool
	managed             bool
	managedPin          string
	download            *ports.ManagedBinaryDownload
	sourceMethod        string
	sourcePackage       string
	alsoChanges         []Target
	homebrewV1Prefix    string

	daemonRuntimeRejected bool
	daemonPrefixRejected  bool
	deferredReason        string
	ownerPrefixFound      bool
}

func (plan Plan) ownershipMethod() string {
	if plan.sourceMethod != "" {
		return plan.sourceMethod
	}
	return plan.Method
}

type AgentPlan struct {
	AgentID             string               `json:"agentId"`
	Available           bool                 `json:"available"`
	Automatic           bool                 `json:"automatic"`
	Method              string               `json:"method"`
	Command             string               `json:"command,omitempty"`
	Reason              string               `json:"reason,omitempty"`
	Notice              string               `json:"notice,omitempty"`
	DocumentationURL    string               `json:"documentationUrl"`
	ExpectedDestination string               `json:"expectedDestination,omitempty"`
	Methods             []AgentInstallMethod `json:"methods"`
}

type AgentInstallMethod struct {
	ID                  string `json:"id"`
	Label               string `json:"label"`
	Available           bool   `json:"available"`
	Recommended         bool   `json:"recommended"`
	Command             string `json:"command,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Notice              string `json:"notice,omitempty"`
	ExpectedDestination string `json:"expectedDestination,omitempty"`
	ReinstallAvailable  bool   `json:"reinstallAvailable"`
	ReinstallCommand    string `json:"reinstallCommand,omitempty"`
	ReinstallReason     string `json:"reinstallReason,omitempty"`
	UpdateAvailable     bool   `json:"updateAvailable"`
	UpdateCommand       string `json:"updateCommand,omitempty"`
	UpdateReason        string `json:"updateReason,omitempty"`
}

type AgentOperation string

const (
	AgentOperationInstall   AgentOperation = "install"
	AgentOperationReinstall AgentOperation = "reinstall"
	AgentOperationUpdate    AgentOperation = "update"
)

func (operation AgentOperation) valid() bool {
	return operation == AgentOperationInstall || operation == AgentOperationReinstall || operation == AgentOperationUpdate
}

type Status string

const (
	StatusIdle           Status = "idle"
	StatusRunning        Status = "running"
	StatusWaitingForIdle Status = "waiting_for_idle"
	StatusInstalling     Status = "installing"
	StatusVerifying      Status = "verifying"
	StatusSucceeded      Status = "succeeded"
	StatusFailed         Status = "failed"
	StatusUnsupported    Status = "unsupported"
	StatusInterrupted    Status = "interrupted"
)

const maxOutputBytes = 4000

const defaultInstallTimeout = 15 * time.Minute

const cleanupTimeout = 2 * time.Minute

const otherHarnessHoldWait = 10 * time.Second

const defaultPersistenceTimeout = 2 * time.Second

var devinInstalledLine = regexp.MustCompile(`Installed devin v\S+ to [^\r\n]+/devin\.`)

type Job struct {
	Target              Target               `json:"target" enum:"tmux,gh,claude,claude-code,codex,cursor,opencode,opencode-v2,aider,copilot,grok,kimi,pi,amp,auggie,droid,crush,cline,goose,qwen,gemini,continue,devin,kiro,kilocode,vibe,muse,agy,autohand,kimchi,prime-agent,omp,fx,unreal-agent,codewhale,mimo-code,minimax-code,deepseek-harness,openhands,command-code,docker-agent,memcode,hermes,stakpak,cloudflared,cua-driver" description:"Fixed install target this job ran (or is running) for."`
	Status              Status               `json:"status" enum:"idle,running,waiting_for_idle,installing,verifying,succeeded,failed,unsupported,interrupted" description:"Current lifecycle state of the job. waiting_for_idle: the update runs once every session using the harness is idle (Windows)."`
	Method              string               `json:"method,omitempty" description:"Server-owned installation method selected for this harness job."`
	Command             string               `json:"command,omitempty" description:"Human-readable install command, e.g. \"brew install tmux\", for display even before/without output."`
	ExpectedDestination string               `json:"expectedDestination,omitempty" description:"Expected or adapter-resolved executable destination."`
	Output              string               `json:"output,omitempty" description:"Combined stdout+stderr from the install command, tail-capped to the last ~4000 bytes. Published while the command runs."`
	Progress            *Progress            `json:"progress,omitempty" description:"While installing, the phase and percentage the installer's output reports. Absent in every other status."`
	Error               string               `json:"error,omitempty" description:"Set on failure or when the target is unsupported on this machine: the exec error, the Unsupported reason, or a timeout message."`
	Notice              string               `json:"notice,omitempty" description:"Fixed user-visible consequence to acknowledge before running this install."`
	WaitingOn           []domain.SessionWait `json:"waitingOn,omitempty" description:"While waiting_for_idle, the sessions the update still waits for and why."`
	StartedAt           *time.Time           `json:"startedAt,omitempty"`
	FinishedAt          *time.Time           `json:"finishedAt,omitempty" description:"Absent until the job finishes."`
	UpdatedAt           *time.Time           `json:"updatedAt,omitempty"`
}

type HarnessVerifier interface {
	Verify(ctx context.Context, target Target) (VerifyResult, error)
}

type SessionLister interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type ReviewerLister interface {
	ListLiveReviewerHandles(ctx context.Context) ([]domain.ReviewerHandle, error)
	ListRecoverableChatReviews(ctx context.Context) ([]domain.Review, error)
}

type Deps struct {
	JobStore                  ports.AgentInstallJobStore
	Verifier                  HarnessVerifier
	Sessions                  SessionLister
	Reviewers                 ReviewerLister
	DisableUpdateChecks       bool
	DisableUpdateWhileRunning bool
	ManagedRoot               string
	Releases                  ManagedReleaseStore
	UpdateWhenIdle            bool
	PrivateNPMPrefixes        map[Target]string
	PrivateInstallerHomes     map[Target]string
}

type Service struct {
	mu                sync.Mutex
	jobs              map[Target]*Job
	backgroundContext context.Context
	stop              context.CancelFunc
	stopping          bool
	workers           sync.WaitGroup
	harnessGatesMu    sync.Mutex
	harnessGates      map[domain.AgentHarness]*sync.RWMutex

	executables                ports.ExecutableFinder
	commands                   ports.CommandRunner
	installCommands            ports.InstallCommandRunner
	installScripts             ports.InstallScriptRunner
	managedBinaries            ports.ManagedBinaryInstaller
	installCapabilities        ports.InstallCapabilityProbe
	pathWritable               ports.PathWritableProbe
	privateNPMPrefixes         map[Target]string
	privateInstallerHomes      map[Target]string
	jobStore                   ports.AgentInstallJobStore
	verifier                   HarnessVerifier
	sessions                   SessionLister
	reviewers                  ReviewerLister
	goos                       string
	goarch                     string
	installTimeout             time.Duration
	cleanupStepLimit           time.Duration
	persistenceTimeout         time.Duration
	onSucceeded                func(Target)
	managedVersion             managedVersionChecker
	ownsInstallation           func(context.Context, string, string, string, bool) (bool, error)
	updateAdvisories           map[Target]UpdateAdvisory
	updateChecksDisabled       bool
	updateWhileRunningDisabled bool
	managedRoot                string
	releases                   ManagedReleaseStore
	invalidateBinary           func(Target)
	updateWhenIdle             bool
	changing                   map[Target]bool
	idleUpdates                map[Target]*idleUpdateRun
	idleCoordinator            IdleUpdateCoordinator
	telemetry                  ports.EventSink
	updateLevels               map[Target]UpdateWhileRunning
	updateObserver             HarnessUpdateObserver
	deferredCleanups           map[Target]string
	derivedOwners              map[Target]derivedOwner
	updateAdvisoryCalls        map[Target]*updateAdvisoryCall
	updateCheckSlots           chan struct{}
	officialVersion            func(context.Context, Target) (string, error)
	cloudflaredDownloadFailed  atomic.Bool
	downloadWaits              []time.Duration
	cloudflaredAutoStarted     bool
	onCloudflaredInstalled     func()
	systemResolvers            map[Target]func(context.Context) (string, error)
}

type requestPlanner struct {
	*Service
	capabilities *ports.InstallCapabilities
	ctx          context.Context
}

func (s *Service) newRequestPlanner(ctx context.Context) (requestPlanner, error) {
	planner := requestPlanner{Service: s, ctx: ctx}
	if s.installCapabilities == nil {
		return planner, nil
	}
	capabilities, err := s.installCapabilities.Probe(ctx)
	if err != nil {
		return requestPlanner{}, err
	}
	planner.capabilities = &capabilities
	return planner, nil
}

func (s *Service) SetOnSucceeded(callback func(Target)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSucceeded = callback
}

func New(executables ports.ExecutableFinder, commands ports.CommandRunner) *Service {
	return NewWithDeps(executables, commands, Deps{})
}

func NewWithDeps(executables ports.ExecutableFinder, commands ports.CommandRunner, deps Deps) *Service {
	installCommands, _ := commands.(ports.InstallCommandRunner)
	installScripts, _ := commands.(ports.InstallScriptRunner)
	managedBinaries, _ := commands.(ports.ManagedBinaryInstaller)
	installCapabilities, _ := executables.(ports.InstallCapabilityProbe)
	pathWritable, _ := executables.(ports.PathWritableProbe)
	backgroundContext, stop := context.WithCancel(context.Background())
	return &Service{
		jobs:                       make(map[Target]*Job),
		harnessGates:               make(map[domain.AgentHarness]*sync.RWMutex),
		executables:                executables,
		commands:                   commands,
		installCommands:            installCommands,
		installScripts:             installScripts,
		managedBinaries:            managedBinaries,
		installCapabilities:        installCapabilities,
		jobStore:                   deps.JobStore,
		verifier:                   deps.Verifier,
		sessions:                   deps.Sessions,
		reviewers:                  deps.Reviewers,
		updateChecksDisabled:       deps.DisableUpdateChecks,
		updateWhileRunningDisabled: deps.DisableUpdateWhileRunning,
		updateWhenIdle:             deps.UpdateWhenIdle,
		managedRoot:                deps.ManagedRoot,
		releases:                   deps.Releases,
		goos:                       runtime.GOOS,
		goarch:                     runtime.GOARCH,
		installTimeout:             defaultInstallTimeout,
		downloadWaits:              cloudflaredDownloadWaits,
		persistenceTimeout:         defaultPersistenceTimeout,
		stop:                       stop,
		backgroundContext:          backgroundContext,
		managedVersion:             newManagedVersionChecker(commands, nil),
		officialVersion:            officialReleaseVersion(runtime.GOOS, runtime.GOARCH),
		ownsInstallation:           managerOwnsBinary(commands),
		updateAdvisories:           make(map[Target]UpdateAdvisory),
		updateAdvisoryCalls:        make(map[Target]*updateAdvisoryCall),
		pathWritable:               pathWritable,
		privateNPMPrefixes:         deps.PrivateNPMPrefixes,
		privateInstallerHomes:      deps.PrivateInstallerHomes,
	}
}

func (s *Service) AgentPlans(ctx context.Context) ([]AgentPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planner, err := s.newRequestPlanner(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentPlan, 0, len(agentTargets))
	for _, target := range agentTargets {
		plans := planner.agentMethodPlans(target, AgentOperationInstall)
		reinstallPlans := planner.agentMethodPlans(target, AgentOperationReinstall)
		reinstallByMethod := plansByMethod(reinstallPlans)
		recommended := recommendedPlanIndex(plans)
		plan := plans[recommended]
		updateByMethod := plansByMethod(planner.agentMethodPlans(target, AgentOperationUpdate))
		methods := make([]AgentInstallMethod, 0, len(plans))
		for index, methodPlan := range plans {
			reinstallPlan := reinstallByMethod[methodPlan.Method]
			updatePlan := operationPlanFor(updateByMethod, methodPlan.Method)
			methods = append(methods, AgentInstallMethod{
				ID: methodPlan.Method, Label: installMethodLabel(methodPlan.Method),
				Available: !methodPlan.Unsupported, Recommended: index == recommended,
				Command: displayCommand(methodPlan), Reason: methodPlan.Reason, Notice: methodPlan.Notice,
				ExpectedDestination: methodPlan.ExpectedDestination,
				ReinstallAvailable:  !reinstallPlan.Unsupported,
				ReinstallCommand:    displayCommand(reinstallPlan), ReinstallReason: reinstallPlan.Reason,
				UpdateAvailable: !updatePlan.Unsupported,
				UpdateCommand:   displayCommand(updatePlan), UpdateReason: updatePlan.Reason,
			})
		}
		out = append(out, AgentPlan{
			AgentID: string(target), Available: !plan.Unsupported,
			Automatic: !plan.Unsupported, Method: plan.Method,
			Command: displayCommand(plan), Reason: plan.Reason, Notice: plan.Notice,
			DocumentationURL:    plan.DocsURL,
			ExpectedDestination: plan.ExpectedDestination,
			Methods:             methods,
		})
	}
	return out, nil
}

func plansByMethod(plans []Plan) map[string]Plan {
	out := make(map[string]Plan, len(plans))
	for _, plan := range plans {
		out[plan.ownershipMethod()] = plan
	}
	return out
}

func operationPlanFor(plans map[string]Plan, method string) Plan {
	if plan, ok := plans[method]; ok {
		return plan
	}
	return Plan{Method: method, Unsupported: true, Reason: "This installation method is no longer detected."}
}

var lastResortInstallers = map[Target]bool{TargetHermes: true}

func recommendedPlanIndex(plans []Plan) int {
	for index, plan := range plans {
		if plan.Method == "official-installer" && !plan.Unsupported && !lastResortInstallers[plan.Target] {
			return index
		}
	}
	for index, plan := range plans {
		if !plan.Unsupported {
			return index
		}
	}
	return len(plans) - 1
}

func installMethodLabel(method string) string {
	switch method {
	case "homebrew":
		return "Homebrew"
	case "npm":
		return "npm"
	case "winget":
		return "winget"
	case "uv":
		return "uv tool"
	case "pipx":
		return "pipx"
	case "bun":
		return "Bun"
	case "official-installer":
		return "Official installer"
	default:
		return "Manual installation"
	}
}

func (s *Service) Start(ctx context.Context, target Target) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if !Valid(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown target %q", target)
	}
	if IsAgentTarget(target) {
		return s.StartAgent(ctx, target, "")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if job, ok := s.jobs[target]; ok && job.Status == StatusRunning {
		return *job, nil
	}
	return s.startPlanLocked(target, s.resolvePlan(target)), nil
}

func (s *Service) startPlanLocked(target Target, plan Plan) Job {
	command := displayCommand(plan)
	now := time.Now()
	if plan.Unsupported {
		job := &Job{
			Target:     target,
			Status:     StatusUnsupported,
			Method:     plan.Method,
			Command:    command,
			Error:      plan.Reason,
			Notice:     plan.Notice,
			StartedAt:  &now,
			FinishedAt: &now,
		}
		s.jobs[target] = job
		return *job
	}

	job := &Job{
		Target:    target,
		Status:    StatusRunning,
		Method:    plan.Method,
		Command:   command,
		Notice:    plan.Notice,
		StartedAt: &now,
	}
	s.jobs[target] = job
	if s.stopping {
		job.Status = StatusInterrupted
		job.Error = "daemon shutdown interrupted the install"
		job.FinishedAt = &now
		return *job
	}
	s.workers.Add(1)

	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		if plan.download != nil {
			s.runCloudflaredDownload(s.backgroundContext, *plan.download, job)
			return
		}
		s.run(s.backgroundContext, plan, job)
	}()

	return *job
}

func (s *Service) StartAgent(ctx context.Context, target Target, method string) (Job, error) {
	return s.StartAgentOperation(ctx, target, method, AgentOperationInstall, "")
}

func (s *Service) StartAgentOperation(ctx context.Context, target Target, method string, operation AgentOperation, expected string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if !operation.valid() {
		return Job{}, fmt.Errorf("%w: unknown install operation %q", ErrInstallMethod, operation)
	}
	if expected != "" {
		if _, ok := parseUpdateVersion(expected); !ok || operation != AgentOperationUpdate {
			return Job{}, ErrUpdateVersion
		}
	}
	if !IsAgentTarget(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown harness target %q", target)
	}
	var releaseHarness func()
	requiresHarnessGate := target == TargetDroid || target == TargetFX || operation == AgentOperationReinstall || operation == AgentOperationUpdate
	if requiresHarnessGate {
		s.mu.Lock()
		if current, ok := s.jobs[target]; ok && activeStatus(current.Status) {
			s.mu.Unlock()
			return Job{}, ErrInstallActive
		}
		s.mu.Unlock()
		gate := s.harnessGate(domain.AgentHarness(target))
		if !gate.TryLock() {
			return Job{}, &HarnessActiveError{Target: target, Starting: true, Level: UpdateWhileRunningBlocked}
		}
		s.setHarnessChanging(target, true)
		releaseHarness = func() {
			s.setHarnessChanging(target, false)
			gate.Unlock()
		}
		defer func() {
			if releaseHarness != nil {
				releaseHarness()
			}
		}()
		if operation != AgentOperationUpdate {
			if _, err := s.gateRunningSessions(ctx, target, operation, Plan{}); err != nil {
				return Job{}, err
			}
		}
	}

	var plan Plan
	var err error
	planner, plannerErr := s.newRequestPlanner(ctx)
	if plannerErr != nil {
		return Job{}, plannerErr
	}
	if method == "" {
		plans := planner.agentMethodPlans(target, operation)
		plan = plans[recommendedPlanIndex(plans)]
		if operation == AgentOperationUpdate {
			if owner, ok := s.owningUpdatePlan(ctx, target, plans); ok {
				plan = owner
			}
		}
	} else {
		plan, err = planner.resolveAgentMethod(target, method, operation)
		if err != nil {
			return Job{}, err
		}
	}

	var baseline *installedBaseline
	if operation == AgentOperationUpdate {
		if baseline, err = s.confirmInstalledOwner(ctx, target, plan); err != nil {
			return Job{}, err
		}
		if !plan.managed {
			plannedPrefix := plan.PackagePrefix
			if baseline != nil && baseline.path != "" {
				plan = targetInstalledCopy(s.goos, plan, target, baseline.path)
			}
			if target == TargetKimi {
				binaryPath := ""
				if baseline != nil {
					binaryPath = baseline.path
				}
				home, _ := os.UserHomeDir()
				plan = bindKimiInstallerUpdate(s.goos, plan, binaryPath, home, os.Getenv("PATH"))
			}
			plan = s.validateNPMUpdateTarget(ctx, plan, plannedPrefix)
		}
	}
	if operation == AgentOperationUpdate && expected != "" {
		if baseline == nil {
			baseline = &installedBaseline{target: target, scheme: versionSchemeFor(target)}
		}
		if versionAtMost(baseline.scheme, expected, baseline.version) {
			return Job{}, ErrUpdateVersion
		}
		baseline.expected = expected
	}
	if operation == AgentOperationUpdate && baseline != nil {
		if baseline.latest != "" && versionAtMost(baseline.scheme, baseline.latest, baseline.version) {
			baseline.latest = ""
		}
		pin := baseline.latest
		if baseline.expected != "" && baseline.version != "" && versionAtMost(baseline.scheme, baseline.expected, baseline.latest) {
			pin = baseline.expected
		}
		if pin != "" {
			baseline.latest = pin
		}
		plan = pinVendorUpdate(plan, pin)
	}
	if operation == AgentOperationUpdate {
		withSessions, err := s.gateRunningSessions(ctx, target, operation, plan)
		var active *HarnessActiveError
		switch {
		case errors.Is(err, errUpdateWhenIdle) && !plan.Unsupported:
			s.emitHarnessUpdate(target, UpdateWhileRunningIdle, "waiting", 0)
			return s.startIdleUpdate(ctx, plan, baseline)
		case errors.As(err, &active):
			result := "refused_sessions"
			if active.Starting {
				result = "refused_starting"
			}
			s.emitHarnessUpdate(target, active.Level, result, active.Count())
		}
		if err != nil {
			return Job{}, err
		}
		level := s.effectiveUpdateLevel(plan.UpdateWhileRunning)
		s.mu.Lock()
		if s.updateLevels == nil {
			s.updateLevels = make(map[Target]UpdateWhileRunning)
		}
		s.updateLevels[target] = level
		s.mu.Unlock()
		plan.keepPreviousRelease = withSessions
	}

	now := time.Now().UTC()
	if plan.Unsupported && operation != AgentOperationInstall {
		return Job{
			Target: target, Status: StatusUnsupported, Method: plan.Method,
			Command: displayCommand(plan), ExpectedDestination: plan.ExpectedDestination,
			Error: plan.Reason, Notice: plan.Notice, StartedAt: &now, FinishedAt: &now, UpdatedAt: &now,
		}, nil
	}
	s.mu.Lock()
	if current, ok := s.jobs[target]; ok && activeStatus(current.Status) {
		s.mu.Unlock()
		return Job{}, ErrInstallActive
	}
	if !requiresHarnessGate && s.changing[target] {
		s.mu.Unlock()
		return Job{}, ErrInstallActive
	}
	previous, hadPrevious := s.jobs[target]
	status := StatusInstalling
	finishedAt := (*time.Time)(nil)
	if plan.Unsupported {
		status = StatusUnsupported
		finishedAt = &now
	}
	job := &Job{
		Target: target, Status: status, Method: plan.Method,
		Command: displayCommand(plan), ExpectedDestination: plan.ExpectedDestination,
		Error: plan.Reason, Notice: plan.Notice, StartedAt: &now, FinishedAt: finishedAt, UpdatedAt: &now,
	}
	s.jobs[target] = job
	s.mu.Unlock()

	if err := s.persistJob(ctx, *job); err != nil {
		s.mu.Lock()
		if s.jobs[target] == job {
			if hadPrevious {
				s.jobs[target] = previous
			} else {
				delete(s.jobs, target)
			}
		}
		s.mu.Unlock()
		return Job{}, err
	}
	if status == StatusUnsupported {
		return *job, nil
	}

	initial := *job
	if !s.beginWorker() {
		s.finishAgentJob(job, StatusInterrupted, "", "daemon shutdown interrupted the install", "")
		return initial, nil
	}
	workerRelease := releaseHarness
	releaseHarness = nil
	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		if workerRelease != nil {
			defer workerRelease()
		}
		s.runAgentOperation(s.backgroundContext, plan, operation, job, baseline)
	}()
	return initial, nil
}

func (s *Service) owningUpdatePlan(ctx context.Context, target Target, plans []Plan) (Plan, bool) {
	s.mu.Lock()
	advisory, hasAdvisory := s.updateAdvisories[target]
	s.mu.Unlock()
	if hasAdvisory && advisory.MaintenanceMethod != "" {
		for _, plan := range plans {
			if plan.ownershipMethod() == advisory.MaintenanceMethod && !plan.Unsupported {
				return plan, true
			}
		}
	}
	if s.verifier == nil {
		return Plan{}, false
	}
	verified, _ := s.verifier.Verify(ctx, target)
	if verified.ResolvedPath == "" {
		return Plan{}, false
	}
	for _, plan := range plans {
		if !plan.Unsupported && s.methodOwnsBinary(ctx, plan, verified.ResolvedPath) {
			return plan, true
		}
	}
	return Plan{}, false
}

func (s *Service) harnessGate(harness domain.AgentHarness) *sync.RWMutex {
	s.harnessGatesMu.Lock()
	defer s.harnessGatesMu.Unlock()
	if s.harnessGates == nil {
		s.harnessGates = make(map[domain.AgentHarness]*sync.RWMutex)
	}
	gate := s.harnessGates[harness]
	if gate == nil {
		gate = &sync.RWMutex{}
		s.harnessGates[harness] = gate
	}
	return gate
}

func (s *Service) TryBeginHarnessUse(harness domain.AgentHarness) (func(), bool) {
	gate := s.harnessGate(harness)
	if !gate.TryRLock() && !s.waitForHarnessChange(Target(harness), gate) {
		return nil, false
	}
	if IsAgentTarget(Target(harness)) {
		s.forgetReplacedBinary(Target(harness))
	}
	return gate.RUnlock, true
}

func (s *Service) Status(ctx context.Context, target Target) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	if !Valid(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown target %q", target)
	}
	s.mu.Lock()
	inMemory, ok := s.jobs[target]
	if ok {
		snapshot := *inMemory
		s.mu.Unlock()
		return snapshot, nil
	}
	s.mu.Unlock()
	if IsAgentTarget(target) && s.jobStore != nil {
		record, ok, err := s.jobStore.GetAgentInstallJob(ctx, string(target))
		if err != nil {
			return Job{}, err
		}
		if ok {
			return jobFromRecord(record), nil
		}
	}

	var plan Plan
	if IsAgentTarget(target) {
		planner, err := s.newRequestPlanner(ctx)
		if err != nil {
			return Job{}, err
		}
		plans := planner.agentMethodPlans(target, AgentOperationInstall)
		plan = plans[recommendedPlanIndex(plans)]
	} else {
		plan = s.resolvePlan(target)
	}
	status := StatusIdle
	if plan.Unsupported {
		status = StatusUnsupported
	}
	return Job{
		Target:  target,
		Status:  status,
		Command: displayCommand(plan),
		Error:   plan.Reason,
		Notice:  plan.Notice,
	}, nil
}

func (s *Service) AgentJobs(ctx context.Context) ([]Job, error) {
	if s.jobStore != nil {
		records, err := s.jobStore.ListAgentInstallJobs(ctx)
		if err != nil {
			return nil, err
		}
		jobs := make([]Job, 0, len(records))
		indexes := make(map[Target]int, len(records))
		for _, record := range records {
			job := jobFromRecord(record)
			indexes[job.Target] = len(jobs)
			jobs = append(jobs, job)
		}
		s.mu.Lock()
		for target, job := range s.jobs {
			if IsAgentTarget(target) {
				if index, ok := indexes[target]; ok {
					jobs[index] = *job
				} else {
					indexes[target] = len(jobs)
					jobs = append(jobs, *job)
				}
			}
		}
		s.mu.Unlock()
		return jobs, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]Job, 0, len(s.jobs))
	for target, job := range s.jobs {
		if IsAgentTarget(target) {
			jobs = append(jobs, *job)
		}
	}
	return jobs, nil
}

func (s *Service) Recover(ctx context.Context) error {
	if err := s.ReconcileManagedReleases(ctx); err != nil {
		return err
	}
	if s.jobStore == nil {
		return nil
	}
	if err := s.jobStore.InterruptActiveAgentInstallJobs(ctx, time.Now().UTC()); err != nil {
		return err
	}
	records, err := s.jobStore.ListAgentInstallJobs(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range records {
		job := jobFromRecord(record)
		s.jobs[job.Target] = &job
	}
	return nil
}

func (s *Service) Verify(ctx context.Context, target Target) (Job, error) {
	if !IsAgentTarget(target) {
		return Job{}, fmt.Errorf("systeminstall: unknown harness target %q", target)
	}
	s.mu.Lock()
	if current, ok := s.jobs[target]; ok && activeStatus(current.Status) {
		s.mu.Unlock()
		return Job{}, ErrInstallActive
	}
	previous, hadPrevious := s.jobs[target]
	now := time.Now().UTC()
	job := &Job{Target: target, Status: StatusVerifying, StartedAt: &now, UpdatedAt: &now}
	if current, ok := s.jobs[target]; ok {
		job.Method = current.Method
		job.Command = current.Command
		job.ExpectedDestination = current.ExpectedDestination
		job.Output = current.Output
	}
	s.jobs[target] = job
	s.mu.Unlock()
	if err := s.persistJob(ctx, *job); err != nil {
		s.mu.Lock()
		if s.jobs[target] == job {
			if hadPrevious {
				s.jobs[target] = previous
			} else {
				delete(s.jobs, target)
			}
		}
		s.mu.Unlock()
		return Job{}, err
	}
	initial := *job
	if !s.beginWorker() {
		s.finishAgentJob(job, StatusInterrupted, "", "daemon shutdown interrupted verification", "")
		return initial, nil
	}
	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		s.runAgentVerification(s.backgroundContext, job, nil, "")
	}()
	return initial, nil
}

func (s *Service) beginWorker() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.workers.Add(1)
	return true
}

func (s *Service) ShutdownTimeout() time.Duration {
	return shutdownTimeoutForCleanup(s.cleanupStepTimeout())
}

func DefaultShutdownTimeout() time.Duration {
	return shutdownTimeoutForCleanup(cleanupTimeout)
}

func shutdownTimeoutForCleanup(stepTimeout time.Duration) time.Duration {
	return 2*stepTimeout + otherHarnessHoldWait + 2*defaultPersistenceTimeout + 30*time.Second
}

func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		s.stop()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("systeminstall: drain workers: %w", ctx.Err())
	}
}

func (s *Service) run(parent context.Context, plan Plan, job *Job) {
	ctx, cancel := context.WithTimeout(parent, s.installTimeout)
	defer cancel()

	out := &capturedOutput{max: maxOutputBytes}
	env := []string{
		"CI=1",
		"NONINTERACTIVE=1",
		"HOMEBREW_NO_AUTO_UPDATE=1",
		"NPM_CONFIG_AUDIT=false",
		"NPM_CONFIG_FUND=false",
	}
	var runErr error
	switch {
	case plan.Script != nil:
		if s.installScripts == nil {
			runErr = errors.New("remote installer runner is not configured")
			break
		}
		command := *plan.Script
		command.Env = append(append([]string(nil), env...), command.Env...)
		var result ports.InstallScriptResult
		result, runErr = s.installScripts.RunInstallScript(ctx, command, out, out)
		if result.SHA256 != "" {
			_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
		}
	case s.installCommands != nil:
		runErr = s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: plan.Command, Env: env}, out, out)
	default:
		runErr = s.commands.Run(ctx, plan.Command, out, out)
	}
	needsBinary := runErr == nil && ctx.Err() == nil && !IsAgentTarget(job.Target)
	found := !needsBinary || s.systemTargetInstalled(ctx, job.Target)
	now := time.Now()

	s.mu.Lock()
	job.Output = out.String()
	job.FinishedAt = &now
	if ctx.Err() == context.DeadlineExceeded {
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("install timed out after %s", s.installTimeout)
		s.mu.Unlock()
		return
	}
	if ctx.Err() == context.Canceled {
		job.Status = StatusInterrupted
		job.Error = "daemon shutdown interrupted the install"
		s.mu.Unlock()
		return
	}
	if runErr != nil {
		job.Status = StatusFailed
		job.Error = runErr.Error()
		s.mu.Unlock()
		return
	}
	if !found {
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("install command finished but %s is still not in PATH", job.Target)
		s.mu.Unlock()
		return
	}
	if connectorInstalled := s.onCloudflaredInstalled; job.Target == TargetCloudflared && connectorInstalled != nil {
		s.mu.Unlock()
		connectorInstalled()
		s.mu.Lock()
	}
	job.Status = StatusSucceeded
	callback := s.onSucceeded
	target := job.Target
	s.mu.Unlock()
	if callback != nil {
		callback(target)
	}
}

func (s *Service) SetSystemTargetResolver(target Target, resolve func(context.Context) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.systemResolvers == nil {
		s.systemResolvers = make(map[Target]func(context.Context) (string, error))
	}
	s.systemResolvers[target] = resolve
}

func (s *Service) systemTargetInstalled(ctx context.Context, target Target) bool {
	if path, err := s.executables.LookPath(string(target)); err == nil && path != "" {
		return true
	}
	s.mu.Lock()
	resolve := s.systemResolvers[target]
	s.mu.Unlock()
	if resolve == nil {
		return false
	}
	path, err := resolve(ctx)
	return err == nil && path != ""
}

func (s *Service) runAgentOperation(parent context.Context, plan Plan, operation AgentOperation, job *Job, baseline *installedBaseline) {
	release, err := s.holdOtherHarnesses(plan.alsoChanges)
	if err != nil {
		s.finishAgentJob(job, StatusFailed, "", err.Error(), "")
		return
	}
	defer release()
	plan = plan.withLinkedOpenCodeV1()
	s.mu.Lock()
	job.Command = displayCommand(plan)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, s.installTimeout)
	defer cancel()
	out := &capturedOutput{max: maxOutputBytes}
	outputWriter := io.Writer(out)
	var devinOutput *devinInstallOutput
	if plan.Target == TargetDevin && plan.Method == "official-installer" && plan.Script != nil {
		devinOutput = &devinInstallOutput{dst: out, maxPending: maxOutputBytes}
		outputWriter = devinOutput
	}
	env := []string{
		"CI=1", "NONINTERACTIVE=1", "HOMEBREW_NO_AUTO_UPDATE=1",
		"NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false",
	}
	if plan.keepPreviousRelease && plan.Method == "homebrew" && !plan.PackageCask {
		env = append(env, "HOMEBREW_NO_INSTALL_CLEANUP=1")
		s.deferHomebrewCleanup(plan.Target, packageWithoutLatest(plan.Package))
	}
	progress := newProgressTracker(outputWriter)
	stopProgress := s.publishAgentProgress(job, out, progress)
	var runErr error
	if plan.managed {
		runErr = s.runManagedInstall(ctx, plan, baseline, env, progress)
	} else if plan.Script != nil {
		if s.installScripts == nil {
			runErr = errors.New("remote installer runner is not configured")
		} else {
			command := *plan.Script
			command.Env = append(append([]string(nil), env...), command.Env...)
			var result ports.InstallScriptResult
			result, runErr = s.installScripts.RunInstallScript(ctx, command, progress, progress)
			if result.SHA256 != "" {
				_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
			}
		}
	} else {
		if plan.Method == "official-installer" {
			env = append(env, vendorUpdateEnv[plan.Target]...)
		}
		runCommand := func(ctx context.Context, argv []string) error {
			if s.installCommands != nil {
				return s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: env}, progress, progress)
			}
			return s.commands.Run(ctx, argv, progress, progress)
		}
		for _, argv := range plan.Prepare {
			if err := runCommand(ctx, argv); err != nil && ctx.Err() == nil {
				_, _ = fmt.Fprintf(out, "\n%s failed (%v); continuing.\n", strings.Join(argv, " "), err)
			}
		}
		if ctx.Err() == nil {
			runErr = runCommand(ctx, plan.Command)
		}
		for _, argv := range plan.Cleanup {
			cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), s.cleanupStepTimeout())
			err := runCommand(cleanupCtx, argv)
			cancelCleanup()
			if err != nil {
				_, _ = fmt.Fprintf(out, "\n%s failed (%v).\n", strings.Join(argv, " "), err)
				if runErr == nil && ctx.Err() == nil {
					runErr = fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
				}
			}
		}
	}
	stopProgress()
	if ctx.Err() == context.DeadlineExceeded {
		s.finishAgentJob(job, StatusFailed, out.String(), fmt.Sprintf("%s timed out after %s", operation, s.installTimeout), "")
		return
	}
	if ctx.Err() == context.Canceled {
		s.finishAgentJob(job, StatusInterrupted, out.String(), fmt.Sprintf("daemon shutdown interrupted %s", operation), "")
		return
	}
	installConfirmed := runErr != nil && devinOutput != nil && devinOutput.Confirmed()
	autohandCLIInstalled := runErr != nil && s.goos == "windows" && plan.Target == TargetAutohand &&
		plan.Method == "official-installer" && plan.Script != nil &&
		strings.Contains(out.String(), "Installed to ") &&
		strings.Contains(out.String(), "Computer control could not be installed.")
	if autohandCLIInstalled {
		_, _ = io.WriteString(out, "\nAutohand CLI install will be verified. Computer control needs a separate repair: autohand computer install\n")
	}
	if runErr != nil && !installConfirmed && !autohandCLIInstalled {
		message := failureSummary(runErr, out)
		if plan.Target == TargetAutohand && plan.Method == "official-installer" &&
			strings.Contains(out.String(), "Downloaded Autohand CLI failed to start") &&
			strings.Contains(out.String(), "The existing installation was not changed.") {
			message = fmt.Sprintf("Autohand could not start the downloaded CLI; the existing installation was not changed. Its startup check can time out on a busy machine. Let other updates finish, then retry. (%v)", runErr)
		}
		s.finishAgentJob(job, StatusFailed, out.String(), message, "")
		return
	}

	if err := s.transitionAgentJob(job, StatusVerifying, out.String(), "", ""); err != nil {
		sentryobs.CaptureBackground(parent, err, "systeminstall", "agent_install", map[string]string{"agent": string(job.Target), "reason": "persist"})
		s.finishAgentJob(job, StatusFailed, "", fmt.Sprintf("persist verifying state: %v", err), "")
		return
	}
	updaterReport := ""
	if plan.Script == nil {
		updaterReport = lastOutputLine(out.String())
	}
	s.runAgentVerification(s.backgroundContext, job, baseline, updaterReport)
}

const maxUpdaterReportRunes = 80

type installFailure struct {
	summary string
	err     error
}

func (e *installFailure) Error() string { return e.summary }
func (e *installFailure) Unwrap() error { return e.err }

func installFailed(summary string, err error) error {
	return &installFailure{summary: summary, err: err}
}

func failureSummary(err error, out io.Writer) string {
	var failure *installFailure
	if errors.As(err, &failure) && failure.err != nil {
		_, _ = fmt.Fprintf(out, "\n%s\n%v\n", failure.summary, failure.err)
	}
	return err.Error()
}

var terminalEscape = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func lastOutputLine(output string) string {
	output = terminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r\n", "\n"), "")
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := ""
		segments := strings.Split(lines[index], "\r")
		for segment := len(segments) - 1; segment >= 0 && line == ""; segment-- {
			line = strings.TrimSpace(segments[segment])
		}
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > maxUpdaterReportRunes {
			line = string(runes[:maxUpdaterReportRunes]) + "…"
		}
		return line
	}
	return ""
}

var errVerifierMissing = errors.New("systeminstall: adapter-backed install verifier is not configured")

func (s *Service) runAgentVerification(ctx context.Context, job *Job, baseline *installedBaseline, updaterReport string) {
	if s.verifier == nil {
		sentryobs.CaptureBackground(ctx, errVerifierMissing, "systeminstall", "agent_install", map[string]string{"agent": string(job.Target), "reason": "verify"})
		s.finishAgentJob(job, StatusFailed, "", "adapter-backed install verifier is not configured", "")
		return
	}
	result, err := s.verifier.Verify(ctx, job.Target)
	if ctx.Err() == context.Canceled {
		s.finishAgentJob(job, StatusInterrupted, result.Output, "daemon shutdown interrupted verification", result.ResolvedPath)
		return
	}
	if err != nil {
		s.finishAgentJob(job, StatusFailed, result.Output, err.Error(), result.ResolvedPath)
		return
	}
	output := result.Output
	failure, note := updateOutcome(baseline, result)
	if note != "" {
		output = strings.TrimRight(output, "\n") + "\n" + note
	}
	if failure != "" {
		if updaterReport != "" {
			failure = fmt.Sprintf("%s The updater said: %q", failure, updaterReport)
		}
		s.finishAgentJob(job, StatusFailed, output, failure, result.ResolvedPath)
		return
	}
	if baseline != nil {
		if installed, ok := baseline.scheme.find(installedVersionText(job.Target, result.ResolvedPath, result.Output)); ok {
			s.noteHarnessUpdated(job.Target, installed.display)
		}
	}
	s.finishAgentJob(job, StatusSucceeded, output, "", result.ResolvedPath)
}

func (s *Service) transitionAgentJob(job *Job, status Status, output, errorMessage, resolvedPath string) error {
	now := time.Now().UTC()
	s.mu.Lock()
	job.Status = status
	job.Output = combineOutput(job.Output, output)
	job.Error = errorMessage
	if resolvedPath != "" {
		job.ExpectedDestination = resolvedPath
	}
	job.UpdatedAt = &now
	snapshot := *job
	s.mu.Unlock()
	return s.persistJobBestEffort(snapshot)
}

func (s *Service) finishAgentJob(job *Job, status Status, output, errorMessage, resolvedPath string) {
	now := time.Now().UTC()
	s.mu.Lock()
	job.Status = status
	job.Output = combineOutput(job.Output, output)
	job.Error = errorMessage
	if resolvedPath != "" {
		job.ExpectedDestination = resolvedPath
	}
	job.FinishedAt = &now
	job.UpdatedAt = &now
	level, wasUpdate := s.updateLevels[job.Target]
	delete(s.updateLevels, job.Target)
	delete(s.updateAdvisories, job.Target)
	delete(s.updateAdvisoryCalls, job.Target)
	delete(s.derivedOwners, job.Target)
	snapshot := *job
	callback := s.onSucceeded
	target := job.Target
	s.mu.Unlock()
	if err := s.persistJobBestEffort(snapshot); err != nil {
		s.mu.Lock()
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("persist terminal operation state: %v", err)
		s.mu.Unlock()
		return
	}
	if wasUpdate {
		s.emitHarnessUpdate(target, level, string(status), 0)
	}
	if status == StatusSucceeded && callback != nil {
		callback(target)
	}
}

func (s *Service) persistJobBestEffort(job Job) error {
	timeout := s.persistenceTimeout
	if timeout <= 0 {
		timeout = defaultPersistenceTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.persistJob(ctx, job)
}

func (s *Service) persistJob(ctx context.Context, job Job) error {
	if s.jobStore == nil {
		return nil
	}
	if job.StartedAt == nil {
		return fmt.Errorf("systeminstall: persist job without startedAt")
	}
	updatedAt := *job.StartedAt
	if job.UpdatedAt != nil {
		updatedAt = *job.UpdatedAt
	}
	return s.jobStore.UpsertAgentInstallJob(ctx, ports.AgentInstallJobRecord{
		Target: string(job.Target), Status: string(job.Status), Method: job.Method,
		Command: job.Command, ExpectedDestination: job.ExpectedDestination,
		Output: job.Output, Error: job.Error, StartedAt: *job.StartedAt,
		FinishedAt: job.FinishedAt, UpdatedAt: updatedAt,
	})
}

func jobFromRecord(record ports.AgentInstallJobRecord) Job {
	startedAt := record.StartedAt
	updatedAt := record.UpdatedAt
	return Job{
		Target: Target(record.Target), Status: Status(record.Status), Method: record.Method,
		Command: record.Command, ExpectedDestination: record.ExpectedDestination,
		Output: record.Output, Error: record.Error, StartedAt: &startedAt,
		FinishedAt: record.FinishedAt, UpdatedAt: &updatedAt,
	}
}

func activeStatus(status Status) bool {
	return status == StatusRunning || status == StatusWaitingForIdle || status == StatusInstalling || status == StatusVerifying
}

func combineOutput(existing, next string) string {
	out := &capturedOutput{max: maxOutputBytes}
	_, _ = out.Write([]byte(existing))
	if existing != "" && next != "" && !strings.HasSuffix(existing, "\n") {
		_, _ = out.Write([]byte("\n"))
	}
	_, _ = out.Write([]byte(next))
	return out.String()
}

func (s *Service) resolvePlan(target Target) Plan {
	if IsAgentTarget(target) {
		return s.planAgent(target)
	}
	return s.planFor(target)
}

func displayCommand(plan Plan) string {
	if plan.download != nil {
		return "download " + plan.download.URL
	}
	if plan.Script != nil {
		display := fmt.Sprintf("%s <downloaded from %s>", strings.Join(plan.Script.Interpreter, " "), plan.Script.URL)
		if len(plan.Script.Args) > 0 {
			display += " " + strings.Join(plan.Script.Args, " ")
		}
		return display
	}
	argv := plan.Command
	if plan.NeedsRoot && len(argv) > 0 {
		argv = append([]string{"sudo"}, argv...)
	}
	steps := make([]string, 0, len(plan.Prepare)+1+len(plan.Cleanup))
	for _, prepare := range plan.Prepare {
		steps = append(steps, strings.Join(prepare, " "))
	}
	steps = append(steps, strings.Join(argv, " "))
	for _, cleanup := range plan.Cleanup {
		steps = append(steps, strings.Join(cleanup, " "))
	}
	return strings.Join(steps, "; ")
}

type capturedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (c *capturedOutput) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Write(p)
	if c.buf.Len() > c.max {
		tail := c.buf.String()[c.buf.Len()-c.max:]
		c.buf.Reset()
		c.buf.WriteString(tail)
	}
	return len(p), nil
}

func (c *capturedOutput) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

type devinInstallOutput struct {
	mu         sync.Mutex
	dst        io.Writer
	pending    []byte
	maxPending int
	confirmed  bool
}

func (w *devinInstallOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.dst.Write(p)
	if w.confirmed {
		return n, err
	}
	w.pending = append(w.pending, p...)
	if devinInstalledLine.Match(w.pending) {
		w.confirmed = true
		w.pending = nil
		return n, err
	}
	if delimiter := bytes.LastIndexAny(w.pending, "\r\n"); delimiter >= 0 {
		w.pending = append(w.pending[:0], w.pending[delimiter+1:]...)
	}
	if len(w.pending) > w.maxPending {
		w.pending = append(w.pending[:0], w.pending[len(w.pending)-w.maxPending:]...)
	}
	return n, err
}

func (w *devinInstallOutput) Confirmed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.confirmed
}

func (s *Service) planFor(target Target) Plan {
	switch target {
	case TargetTmux:
		return s.planTmux()
	case TargetGH:
		return s.planGH()
	case TargetClaude:
		return s.planNPM(TargetClaude, "@anthropic-ai/claude-code")
	case TargetCodex:
		return s.planNPM(TargetCodex, "@openai/codex")
	case TargetCopilot:
		return s.planNPM(TargetCopilot, "@github/copilot")
	case TargetOpencode:
		return s.planOpencode()
	case TargetOpencodeV2:
		return s.planAgent(TargetOpencodeV2)
	case TargetCloudflared:
		return s.planCloudflared()
	case TargetCuaDriver:
		return s.planCuaDriver()
	default:
		return Plan{Target: target, Unsupported: true, Reason: "unknown install target"}
	}
}

func (s *Service) planTmux() Plan {
	switch s.goos {
	case "windows":
		return Plan{
			Target: TargetTmux, Unsupported: true,
			Reason: "tmux is not required on Windows; Orchestrator uses the built-in ConPTY terminal runtime instead.",
		}
	case "darwin":
		return s.planBrew(TargetTmux, "tmux")
	case "linux":
		return s.planLinuxPackage(TargetTmux, func(string) string { return "tmux" })
	default:
		return Plan{Target: TargetTmux, Unsupported: true, Reason: "tmux installation is not supported on this platform."}
	}
}

func (s *Service) planGH() Plan {
	switch s.goos {
	case "windows":
		return s.planWinget(TargetGH, "GitHub.cli")
	case "darwin":
		return s.planBrew(TargetGH, "gh")
	case "linux":
		return s.planLinuxPackage(TargetGH, func(mgr string) string {
			if mgr == "pacman" {
				return "github-cli"
			}
			return "gh"
		})
	default:
		return Plan{Target: TargetGH, Unsupported: true, Reason: "gh installation is not supported on this platform."}
	}
}

func (s *Service) planCloudflared() Plan {
	if plan, ok := s.cloudflaredDownloadPlan(); ok && !s.cloudflaredDownloadFailed.Load() {
		return plan
	}
	switch s.goos {
	case "windows":
		return s.planWinget(TargetCloudflared, "Cloudflare.cloudflared")
	case "darwin":
		return s.planBrew(TargetCloudflared, "cloudflared")
	case "linux":
		return s.planLinuxPackage(TargetCloudflared, func(string) string { return "cloudflared" })
	default:
		return Plan{
			Target: TargetCloudflared, Unsupported: true,
			Reason: "cloudflared installation is not supported on this platform.",
		}
	}
}

const (
	cuaDriverUnixInstaller    = "https://cua.ai/driver/install.sh"
	cuaDriverWindowsInstaller = "https://cua.ai/driver/install.ps1"
	cuaDriverDocs             = "https://cua.ai/docs/how-to-guides/driver/install"
)

func (s *Service) planCuaDriver() Plan {
	plan := s.officialByOS(TargetCuaDriver, cuaDriverUnixInstaller, "bash", cuaDriverWindowsInstaller, cuaDriverDocs)
	if !plan.Unsupported || plan.Script != nil || len(plan.Command) > 0 {
		return plan
	}
	switch s.goos {
	case "windows":
		plan.Command = []string{"irm", cuaDriverWindowsInstaller, "|", "iex"}
		plan.Reason += " Copy the install command and run it in PowerShell, or follow " + cuaDriverDocs + "."
	case "darwin", "linux":
		plan.Command = []string{"curl", "-fsSL", cuaDriverUnixInstaller, "|", "bash"}
		plan.Reason += " Copy the install command and run it in a terminal, or follow " + cuaDriverDocs + "."
	}
	return plan
}

func (s *Service) planNPM(target Target, pkg string) Plan {
	if !IsAgentTarget(target) {
		return (requestPlanner{Service: s}).planNPM(target, pkg)
	}
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "npm", Reason: "npm and Node.js capabilities could not be inspected."}
	}
	return planner.planNPM(target, pkg)
}

func (p requestPlanner) planNPM(target Target, pkg string) Plan {
	s := p.Service
	if _, err := s.executables.LookPath("npm"); err != nil {
		return Plan{
			Target: target, Unsupported: true,
			Method: "npm", Reason: "npm was not found on PATH. Install Node.js from https://nodejs.org first, then retry.",
		}
	}
	plan := Plan{Target: target, Command: []string{"npm", "install", "-g", pkg}, Method: "npm", Package: pkg}
	privatePrefix := ""
	if target == TargetOpencodeV2 {
		privatePrefix = strings.TrimSpace(s.privateNPMPrefixes[target])
		if privatePrefix == "" {
			plan.Unsupported = true
			plan.Reason = "OpenCode 2's private npm prefix could not be resolved."
			return plan
		}
		plan.Command = []string{"npm", "install", "-g", "--prefix", privatePrefix, pkg}
	}
	if IsAgentTarget(target) {
		if p.capabilities == nil {
			plan.Unsupported = true
			plan.Reason = "npm and Node.js capabilities could not be inspected."
			return plan
		}
		npm := p.capabilities.NPM
		capabilityErr := npm.Err
		if privatePrefix != "" {
			capabilityErr = npm.RuntimeErr
		}
		if capabilityErr != nil {
			plan.Unsupported = true
			plan.Reason = "npm and Node.js capabilities could not be inspected."
			return plan
		}
		nodeVersion, nodeOK := parseToolVersion(npm.NodeVersion)
		_, npmOK := parseToolVersion(npm.NPMVersion)
		runtimeReason := ""
		if !nodeOK || !npmOK {
			runtimeReason = "npm and Node.js versions could not be validated."
		} else if minimumNode := minimumNodeVersionForTarget(target); !versionAtLeast(nodeVersion, minimumNode) {
			runtimeReason = fmt.Sprintf("Node.js %s+ is required for %s's npm recipe; found %s.", formatToolVersion(minimumNode), target, npm.NodeVersion)
		}
		if runtimeReason != "" && privatePrefix != "" {
			plan.Unsupported = true
			plan.Reason = runtimeReason
			return plan
		}
		prefix := npm.GlobalPrefix
		prefixWritable := npm.PrefixWritable
		if privatePrefix != "" {
			prefix = privatePrefix
			if s.pathWritable == nil {
				plan.Unsupported = true
				plan.Reason = "OpenCode 2's private npm prefix could not be validated."
				return plan
			}
			var err error
			prefixWritable, err = s.pathWritable.PathWritable(p.ctx, prefix)
			if err != nil {
				plan.Unsupported = true
				plan.Reason = "OpenCode 2's private npm prefix could not be validated."
				return plan
			}
		}
		plan.PackagePrefix = prefix
		if prefix == "" {
			plan.Unsupported = true
			plan.Reason = "npm's global install prefix could not be resolved."
			return plan
		}
		if runtimeReason != "" {
			plan.Unsupported = true
			plan.Reason = runtimeReason
			plan.daemonRuntimeRejected = true
			plan.daemonPrefixRejected = !prefixWritable
			return plan
		}
		if !prefixWritable {
			plan.Unsupported = true
			plan.daemonPrefixRejected = privatePrefix == ""
			if privatePrefix != "" {
				plan.Reason = fmt.Sprintf("OpenCode 2's private npm prefix %s is not writable by the current user.", prefix)
			} else {
				plan.Reason = fmt.Sprintf("npm's global prefix %s is not writable by the current user. Configure a user-owned prefix; Orchestrator will not use sudo.", prefix)
			}
			return plan
		}
		if s.goos == "windows" {
			plan.ExpectedDestination = prefix
		} else {
			plan.ExpectedDestination = filepath.Join(prefix, "bin")
		}
	}
	return plan
}

func minimumNodeVersionForTarget(target Target) [3]int {
	switch target {
	case TargetAuggie, TargetDroid, TargetGemini:
		return [3]int{20, 0, 0}
	case TargetClaudeCode, TargetQwen, TargetAutohand:
		return [3]int{22, 0, 0}
	case TargetPi, TargetMiniMaxCode:
		return [3]int{22, 19, 0}
	default:
		return [3]int{16, 0, 0}
	}
}

func formatToolVersion(version [3]int) string {
	if version[2] != 0 {
		return fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])
	}
	if version[1] != 0 {
		return fmt.Sprintf("%d.%d", version[0], version[1])
	}
	return strconv.Itoa(version[0])
}

func parseToolVersion(value string) ([3]int, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	fields := strings.Split(value, ".")
	if len(fields) == 0 || len(fields) > 3 {
		return [3]int{}, false
	}
	var parsed [3]int
	for index, field := range fields {
		part, err := strconv.Atoi(field)
		if err != nil || part < 0 {
			return [3]int{}, false
		}
		parsed[index] = part
	}
	return parsed, true
}

func versionAtLeast(got, minimum [3]int) bool {
	for index := range got {
		if got[index] != minimum[index] {
			return got[index] > minimum[index]
		}
	}
	return true
}

func (s *Service) planOpencode() Plan {
	if s.goos == "windows" {
		return s.planWinget(TargetOpencode, "SST.opencode")
	}
	return Plan{
		Target: TargetOpencode, Unsupported: true, Method: "manual",
		Reason: "Orchestrator does not automatically execute opencode's mutable remote installer script.",
	}
}

func (s *Service) planBrew(target Target, pkg string) Plan {
	if !IsAgentTarget(target) {
		return (requestPlanner{Service: s}).planBrew(target, pkg)
	}
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew packages could not be inspected."}
	}
	return planner.planBrew(target, pkg)
}

func (s *Service) planBrewCask(target Target, pkg string) Plan {
	if !IsAgentTarget(target) {
		return (requestPlanner{Service: s}).planBrewCask(target, pkg)
	}
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew packages could not be inspected."}
	}
	return planner.planBrewCask(target, pkg)
}

func (p requestPlanner) planBrew(target Target, pkg string) Plan {
	return p.planHomebrew(target, pkg, false)
}

func (p requestPlanner) planBrewCask(target Target, pkg string) Plan {
	return p.planHomebrew(target, pkg, true)
}

func (p requestPlanner) planHomebrew(target Target, pkg string, cask bool) Plan {
	s := p.Service
	if _, err := s.executables.LookPath("brew"); err != nil {
		return Plan{
			Target: target, Unsupported: true,
			Method: "homebrew", Reason: "Homebrew was not found on PATH. Install it from https://brew.sh first, then retry.",
		}
	}
	var installed bool
	if IsAgentTarget(target) {
		if p.capabilities == nil || p.capabilities.Homebrew.Err != nil {
			return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew packages could not be inspected."}
		}
		homebrew := p.capabilities.Homebrew
		prefix := homebrew.Prefix
		if prefix == "" {
			return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: "Homebrew's installation prefix could not be resolved."}
		}
		if !homebrew.PrefixWritable {
			return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: fmt.Sprintf("Homebrew's prefix %s is not writable by the current user. Repair Homebrew ownership; Orchestrator will not use sudo.", prefix)}
		}
		if cask {
			installed = homebrewPackageInstalled(homebrew.Casks, pkg)
		} else {
			installed = homebrewPackageInstalled(homebrew.Formulae, pkg)
		}
	}
	verb := "install"
	if installed {
		verb = "reinstall"
	}
	command := []string{"brew", verb}
	if cask {
		command = append(command, "--cask")
	}
	command = append(command, pkg)
	return Plan{Target: target, Command: command, Method: "homebrew", Package: pkg, PackageCask: cask}
}

func homebrewPackageInstalled(inventory map[string]bool, pkg string) bool {
	if inventory[pkg] {
		return true
	}
	if slash := strings.LastIndexByte(pkg, '/'); slash >= 0 {
		return inventory[pkg[slash+1:]]
	}
	return false
}

func (s *Service) planWinget(target Target, id string) Plan {
	if _, err := s.executables.LookPath("winget"); err != nil {
		plan := Plan{Target: target, Unsupported: true, Reason: "winget was not found on PATH."}
		if IsAgentTarget(target) {
			plan.Method = "winget"
		}
		return plan
	}
	command := []string{"winget", "install", "-e", "--id", id}
	if IsAgentTarget(target) {
		command = append(command, "--silent", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
		return Plan{Target: target, Command: command, Method: "winget", Package: id}
	}
	return Plan{Target: target, Command: command}
}

func withDocs(plan Plan, docsURL string) Plan {
	plan.DocsURL = docsURL
	return plan
}

var linuxPackageManagers = []string{"apt-get", "dnf", "pacman", "zypper", "apk"}

func (s *Service) planLinuxPackage(target Target, pkgFor func(mgr string) string) Plan {
	for _, mgr := range linuxPackageManagers {
		if _, err := s.executables.LookPath(mgr); err != nil {
			continue
		}
		argv := linuxInstallArgv(mgr, pkgFor(mgr))
		return Plan{
			Target: target, Command: argv, Manager: mgr, NeedsRoot: true, Unsupported: true,
			Reason: "Orchestrator cannot ask for your administrator password. Run the command below in a terminal.",
		}
	}
	return Plan{
		Target: target, Unsupported: true,
		Reason: fmt.Sprintf(
			"No supported Linux package manager (%s) was found.",
			strings.Join(linuxPackageManagers, ", "),
		),
	}
}

func linuxInstallArgv(mgr, pkg string) []string {
	switch mgr {
	case "apt-get":
		return []string{"apt-get", "install", "-y", pkg}
	case "dnf":
		return []string{"dnf", "install", "-y", pkg}
	case "pacman":
		return []string{"pacman", "-S", "--noconfirm", pkg}
	case "zypper":
		return []string{"zypper", "install", "-y", pkg}
	case "apk":
		return []string{"apk", "add", pkg}
	default:
		return nil
	}
}
