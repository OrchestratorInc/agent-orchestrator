// Package systeminstall executes real install commands from a fixed allowlist
// of system prerequisites and agent harnesses. This is
// the core security invariant of the package — a caller can only select
// which fixed Target value to install; the actual argv run on
// the machine is always built from hardcoded command shapes, never from
// caller-supplied strings. Runs are tracked as async Jobs so an HTTP handler
// never blocks on an installer that can take minutes.
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
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	// ErrHarnessActive prevents installers from replacing a binary used by a live session.
	ErrHarnessActive = errors.New("systeminstall: harness has an active session")
	// ErrInstallMethod reports an unknown or currently unavailable install method.
	ErrInstallMethod = errors.New("systeminstall: invalid install method")
	// ErrInstallActive prevents a second operation from being silently discarded.
	ErrInstallActive = errors.New("systeminstall: harness install operation already active")
	// ErrInstallOwner refuses to update a binary the selected method did not install.
	ErrInstallOwner = errors.New("systeminstall: installation owner not confirmed")
	// ErrUpdateVersion rejects an invalid caller-supplied verification target.
	ErrUpdateVersion = errors.New("systeminstall: invalid expected update version")
)

// Target is one of the fixed install targets AO knows how to install.
type Target string

// The exhaustive set of installable targets. No other value is ever accepted.
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
	TargetDeepSeek    Target = "deepseek-harness"
	TargetOpenHands   Target = "openhands"
	TargetCommandCode Target = "command-code"
	TargetMemcode     Target = "memcode"
	TargetHermes      Target = "hermes"
	// TargetCloudflared is the optional connector that makes a paired phone
	// reachable from outside the local network.
	TargetCloudflared Target = "cloudflared"
	// TargetCuaDriver is Cua Driver, the optional computer-use driver behind
	// the agents' computer-use tools.
	TargetCuaDriver Target = "cua-driver"
)

// agentTargets is the stable settings-page order.
var agentTargets = []Target{
	TargetClaudeCode, TargetCodex, TargetCursor, TargetOpencode, TargetOpencodeV2, TargetAider,
	TargetCopilot, TargetGrok, TargetKimi, TargetPi, TargetAmp, TargetAuggie,
	TargetDroid, TargetCrush, TargetCline, TargetGoose, TargetQwen, TargetGemini,
	TargetContinue, TargetDevin, TargetKiro, TargetKilocode, TargetVibe,
	TargetMuse, TargetAgy, TargetAutohand, TargetKimchi, TargetPrimeAgent,
	TargetOMP, TargetFX, TargetUnreal, TargetCodewhale, TargetMiMoCode, TargetDeepSeek, TargetOpenHands,
	TargetCommandCode, TargetMemcode, TargetHermes,
}

var agentTargetSet = func() map[Target]bool {
	out := make(map[Target]bool, len(agentTargets))
	for _, target := range agentTargets {
		out[target] = true
	}
	return out
}()

const openCodeV2ReplacementNotice = "Installing OpenCode 2 at the default location replaces the default OpenCode 1 `opencode` executable; the two majors are not installed side by side by default."

func installNotice(target Target) string {
	if target == TargetOpencodeV2 {
		return openCodeV2ReplacementNotice
	}
	return ""
}

// systemTargetSet is the stable contract of the legacy /system/install route.
// Agent-only targets use /agents/{agent}/install instead.
var systemTargetSet = map[Target]bool{
	TargetTmux: true, TargetGH: true, TargetClaude: true, TargetCloudflared: true,
	TargetCuaDriver: true, TargetCodex: true, TargetOpencode: true, TargetOpencodeV2: true, TargetCopilot: true,
}

// knownTargets is the exhaustive allowlist backing Valid.
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

// IsAgentTarget reports whether target is a user-facing harness id.
func IsAgentTarget(target Target) bool { return agentTargetSet[target] }

// IsSystemTarget reports whether target belongs to the legacy system-install
// route documented by InstallTargetParam.
func IsSystemTarget(target Target) bool { return systemTargetSet[target] }

// Valid reports whether target is a known prerequisite or agent install target.
func Valid(target Target) bool {
	return knownTargets[target]
}

// Resolve reports how target would be installed on goos, without running
// anything and without constructing a Service.
//
// This is the package's answer to "how is this installed here?" Keeping one
// resolver lets the daemon expose a plan preview and lets the bootstrap CLI
// print the same manual tmux remedy without either caller executing arbitrary
// input. The caller supplies the PATH lookup boundary explicitly.
func Resolve(goos string, lookPath func(string) (string, error), target Target) Plan {
	if !Valid(target) {
		return Plan{Target: target, Unsupported: true, Reason: "unknown install target"}
	}
	return (&Service{goos: goos, executables: executableFinderFunc(lookPath)}).resolvePlan(target)
}

type executableFinderFunc func(string) (string, error)

func (f executableFinderFunc) LookPath(file string) (string, error) { return f(file) }

// Plan is the resolved install command for a Target on the current platform.
//
// Command is populated whenever an install command could be resolved at all,
// including when Unsupported is true. Those two are independent on purpose:
// "we know exactly how to install this" and "this daemon is allowed to run it"
// are different questions. On Linux every package manager needs root, so the
// daemon refuses (Unsupported) while still reporting the argv for the desktop
// to display as a manual command. Callers that intend to execute a Plan must
// branch on Unsupported; callers that only need to know whether a route exists
// should test len(Command).
type Plan struct {
	Target  Target
	Command []string // argv, e.g. ["brew", "install", "tmux"]
	// Prepare runs before Command with the same environment, e.g. refreshing
	// Homebrew's metadata so `brew upgrade` sees the release AO announced. A
	// failed step is noted in the output; Command still runs and post-update
	// verification decides the outcome.
	Prepare             [][]string
	Script              *ports.InstallScriptCommand
	Package             string
	PackagePrefix       string
	PackageCask         bool
	Manager             string // resolving package manager ("brew", "apt-get", ...), empty when none applies
	NeedsRoot           bool   // Command must run as root; the caller supplies the privilege
	Unsupported         bool
	Reason              string // set when Unsupported, or as extra context otherwise
	Notice              string // fixed user-visible consequence that applies even when available
	Method              string
	DocsURL             string
	ExpectedDestination string
	// UpdateWhileRunning is set on update plans: whether the update may run
	// while sessions use the binary. Empty means blocked.
	UpdateWhileRunning UpdateWhileRunning
	// keepPreviousRelease asks the installer to leave the replaced release on
	// disk because live sessions still run it.
	keepPreviousRelease bool
	// managed installs this update side by side in AO's versioned directory
	// (see managed.go) instead of rewriting the installed copy.
	managed bool
	// managedPin is the release a managed reinstall installs again.
	managedPin string

	// daemonRuntimeRejected and daemonPrefixRejected mark an npm plan rejected
	// only for the daemon's own Node/npm or global prefix. An install runs
	// there, so it stays rejected; an update acts on the installation that owns
	// the binary, so it re-checks against that one instead.
	daemonRuntimeRejected bool
	daemonPrefixRejected  bool
	// deferredReason is the daemon rejection an update deferred; it applies
	// again when the owning installation turns out to use the daemon's runtime
	// or prefix, or cannot be found.
	deferredReason string
	// ownerPrefixFound records that targetInstalledCopy located the npm prefix
	// that owns the installed binary.
	ownerPrefixFound bool
}

// AgentPlan is the display-safe plan returned to the settings page. Command is
// a preview of fixed server-owned argv and is never accepted from the client.
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

// AgentInstallMethod is one server-owned installation recipe and its current
// viability. The renderer may select ID but never submits argv.
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

// AgentOperation selects a fixed install, reinstall, or update plan.
type AgentOperation string

const (
	// AgentOperationInstall requests a first-time installation.
	AgentOperationInstall AgentOperation = "install"
	// AgentOperationReinstall requests an explicit rebuild of an existing installation.
	AgentOperationReinstall AgentOperation = "reinstall"
	// AgentOperationUpdate refreshes the installed agent CLI using its selected method.
	AgentOperationUpdate AgentOperation = "update"
)

func (operation AgentOperation) valid() bool {
	return operation == AgentOperationInstall || operation == AgentOperationReinstall || operation == AgentOperationUpdate
}

// Status is the lifecycle state of an install Job.
type Status string

// The full set of Job lifecycle states.
const (
	StatusIdle    Status = "idle"
	StatusRunning Status = "running"
	// StatusWaitingForIdle holds an update until every session using the
	// harness is idle; it then stops them, updates and resumes them.
	StatusWaitingForIdle Status = "waiting_for_idle"
	StatusInstalling     Status = "installing"
	StatusVerifying      Status = "verifying"
	StatusSucceeded      Status = "succeeded"
	StatusFailed         Status = "failed"
	StatusUnsupported    Status = "unsupported"
	StatusInterrupted    Status = "interrupted"
)

// maxOutputBytes bounds Job.Output so a chatty installer can't grow memory
// unbounded — only the last ~4000 bytes are kept.
const maxOutputBytes = 4000

// defaultInstallTimeout bounds how long a single install run may take. A
// stalled installer (a network hang on curl, a held brew/apt lock, winget
// waiting on a prompt it'll never get) would otherwise pin its target in
// StatusRunning forever: no retry path, and the caller polls an indefinite
// progress bar. Real installs (npm global, brew, curl-piped scripts) normally
// finish in well under a minute; 15 minutes is generous headroom, not a
// realistic expected duration.
const defaultInstallTimeout = 15 * time.Minute

// defaultPersistenceTimeout keeps best-effort worker state writes from
// consuming the daemon's entire shutdown drain budget behind a blocked DB.
const defaultPersistenceTimeout = 2 * time.Second

var devinInstalledLine = regexp.MustCompile(`Installed devin v\S+ to [^\r\n]+/devin\.`)

// Job is the tracked state of one harness operation for a Target.
type Job struct {
	Target              Target `json:"target" enum:"tmux,gh,claude,claude-code,codex,cursor,opencode,opencode-v2,aider,copilot,grok,kimi,pi,amp,auggie,droid,crush,cline,goose,qwen,gemini,continue,devin,kiro,kilocode,vibe,muse,agy,autohand,kimchi,prime-agent,omp,fx,unreal-agent,codewhale,mimo-code,deepseek-harness,openhands,command-code,memcode,hermes,cloudflared,cua-driver" description:"Fixed install target this job ran (or is running) for."`
	Status              Status `json:"status" enum:"idle,running,waiting_for_idle,installing,verifying,succeeded,failed,unsupported,interrupted" description:"Current lifecycle state of the job. waiting_for_idle: the update runs once every session using the harness is idle (Windows)."`
	Method              string `json:"method,omitempty" description:"Server-owned installation method selected for this harness job."`
	Command             string `json:"command,omitempty" description:"Human-readable install command, e.g. \"brew install tmux\", for display even before/without output."`
	ExpectedDestination string `json:"expectedDestination,omitempty" description:"Expected or adapter-resolved executable destination."`
	Output              string `json:"output,omitempty" description:"Combined stdout+stderr from the install command, tail-capped to the last ~4000 bytes."`
	Error               string `json:"error,omitempty" description:"Set on failure or when the target is unsupported on this machine: the exec error, the Unsupported reason, or a timeout message."`
	Notice              string `json:"notice,omitempty" description:"Fixed user-visible consequence to acknowledge before running this install."`
	// Pointers, not time.Time: omitempty has no effect on a struct, so a bare
	// time.Time always serializes (as the zero value's "0001-01-01..."
	// timestamp) even when nothing has happened yet. A nil pointer actually
	// omits the field, matching FinishedAt's documented "zero until the job
	// finishes" contract.
	// WaitingOn lists the sessions a waiting_for_idle job still waits for.
	WaitingOn  []domain.SessionWait `json:"waitingOn,omitempty" description:"While waiting_for_idle, the sessions the update still waits for and why."`
	StartedAt  *time.Time           `json:"startedAt,omitempty"`
	FinishedAt *time.Time           `json:"finishedAt,omitempty" description:"Absent until the job finishes."`
	UpdatedAt  *time.Time           `json:"updatedAt,omitempty"`
}

// HarnessVerifier performs post-install binary verification without probing
// authentication.
type HarnessVerifier interface {
	Verify(ctx context.Context, target Target) (VerifyResult, error)
}

// SessionLister exposes durable lifecycle facts used to keep maintenance
// operations away from active harness processes.
type SessionLister interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// ReviewerLister exposes reviewer processes, which have no session row of
// their own: live TUI reviewer panes and reviewer chats with a live controller.
type ReviewerLister interface {
	ListLiveReviewerHandles(ctx context.Context) ([]domain.ReviewerHandle, error)
	ListRecoverableChatReviews(ctx context.Context) ([]domain.Review, error)
}

// Deps are the durable and adapter-backed dependencies used for harness jobs.
type Deps struct {
	JobStore ports.AgentInstallJobStore
	Verifier HarnessVerifier
	Sessions SessionLister
	// Reviewers, when set, keeps maintenance away from live reviewers too.
	Reviewers ReviewerLister
	// DisableUpdateChecks stops update advisories from reaching the network
	// (AO_HARNESS_UPDATE_CHECKS=off).
	DisableUpdateChecks bool
	// DisableUpdateWhileRunning makes every update wait for the harness's
	// sessions to end, as before update-while-running existed
	// (AO_UPDATE_WHILE_RUNNING=off).
	DisableUpdateWhileRunning bool
	// ManagedRoot is where AO keeps versioned harness installs (under the AO
	// data dir). Empty keeps every update in place.
	ManagedRoot string
	// Releases records which managed release each session launch runs, so a
	// release is deleted only once no live session runs it.
	Releases ManagedReleaseStore
	// UpdateWhenIdle lets an update that cannot run beside sessions wait for
	// them to be idle, stop them, update and resume them, instead of refusing
	// while they run (AO_UPDATE_WHEN_IDLE).
	UpdateWhenIdle     bool
	PrivateNPMPrefixes map[Target]string
}

// Service runs real install commands for the fixed Target allowlist.
type Service struct {
	mu                sync.Mutex
	jobs              map[Target]*Job
	backgroundContext context.Context
	stop              context.CancelFunc
	stopping          bool
	workers           sync.WaitGroup
	harnessGatesMu    sync.Mutex
	harnessGates      map[domain.AgentHarness]*sync.RWMutex

	executables         ports.ExecutableFinder
	commands            ports.CommandRunner
	installCommands     ports.InstallCommandRunner
	installScripts      ports.InstallScriptRunner
	installCapabilities ports.InstallCapabilityProbe
	pathWritable        ports.PathWritableProbe
	privateNPMPrefixes  map[Target]string
	jobStore            ports.AgentInstallJobStore
	verifier            HarnessVerifier
	sessions            SessionLister
	reviewers           ReviewerLister
	// goos selects the platform branch in planFor. Real use is always
	// runtime.GOOS; tests override it to exercise every OS branch from one
	// machine, the same seam lookPath provides for PATH probing.
	goos string
	// installTimeout bounds each run — see defaultInstallTimeout. Tests
	// override it with a short duration to exercise the timeout path without
	// a real multi-minute wait.
	installTimeout time.Duration
	// persistenceTimeout bounds worker-owned transition and terminal writes.
	persistenceTimeout   time.Duration
	onSucceeded          func(Target)
	managedVersion       managedVersionChecker
	ownsInstallation     func(context.Context, string, string, string, bool) (bool, error)
	updateAdvisories     map[Target]UpdateAdvisory
	updateChecksDisabled bool
	// updateWhileRunningDisabled forces every update to wait for sessions.
	updateWhileRunningDisabled bool
	// managedRoot holds AO's versioned harness installs; "" turns them off.
	managedRoot string
	// releases tracks the managed release each session launch runs.
	releases ManagedReleaseStore
	// invalidateBinary makes a harness adapter resolve its executable again.
	invalidateBinary func(Target)
	// updateWhenIdle turns a blocked or warn level into update-when-idle.
	updateWhenIdle bool
	// changing marks harnesses whose installation is being changed, so a
	// launch waits for the change instead of failing.
	changing map[Target]bool
	// idleUpdates are the waiting or running update-when-idle runs.
	idleUpdates map[Target]*idleUpdateRun
	// idleCoordinator stops and resumes a harness's sessions around such a run.
	idleCoordinator IdleUpdateCoordinator
	// telemetry receives update and version-move events; nil drops them.
	telemetry ports.EventSink
	// updateLevels remembers the level each running update was started under.
	updateLevels map[Target]UpdateWhileRunning
	// updateObserver marks sessions an update left on the replaced release.
	updateObserver HarnessUpdateObserver
	// deferredCleanups are Homebrew formulas whose old keg is kept until the
	// sessions that run it have moved to the new release.
	deferredCleanups    map[Target]string
	derivedOwners       map[Target]derivedOwner
	updateAdvisoryCalls map[Target]*updateAdvisoryCall
	// updateCheckSlots bounds concurrent update checks to
	// maxConcurrentUpdateChecks; created on first use.
	updateCheckSlots chan struct{}
	// officialVersion reads a vendor release channel when no package manager
	// owns the harness binary.
	officialVersion func(context.Context, Target) (string, error)
	// systemResolvers find system targets installed outside the daemon's PATH.
	systemResolvers map[Target]func(context.Context) (string, error)
}

// requestPlanner carries one immutable capability snapshot through all recipe
// resolution performed for a single HTTP/service request.
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

// SetOnSucceeded registers the daemon callback invoked after a successful
// harness operation. It is called outside the job mutex.
func (s *Service) SetOnSucceeded(callback func(Target)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSucceeded = callback
}

// New returns a Service backed by explicit host-operation ports. The daemon
// supplies their concrete adapter; core service code never invokes os/exec.
func New(executables ports.ExecutableFinder, commands ports.CommandRunner) *Service {
	return NewWithDeps(executables, commands, Deps{})
}

// NewWithDeps returns a Service with durable harness-job and verification
// dependencies supplied by the daemon.
func NewWithDeps(executables ports.ExecutableFinder, commands ports.CommandRunner, deps Deps) *Service {
	installCommands, _ := commands.(ports.InstallCommandRunner)
	installScripts, _ := commands.(ports.InstallScriptRunner)
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
		installTimeout:             defaultInstallTimeout,
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
	}
}

// AgentPlans resolves one installation plan for every supported harness
// without executing anything.
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
		reinstallByMethod := make(map[string]Plan, len(reinstallPlans))
		for _, reinstallPlan := range reinstallPlans {
			reinstallByMethod[reinstallPlan.Method] = reinstallPlan
		}
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
		out[plan.Method] = plan
	}
	return out
}

// operationPlanFor treats a method missing from a later listing, such as a
// derived owner cleared in between, as unsupported rather than misaligned.
func operationPlanFor(plans map[string]Plan, method string) Plan {
	if plan, ok := plans[method]; ok {
		return plan
	}
	return Plan{Method: method, Unsupported: true, Reason: "This installation method is no longer detected."}
}

// lastResortInstallers are official installers that clone a source repository
// rather than fetch a release artifact, so an available package manager is
// recommended ahead of them.
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

// Start begins the install for target, or returns the already-running Job if
// one is in flight (idempotent — it never starts a second concurrent run of
// the same target). target must be one of the fixed known values; anything else
// is a caller bug and returns an error the controller turns into a 400.
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

	plan := s.resolvePlan(target)
	command := displayCommand(plan)
	now := time.Now()
	if plan.Unsupported {
		job := &Job{
			Target:     target,
			Status:     StatusUnsupported,
			Command:    command,
			Error:      plan.Reason,
			Notice:     plan.Notice,
			StartedAt:  &now,
			FinishedAt: &now,
		}
		s.jobs[target] = job
		return *job, nil
	}

	job := &Job{
		Target:    target,
		Status:    StatusRunning,
		Command:   command,
		Notice:    plan.Notice,
		StartedAt: &now,
	}
	s.jobs[target] = job
	if s.stopping {
		job.Status = StatusInterrupted
		job.Error = "daemon shutdown interrupted the install"
		job.FinishedAt = &now
		return *job, nil
	}
	s.workers.Add(1)

	go func() { //nolint:gosec // bounded daemon-owned worker intentionally outlives the request.
		defer s.workers.Done()
		s.run(s.backgroundContext, plan, job)
	}()

	return *job, nil
}

// StartAgent begins a harness install using one server-owned method ID. An
// empty method chooses the first currently viable method for compatibility
// with older clients.
func (s *Service) StartAgent(ctx context.Context, target Target, method string) (Job, error) {
	return s.StartAgentOperation(ctx, target, method, AgentOperationInstall, "")
}

// StartAgentOperation begins a harness operation using one server-owned
// method ID and operation. An empty method chooses the recommended method
// for an install or reinstall and the method that owns the installed copy for
// an update. expected is the release the user approved for an update, or "".
// An update runs alongside live sessions, which restart on the new release
// once idle; see gateRunningSessions.
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
			// Only an update can run alongside sessions; anything else waits.
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
			// A managed update installs into AO's own directory, so the
			// installed copy's prefix and its Node do not apply.
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
		// An update never moves the installed copy backwards.
		if versionAtMost(baseline.scheme, expected, baseline.version) {
			return Job{}, ErrUpdateVersion
		}
		// Pin the version the user approved into this job's immutable baseline.
		// Concurrent advisory refreshes cannot erase this verification floor.
		baseline.expected = expected
	}
	if operation == AgentOperationUpdate && baseline != nil {
		// A cached latest at or below the version just probed is stale (the
		// harness was updated elsewhere); pinning it would move it backwards.
		if baseline.latest != "" && versionAtMost(baseline.scheme, baseline.latest, baseline.version) {
			baseline.latest = ""
		}
		// Install the release the user approved when it is one the advisory
		// knows exists, else the one the advisory announced. The client value
		// never reaches the command unless it lies between installed and latest.
		pin := baseline.latest
		if baseline.expected != "" && baseline.version != "" && versionAtMost(baseline.scheme, baseline.expected, baseline.latest) {
			pin = baseline.expected
		}
		if pin != "" {
			// The job succeeds once it reaches the release it installs, even
			// if a newer one was announced meanwhile.
			baseline.latest = pin
		}
		plan = pinVendorUpdate(plan, pin)
	}
	if operation == AgentOperationUpdate {
		// Sessions are checked once the plan is final: whether an update may run
		// alongside them depends on the method that owns the installed copy.
		withSessions, err := s.gateRunningSessions(ctx, target, operation, plan)
		var active *HarnessActiveError
		switch {
		case errors.Is(err, errUpdateWhenIdle) && !plan.Unsupported:
			s.emitHarnessUpdate(target, UpdateWhileRunningIdle, "waiting", 0)
			// Wait for idle sessions without holding the harness meanwhile.
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
		// effectiveUpdateLevel takes s.mu itself, so it runs before the lock.
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
		// A refused update or reinstall changes nothing, so it is reported to
		// the caller without replacing the record of the installation's last job.
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

// owningUpdatePlan picks the update plan whose method owns the binary
// sessions run, for an update request that names no method. The latest
// advisory's maintenance method is preferred; otherwise each available
// method is checked against the resolved binary. ok is false when no method
// is confirmed, and the caller's plan is then refused by confirmInstalledOwner.
func (s *Service) owningUpdatePlan(ctx context.Context, target Target, plans []Plan) (Plan, bool) {
	s.mu.Lock()
	advisory, hasAdvisory := s.updateAdvisories[target]
	s.mu.Unlock()
	if hasAdvisory && advisory.MaintenanceMethod != "" {
		for _, plan := range plans {
			if plan.Method == advisory.MaintenanceMethod && !plan.Unsupported {
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

// TryBeginHarnessUse prevents session launches from racing replacement of the
// selected harness executable. The returned release must be called after launch.
// A launch also notices a binary replaced outside AO and drops its stale
// update advisory.
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

// Status returns the current or last known Job for target. A target that has
// never been started returns a plan preview: Idle when the daemon can run it,
// or Unsupported with a manual command/reason when it cannot. An error is
// returned only when target is not a known install target.
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

// AgentJobs returns the latest durable job for every harness that has one.
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

// Recover marks unfinished durable jobs interrupted and hydrates the in-memory
// coordination map before the HTTP server starts accepting requests.
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

// Verify starts adapter-backed verification without rerunning an installer.
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
	job := &Job{Target: target, Status: StatusVerifying, Notice: installNotice(target), StartedAt: &now, UpdatedAt: &now}
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

// Close cancels and drains daemon-owned harness installation work.
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

// run executes plan in the background and records the outcome onto job. job
// is only ever mutated here and read back through a copy under s.mu, so
// concurrent Start/Status calls never race with this goroutine's writes.
// The run is bounded by installTimeout so a stalled installer eventually
// surfaces as a failure instead of pinning the target in StatusRunning.
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
	// A clean run of a system target must leave its binary findable. Checked
	// before taking s.mu: a resolver may probe the filesystem.
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
	job.Status = StatusSucceeded
	callback := s.onSucceeded
	target := job.Target
	s.mu.Unlock()
	if callback != nil {
		callback(target)
	}
}

// SetSystemTargetResolver registers how to find target's executable once it
// is installed, for a tool whose installer puts it somewhere the daemon's own
// PATH may not reach (a desktop app launched from the Dock does not inherit
// the shell's ~/.local/bin).
func (s *Service) SetSystemTargetResolver(target Target, resolve func(context.Context) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.systemResolvers == nil {
		s.systemResolvers = make(map[Target]func(context.Context) (string, error))
	}
	s.systemResolvers[target] = resolve
}

// systemTargetInstalled reports whether a system target's executable can now
// be found: on PATH, or through its registered resolver.
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
		// Live sessions still run the old keg; it is cleaned up after they move.
		env = append(env, "HOMEBREW_NO_INSTALL_CLEANUP=1")
		s.deferHomebrewCleanup(plan.Target, packageWithoutLatest(plan.Package))
	}
	var runErr error
	if plan.managed {
		runErr = s.runManagedInstall(ctx, plan, baseline, env, outputWriter)
	} else if plan.Script != nil {
		if s.installScripts == nil {
			runErr = errors.New("remote installer runner is not configured")
		} else {
			command := *plan.Script
			command.Env = append(append([]string(nil), env...), command.Env...)
			var result ports.InstallScriptResult
			result, runErr = s.installScripts.RunInstallScript(ctx, command, outputWriter, outputWriter)
			if result.SHA256 != "" {
				_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
			}
		}
	} else {
		if plan.Method == "official-installer" {
			env = append(env, vendorUpdateEnv[plan.Target]...)
		}
		runCommand := func(argv []string) error {
			if s.installCommands != nil {
				return s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: env}, out, out)
			}
			return s.commands.Run(ctx, argv, out, out)
		}
		for _, argv := range plan.Prepare {
			if err := runCommand(argv); err != nil && ctx.Err() == nil {
				_, _ = fmt.Fprintf(out, "\n%s failed (%v); continuing.\n", strings.Join(argv, " "), err)
			}
		}
		if ctx.Err() == nil {
			runErr = runCommand(plan.Command)
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		s.finishAgentJob(job, StatusFailed, out.String(), fmt.Sprintf("%s timed out after %s", operation, s.installTimeout), "")
		return
	}
	if ctx.Err() == context.Canceled {
		s.finishAgentJob(job, StatusInterrupted, out.String(), fmt.Sprintf("daemon shutdown interrupted %s", operation), "")
		return
	}
	installConfirmed := runErr != nil && devinOutput != nil && devinOutput.Confirmed()
	// Autohand's Windows installer can finish replacing the CLI, then fail
	// while installing its separate Computer control component. Verify the
	// actual CLI version before deciding whether the requested update worked.
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
		s.finishAgentJob(job, StatusFailed, "", fmt.Sprintf("persist verifying state: %v", err), "")
		return
	}
	updaterReport := ""
	if plan.Script == nil {
		updaterReport = lastOutputLine(out.String())
	}
	s.runAgentVerification(s.backgroundContext, job, baseline, updaterReport)
}

// maxUpdaterReportRunes keeps the updater line quoted in a failed update
// short enough for the update notice.
const maxUpdaterReportRunes = 80

// installFailure is a failed operation with a short summary for the update
// notice. err, which may name paths or quote output, goes to diagnostics.
type installFailure struct {
	summary string
	err     error
}

func (e *installFailure) Error() string { return e.summary }
func (e *installFailure) Unwrap() error { return e.err }

func installFailed(summary string, err error) error {
	return &installFailure{summary: summary, err: err}
}

// failureSummary is the job error for err. An installFailure's detail is
// written to out, the job's diagnostics, so the notice keeps only the summary.
func failureSummary(err error, out io.Writer) string {
	var failure *installFailure
	if errors.As(err, &failure) && failure.err != nil {
		_, _ = fmt.Fprintf(out, "\n%s\n%v\n", failure.summary, failure.err)
	}
	return err.Error()
}

// terminalEscape matches ANSI CSI and OSC sequences that colour or retitle
// terminal output.
var terminalEscape = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// lastOutputLine is the last non-empty line a command printed, as a terminal
// would show it: escape sequences removed and only the text after a line's
// last carriage return, shortened to fit an error message.
func lastOutputLine(output string) string {
	output = terminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r\n", "\n"), "")
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		// A terminal keeps the last carriage-return segment that printed text,
		// including one followed by a trailing bare \r.
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

// runAgentVerification re-probes the harness after a job. For an update, the
// baseline from before the command decides whether the binary sessions run
// actually changed; a zero exit status alone is not proof. updaterReport is
// the update command's last line: self-updaters can exit 0 without replacing
// the binary ("already up to date", "update manually", "restart to finish"),
// and a failed update quotes it so the user sees which.
func (s *Service) runAgentVerification(ctx context.Context, job *Job, baseline *installedBaseline, updaterReport string) {
	if s.verifier == nil {
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
		// Only updates carry a baseline. Mark the sessions it left behind
		// before reporting success, while launches still wait on this update.
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
	// The next advisory re-derives the owner of whatever binary now runs.
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
		Output: record.Output, Error: record.Error, Notice: installNotice(Target(record.Target)), StartedAt: &startedAt,
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

// displayCommand renders the plan for display. Prepare steps are joined with
// "; " rather than "&&": a failed step is noted and Command still runs.
func displayCommand(plan Plan) string {
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
	steps := make([]string, 0, len(plan.Prepare)+1)
	for _, prepare := range plan.Prepare {
		steps = append(steps, strings.Join(prepare, " "))
	}
	return strings.Join(append(steps, strings.Join(argv, " ")), "; ")
}

// capturedOutput is an io.Writer that keeps only the last max bytes written,
// trimming from the front.
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

// devinInstallOutput remembers whether Devin reported a completed binary
// install, even after the bounded destination evicts that line from diagnostics.
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

// planFor resolves the install Plan for target on the current platform,
// probing PATH via s.executables so tests can inject deterministic results.
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
			Reason: "tmux is not required on Windows; AO uses the built-in ConPTY terminal runtime instead.",
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

// planCloudflared mirrors planGH: Cloudflare publishes cloudflared through
// Homebrew and winget, and Linux distributions package it too.
//
// Deliberately not a downloaded binary. Fetching a release archive ourselves
// would mean pinning per-platform URLs, verifying checksums and clearing
// macOS quarantine before executing it — a fourth install shape this package
// does not have, for a tool the three it does have already cover. The one
// curl-piped target here is opencode, and only because that is the vendor's
// own documented installer.
func (s *Service) planCloudflared() Plan {
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

// Cua Driver's official installers. Both install per user without
// administrator rights: macOS puts CuaDriver.app in /Applications, Linux and
// macOS link the binary into ~/.local/bin, and Windows installs under
// %LOCALAPPDATA%\Programs\Cua. Rerunning either installs the latest release,
// so the same plan also updates.
const (
	cuaDriverUnixInstaller    = "https://cua.ai/driver/install.sh"
	cuaDriverWindowsInstaller = "https://cua.ai/driver/install.ps1"
	cuaDriverDocs             = "https://cua.ai/docs/how-to-guides/driver/install"
)

// planCuaDriver runs the official installer. When the shell AO would run it
// with is missing from the daemon's PATH, the plan is unsupported but carries
// Cua's own one-line install command, so Settings offers it to copy instead of
// an Install button that fails the same way again.
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
			// The daemon's Node/npm cannot run this recipe. Reported before any
			// prefix problem, as it always was; an update may still use another.
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
				plan.Reason = fmt.Sprintf("npm's global prefix %s is not writable by the current user. Configure a user-owned prefix; AO will not use sudo.", prefix)
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
	case TargetPi:
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
		Reason: "AO does not automatically execute opencode's mutable remote installer script.",
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
			return Plan{Target: target, Unsupported: true, Method: "homebrew", Reason: fmt.Sprintf("Homebrew's prefix %s is not writable by the current user. Repair Homebrew ownership; AO will not use sudo.", prefix)}
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

// linuxPackageManagers is probed in this fixed order; the first one found on
// PATH is used.
var linuxPackageManagers = []string{"apt-get", "dnf", "pacman", "zypper", "apk"}

// planLinuxPackage resolves a Linux install command for target via the first
// available package manager. pkgFor lets a target use a different package
// name on a given manager (e.g. gh is "github-cli" on pacman).
//
// AO deliberately never elevates privileges on the user's behalf (no auto
// sudo, no pkexec): every one of apt-get/dnf/pacman/zypper install requires
// root, so running the resolved command as the desktop user is guaranteed to
// fail with a permission error. Rather than expose a button that always
// fails, this always resolves as Unsupported on Linux. Command carries the
// exact argv and displayCommand adds sudo for the user-facing manual remedy.
func (s *Service) planLinuxPackage(target Target, pkgFor func(mgr string) string) Plan {
	for _, mgr := range linuxPackageManagers {
		if _, err := s.executables.LookPath(mgr); err != nil {
			continue
		}
		argv := linuxInstallArgv(mgr, pkgFor(mgr))
		return Plan{
			Target: target, Command: argv, Manager: mgr, NeedsRoot: true, Unsupported: true,
			Reason: "AO cannot ask for your administrator password. Run the command below in a terminal.",
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
