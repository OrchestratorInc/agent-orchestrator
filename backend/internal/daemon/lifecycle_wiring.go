package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitydispatch"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/fx/herdr"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/container/dockerreap"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	workspacerouter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/router"
	scratchworkspace "github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/scratch"
	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	activityobserver "github.com/aoagents/agent-orchestrator/backend/internal/observe/activity"
	artifactsobserver "github.com/aoagents/agent-orchestrator/backend/internal/observe/artifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const startupReconcileWorkers = 4

type notificationSink interface {
	Notify(context.Context, ports.NotificationIntent) error
	Resolve(context.Context, ports.NotificationResolution) error
}

type lifecycleStack struct {
	LCM               *lifecycle.Manager
	ArtifactVersions  *artifactversions.Capturer
	runtimeReaper     *reaper.Reaper
	reaperStart       chan struct{}
	reaperStartOnce   sync.Once
	reaperDone        <-chan struct{}
	activityDone      <-chan struct{}
	turnFailureDone   <-chan struct{}
	artifactsDone     <-chan struct{}
	autoReviewDone    <-chan struct{}
	scmDone           <-chan struct{}
	trackerDone       <-chan struct{}
	herdr             *herdr.Server
	automationDone    <-chan struct{}
	activity          *activityobserver.Observer
	activityStart     chan struct{}
	activityStartOnce sync.Once
}

func startLifecycle(ctx context.Context, dataDir string, store *sqlite.Store, runtime ports.Runtime, messenger ports.AgentMessenger, notifier notificationSink, telemetry ports.EventSink, agents ports.AgentResolver, logger *slog.Logger) *lifecycleStack {
	lcm := lifecycle.New(store, messenger,
		lifecycle.WithNotificationSink(notifier),
		lifecycle.WithTelemetry(telemetry),
		lifecycle.WithDeliveryTelemetry(deliverytelemetry.New(telemetry, store)),
		lifecycle.WithContainerReaper(dockerreap.New(), store),
		lifecycle.WithActiveSteering(activeTurnSteering(agents)),
		lifecycle.WithStartupSignalGate(startupSignalGatesInput(agents)),
		lifecycle.WithUrgentNudgeGate(urgentNudgeWaitingInputSafe(agents)),
		lifecycle.WithDataDir(dataDir),
	)
	reaperStart := make(chan struct{})
	rp := reaper.New(lcm, store, runtime, reaper.Config{Logger: logger, StartAfter: reaperStart})
	activityStart := make(chan struct{})
	activityPoller := activityobserver.New(store, lcm, runtime, agents, activityobserver.Config{Logger: logger, DataDir: dataDir, StartAfter: activityStart})
	turnFailurePoller := activityobserver.NewTurnFailureObserver(store, lcm, agents, activityobserver.TurnFailureConfig{Logger: logger})
	herdrServer, err := herdr.Start(ctx, dataDir, lcm, logger)
	if err != nil {
		logger.Warn("fx Herdr listener unavailable", "error", err)
	}
	artifactVersions := artifactversions.New(store, dataDir, logger)
	artifactsPoller := artifactsobserver.New(store, lcm, artifactVersions, artifactsobserver.Config{Logger: logger})
	return &lifecycleStack{
		LCM:              lcm,
		ArtifactVersions: artifactVersions,
		runtimeReaper:    rp,
		reaperStart:      reaperStart,
		reaperDone:       rp.Start(ctx),
		activityDone:     activityPoller.Start(ctx),
		turnFailureDone:  turnFailurePoller.Start(ctx),
		herdr:            herdrServer,
		artifactsDone:    artifactsPoller.Start(ctx),
		activity:         activityPoller,
		activityStart:    activityStart,
	}
}

func startupSignalGatesInput(agents ports.AgentResolver) func(domain.AgentHarness) bool {
	return func(harness domain.AgentHarness) bool {
		if agents == nil {
			return false
		}
		agent, ok := agents.Agent(harness)
		if !ok {
			return false
		}
		signaler, ok := agent.(ports.StartupInputReadinessSignaler)
		return ok && signaler.FirstSignalProvesInputReady()
	}
}

func urgentNudgeWaitingInputSafe(agents ports.AgentResolver) func(domain.AgentHarness) bool {
	return func(harness domain.AgentHarness) bool {
		if agents == nil {
			return false
		}
		agent, ok := agents.Agent(harness)
		if !ok {
			return false
		}
		signaler, ok := agent.(ports.BlockedActivitySignaler)
		return ok && signaler.EmitsBlockedActivity()
	}
}

func (l *lifecycleStack) ReconcileRuntime(ctx context.Context) error {
	return l.runtimeReaper.Tick(ctx)
}

func (l *lifecycleStack) StartReaperLoop() {
	l.reaperStartOnce.Do(func() { close(l.reaperStart) })
}

func (l *lifecycleStack) StartActivityObserver(env activityobserver.SessionEnvFunc) {
	if l.activity == nil {
		return
	}
	if env != nil {
		l.activity.SetSessionEnv(env)
	}
	if l.activityStart != nil {
		l.activityStartOnce.Do(func() { close(l.activityStart) })
	}
}

func activeTurnSteering(agents ports.AgentResolver) func(domain.AgentHarness) bool {
	return func(harness domain.AgentHarness) bool {
		if agents == nil {
			return false
		}
		agent, ok := agents.Agent(harness)
		if !ok {
			return false
		}
		steerer, ok := agent.(ports.ActiveTurnSteerer)
		return ok && steerer.SteersActiveTurn()
	}
}

func (l *lifecycleStack) Stop() {
	if l.herdr != nil {
		l.herdr.Stop()
	}
	<-l.reaperDone
	if l.activityDone != nil {
		<-l.activityDone
	}
	if l.turnFailureDone != nil {
		<-l.turnFailureDone
	}
	if l.artifactsDone != nil {
		<-l.artifactsDone
	}
	if l.autoReviewDone != nil {
		<-l.autoReviewDone
	}
	if l.scmDone != nil {
		<-l.scmDone
	}
	if l.trackerDone != nil {
		<-l.trackerDone
	}
	if l.automationDone != nil {
		<-l.automationDone
	}
}

type sessionLifecycle interface {
	Reconcile(ctx context.Context) error
	ReconcileStartupSafety(ctx context.Context) error
	ReconnectAwakeAgent(ctx context.Context, target domain.AwakeAgentTarget) error
	ReconcileBackground(ctx context.Context) error
	HibernateIdleChats(ctx context.Context) error
	ReconcileWorkspaceCleanup(ctx context.Context) error
	RefreshStaleChats(ctx context.Context, minIdle time.Duration) error
	RefreshStaleTerminals(ctx context.Context, minIdle time.Duration) error
	BusyHarnessSessions(ctx context.Context, harness domain.AgentHarness) ([]domain.SessionWait, error)
	StopHarnessSessions(ctx context.Context, harness domain.AgentHarness) ([]domain.SessionID, error)
	ResumeHarnessSessions(ctx context.Context, ids []domain.SessionID) error
	SessionRuntimeEnv(ctx context.Context, rec domain.SessionRecord) (map[string]string, error)
	SetVersionTelemetry(sink ports.EventSink)
	SetVersionRefreshPolicy(policy func() sessionmanager.VersionRefreshPolicy)
	SetComputerUseEnabled(enabled func(context.Context) bool)
	RetryVersionMoves(ctx context.Context) error
	ObserveChatVersionSwitch(id domain.SessionID, version, result string)
	MarkHarnessUpdated(ctx context.Context, harness domain.AgentHarness, version string, at time.Time) error
	CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error)
	RestoreAll(ctx context.Context) error
	WaitBackgroundWorkers(ctx context.Context) error
	WaitAgentSwitchWorkers(ctx context.Context) error
	StopAgentsForQuit(ctx context.Context) error
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
	SetShellTerminalCloser(closer sessionmanager.ShellTerminalCloser)
	AcquireSessionInput(id domain.SessionID) (release func(), ok bool)
	SessionMutationInProgress(id domain.SessionID) bool
	SetTerminalInputGate(gate sessionmanager.TerminalInputGate)
	SetReviewerTerminator(terminator sessionmanager.ReviewerTerminator)
	SetHarnessUseGate(gate sessionmanager.HarnessUseGate)
	SetImportArchiveRefresher(refresher sessionmanager.ImportArchiveRefresher)
	PersistChatModel(ctx context.Context, id domain.SessionID, model string) error
}

type sessionLifecycleMessenger struct {
	sessionLifecycle
}

func (m sessionLifecycleMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	return m.sessionLifecycle.Send(ctx, id, message, nil)
}

func telemetryEmitsSpawned(cfg config.Config) bool {
	if !cfg.Telemetry.Events {
		return false
	}
	return !telemetryadapter.EventDenied(cfg.Telemetry.DisabledEvents, "ao.sessions.spawned")
}

func startSession(ctx context.Context, cfg config.Config, runtime runtimeselect.Runtime, store *sqlite.Store, lcm *lifecycle.Manager, artifactVersions *artifactversions.Capturer, messenger ports.AgentMessenger, telemetry ports.EventSink, notifications notificationSink, agents ports.AgentResolver, agentReadiness ports.AgentReadinessProvider, previewLifecycle sessionmanager.PreviewLifecycle, browserLifecycle sessionmanager.BrowserLifecycle, browserCapabilities sessionmanager.BrowserCapabilityIssuer, chat sessionmanager.ChatLauncher, reviewerRecoveryDone <-chan struct{}, defaults sessionmanager.SessionModeDefaults, tracker ports.Tracker, codexOperationGate ports.CodexOperationGate, reviewers *reviewer.Resolver, reviewRefresher reviewsvc.ThreadRefresher, log *slog.Logger) (*sessionsvc.Service, reviewsvc.Manager, sessionLifecycle, error) {
	gitWS, err := gitworktree.New(gitworktree.Options{
		ManagedRoot:  filepath.Join(cfg.DataDir, "worktrees"),
		RepoResolver: projectRepoResolver{store: store},
		Logger:       log,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("session workspace: %w", err)
	}
	scratchWS, err := scratchworkspace.New(scratchworkspace.Options{
		ManagedRoot: filepath.Join(cfg.DataDir, "worktrees"),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("scratch session workspace: %w", err)
	}
	ws := workspacerouter.New(workspacerouter.Deps{
		Git:      gitWS,
		Scratch:  scratchWS,
		Projects: store,
	})
	mgr := sessionmanager.New(sessionmanager.Deps{
		Runtime:             runtime,
		Agents:              agents,
		Workspace:           ws,
		Store:               store,
		Messenger:           messenger,
		Chat:                chat,
		Defaults:            defaults,
		Lifecycle:           lcm,
		Preview:             previewLifecycle,
		Browser:             browserLifecycle,
		BrowserCapabilities: browserCapabilities,
		AgentBrowserURL:     cfg.AgentBrowserURL,
		AgentBrowserToken:   cfg.AgentBrowserToken,
		DataDir:             cfg.DataDir,
		RunFilePath:         cfg.RunFilePath,
		BackgroundContext:   ctx,
		Logger:              log,
		ReconcileWorkers:    startupReconcileWorkers,
		CodexOperationGate:  codexOperationGate,
	})
	mgr.SetAgentReadiness(agentReadiness)
	scmProvider := newMultiSCMProvider(cfg.GitLab, log)
	var githubIdentity ports.ScopedIdentityResolver
	if scmProvider != nil && telemetryEmitsSpawned(cfg) {
		githubIdentity = scmProvider
	}
	deps := sessionsvc.Deps{
		Manager:              mgr,
		Store:                store,
		PRClaimer:            store,
		SCM:                  scmProvider,
		DataDir:              cfg.DataDir,
		Tracker:              tracker,
		Telemetry:            telemetry,
		Milestones:           installMilestones(cfg),
		Delivery:             lcm.DeliveryTelemetry(),
		Logger:               log,
		BackgroundContext:    ctx,
		AgentReadiness:       agentReadiness,
		GithubIdentity:       githubIdentity,
		SignalCapable:        activitydispatch.FullySupportsHarness,
		OutputTypeReconciler: lcm,
		ArtifactDeleter:      artifactVersions,
		ShareLinks:           sessionartifacts.NewShareLinks(cfg.DataDir, cfg.CloudControlPlaneURL),
	}
	if artifactVersions != nil {
		deps.ArtifactTracker = artifactVersions
	}
	sessionSvc := sessionsvc.NewWithDeps(deps)
	mgr.SetBranchStateRecorder(sessionSvc)
	if reviewers == nil {
		if reviewers, err = reviewer.NewResolver(); err != nil {
			return nil, nil, nil, fmt.Errorf("reviewer resolver: %w", err)
		}
	}
	reviewerChat, _ := chat.(reviewcore.ReviewerChatController)
	reviewEngine := reviewcore.New(reviewcore.Deps{
		HarnessUse: mgr,
		Store:      store,
		Sessions:   store,
		PRs:        store,
		Projects:   store,
		Launcher: reviewcore.NewLauncher(reviewers, runtime, cfg.DataDir,
			reviewcore.WithRunFilePath(cfg.RunFilePath),
			reviewcore.WithAgentAuth(reviewerAgentAuth{readiness: agentReadiness}),
			reviewcore.WithReviewerChat(reviewerChat)),

		ChatRecoveryDone: reviewerRecoveryDone,
	})
	reviewOpts := []reviewsvc.Option{
		reviewsvc.WithTelemetry(telemetry),
		reviewsvc.WithNotificationSink(notifications),
		reviewsvc.WithCodexAccountOperationGate(codexOperationGate),
	}
	if scmProvider != nil {
		reviewOpts = append(reviewOpts,
			reviewsvc.WithReviewRequester(scmProvider),
			reviewsvc.WithReviewResolver(scmProvider),
		)
	}
	reviewOpts = append(reviewOpts, reviewsvc.WithPRRefresher(reviewPRRefresher{sessions: sessionSvc}))
	if reviewRefresher != nil {
		reviewOpts = append(reviewOpts, reviewsvc.WithThreadRefresher(reviewRefresher))
	}
	reviewSvc := reviewsvc.New(reviewEngine, store, reviewOpts...)
	mgr.SetReviewerTerminator(reviewSvc)
	return sessionSvc, reviewSvc, mgr, nil
}

type runtimeMessageSender interface {
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
}

type terminalMessageAdmission struct {
	sync.Mutex
	pending  string
	baseline string
	handleID string
	launchID string
}

const terminalAdmissionCaptureLines = 200

type runtimeMessenger struct {
	store      *sqlite.Store
	runtime    runtimeMessageSender
	agents     ports.AgentResolver
	inputLocks *sync.Map
}

func (m runtimeMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session %s: %w", id, sessionmanager.ErrNotFound)
	}
	if rec.IsTerminated {
		return fmt.Errorf("session %s: %w", id, sessionmanager.ErrTerminated)
	}
	if m.agents != nil {
		agent, found := m.agents.Agent(rec.Harness)
		if policy, supports := agent.(interface{ ValidateTerminalMessage(string, string) error }); found && supports {
			lock, _ := m.inputLocks.LoadOrStore(id, &terminalMessageAdmission{})
			admission, validAdmission := lock.(*terminalMessageAdmission)
			if !validAdmission {
				return fmt.Errorf("session %s: invalid input admission state", id)
			}
			admission.Lock()
			defer admission.Unlock()
			reader, supports := m.runtime.(interface {
				GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error)
			})
			if !supports {
				return fmt.Errorf("session %s: current rendered terminal unavailable", id)
			}
			screen, err := reader.GetStyledOutput(ctx, ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID}, terminalAdmissionCaptureLines)
			if err != nil {
				return err
			}
			witness, hasWitness := agent.(interface {
				TerminalMessageObserved(string, string, string) bool
			})
			if admission.pending != "" && (admission.handleID != rec.Metadata.RuntimeHandleID || admission.launchID != rec.Metadata.RuntimeLaunchID) {
				admission.pending = ""
			}
			if admission.pending != "" {
				if !hasWitness || !witness.TerminalMessageObserved(admission.pending, admission.baseline, screen) {
					return fmt.Errorf("session %s: prior terminal submission remains unconfirmed; check the terminal, then kill and restore the session to resume Orchestrator input", id)
				}
				admission.pending = ""
			}
			if err := policy.ValidateTerminalMessage(message, screen); err != nil {
				return err
			}
			current, exists, err := m.store.GetSession(ctx, id)
			if err != nil {
				return err
			}
			if !exists || current.IsTerminated || current.Metadata.RuntimeHandleID != rec.Metadata.RuntimeHandleID || current.Metadata.RuntimeLaunchID != rec.Metadata.RuntimeLaunchID {
				return fmt.Errorf("session %s: runtime changed before input", id)
			}
			admission.pending = message
			admission.baseline = screen
			admission.handleID = rec.Metadata.RuntimeHandleID
			admission.launchID = rec.Metadata.RuntimeLaunchID
			if err := m.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID}, message); err != nil {
				return fmt.Errorf("session %s: terminal submission may have been written: %w", id, err)
			}
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				frame, captureErr := reader.GetStyledOutput(ctx, ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID}, terminalAdmissionCaptureLines)
				if captureErr == nil && hasWitness && witness.TerminalMessageObserved(message, admission.baseline, frame) {
					admission.pending = ""
					return nil
				}
				select {
				case <-ctx.Done():
					return fmt.Errorf("session %s: terminal submission written but unconfirmed: %w", id, ctx.Err())
				case <-deadline.C:
					return fmt.Errorf("session %s: terminal submission written but native echo unconfirmed; do not automatically retry", id)
				case <-ticker.C:
				}
			}

		}
	}
	handleID := rec.Metadata.RuntimeHandleID
	if handleID == "" {
		return fmt.Errorf("session %s: %w", id, sessionmanager.ErrIncompleteHandle)
	}
	return m.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: handleID}, message)
}

func newSessionMessenger(store *sqlite.Store, runtime runtimeMessageSender, _ *slog.Logger, resolvers ...ports.AgentResolver) ports.AgentMessenger {
	var agents ports.AgentResolver
	if len(resolvers) > 0 {
		agents = resolvers[0]
	}
	return runtimeMessenger{store: store, runtime: runtime, agents: agents, inputLocks: &sync.Map{}}
}

type modeAwareMessenger struct {
	mu     sync.RWMutex
	target ports.AgentMessenger
	ready  chan struct{}
	once   sync.Once
}

func newModeAwareMessenger() *modeAwareMessenger {
	return &modeAwareMessenger{ready: make(chan struct{})}
}

func (m *modeAwareMessenger) Bind(target ports.AgentMessenger) {
	m.mu.Lock()
	m.target = target
	m.mu.Unlock()
	m.once.Do(func() { close(m.ready) })
}

func (m *modeAwareMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	m.mu.RLock()
	target := m.target
	m.mu.RUnlock()
	if target == nil {
		select {
		case <-m.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
		m.mu.RLock()
		target = m.target
		m.mu.RUnlock()
	}
	if target == nil {
		return fmt.Errorf("mode-aware lifecycle messenger bound without a target")
	}
	return target.Send(ctx, id, message)
}

func buildAgentRegistry() (*adapters.Registry, error) {
	return agentregistry.Build()
}

type agentRegistry struct {
	reg *adapters.Registry
}

var _ ports.AgentResolver = agentRegistry{}

func (a agentRegistry) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	adapter, ok := a.reg.Get(string(harness))
	if !ok {
		return nil, false
	}
	agent, ok := adapter.(ports.Agent)
	return agent, ok
}

type reviewerAgentAuth struct {
	readiness ports.AgentReadinessProvider
}

func (r reviewerAgentAuth) AuthStatus(ctx context.Context, harness domain.ReviewerHarness) (ports.AgentAuthStatus, bool, error) {
	if r.readiness == nil {
		return "", false, nil
	}
	snapshot, err := r.readiness.EnsureAgentReadiness(ctx, string(harness), domain.AgentReadinessPurposeLaunch)
	if err != nil {
		return "", false, err
	}
	switch snapshot.Authentication.State {
	case domain.AgentAuthenticationAuthorized, domain.AgentAuthenticationNotApplicable:
		return ports.AgentAuthStatusAuthorized, true, nil
	case domain.AgentAuthenticationConfigured:
		return ports.AgentAuthStatusConfigured, true, nil
	case domain.AgentAuthenticationUnauthorized:
		return ports.AgentAuthStatusUnauthorized, true, nil
	default:
		return ports.AgentAuthStatusUnknown, true, nil
	}
}

func buildAgentResolver(defaultAgent string, log *slog.Logger) (ports.AgentResolver, error) {
	if defaultAgent == "" {
		defaultAgent = config.DefaultAgent
	}
	reg, err := buildAgentRegistry()
	if err != nil {
		return nil, err
	}
	resolver := agentRegistry{reg: reg}
	if _, ok := resolver.Agent(domain.AgentHarness(defaultAgent)); !ok {
		return nil, fmt.Errorf("configured agent %q is not a registered adapter", defaultAgent)
	}
	ids := make([]string, 0)
	for _, mf := range reg.Manifests() {
		ids = append(ids, mf.ID)
	}
	log.Info("built per-session agent resolver", "default", defaultAgent, "registered", ids)
	return resolver, nil
}

type projectRepoResolver struct{ store *sqlite.Store }

var _ gitworktree.RepoResolver = projectRepoResolver{}

func (r projectRepoResolver) RepoPath(projectID domain.ProjectID) (string, error) {
	rec, ok, err := r.store.GetProject(context.Background(), string(projectID))
	if err != nil {
		return "", fmt.Errorf("look up project %q: %w", projectID, err)
	}
	if !ok {
		return "", fmt.Errorf("no project registered with id %q — add one with `ao project add`: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	if !rec.ArchivedAt.IsZero() {
		return "", fmt.Errorf("project %q is archived: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	if rec.Path == "" {
		return "", fmt.Errorf("project %q has no repo path on record: %w", projectID, sessionmanager.ErrProjectNotResolvable)
	}
	return rec.Path, nil
}

type chatLauncher struct {
	svc                         *chatsvc.Service
	persistentHostReconcileDone <-chan struct{}
}

func (c chatLauncher) waitForPersistentHostReconcile(ctx context.Context) error {
	if c.persistentHostReconcileDone == nil {
		return nil
	}
	select {
	case <-c.persistentHostReconcileDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ interface {
	HibernateChat(context.Context, domain.SessionID) (bool, error)
} = chatLauncher{}

var _ sessionmanager.ChatLauncher = chatLauncher{}

var _ sessionmanager.ChatVersionRefresher = chatLauncher{}
var _ sessionmanager.ChatUpdateHibernator = chatLauncher{}
var _ interface {
	RunBackgroundTask(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
	RunPromptSuggestionTask(context.Context, domain.SessionID, string, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
	ArmChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	PrepareChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	AbortChatHandoff(domain.SessionID)
} = chatLauncher{}

func (c chatLauncher) SupportsChat(harness domain.AgentHarness) bool {
	return c.svc.SupportsChat(harness)
}

func (c chatLauncher) PermissionModes(ctx context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error) {
	return c.svc.PermissionModes(ctx, harness)
}

func (c chatLauncher) PreflightChat(
	ctx context.Context,
	harness domain.AgentHarness,
	permissions ports.PermissionMode,
) error {
	return c.svc.PreflightChat(ctx, harness, permissions)
}

func (c chatLauncher) PreflightChatWithEnv(
	ctx context.Context,
	harness domain.AgentHarness,
	permissions ports.PermissionMode,
	env map[string]string,
) error {
	return c.svc.PreflightChatWithEnv(ctx, harness, permissions, env)
}

func (c chatLauncher) SupportsReviewChat(harness domain.AgentHarness) bool {
	return c.svc.SupportsChat(harness)
}

func (c chatLauncher) PreflightReviewChat(ctx context.Context, harness domain.AgentHarness) error {
	return c.svc.PreflightChat(ctx, harness, ports.PermissionModeAuto)
}

func (c chatLauncher) StartReviewChat(ctx context.Context, cfg reviewcore.ReviewerChatStart) (string, error) {
	return c.startReviewChat(ctx, cfg, true)
}

func (c chatLauncher) RestoreReviewChat(ctx context.Context, cfg reviewcore.ReviewerChatStart) (string, error) {
	return c.startReviewChat(ctx, cfg, false)
}

func (c chatLauncher) startReviewChat(ctx context.Context, cfg reviewcore.ReviewerChatStart, sendPrompt bool) (string, error) {
	if err := c.waitForPersistentHostReconcile(ctx); err != nil {
		return "", err
	}
	owner := domain.ReviewConversationOwner(cfg.ReviewID)
	started, err := c.svc.StartChat(ctx, chatsvc.StartConfig{Owner: owner, SessionID: cfg.WorkerID, ProjectID: cfg.ProjectID, Kind: domain.KindWorker, Harness: cfg.Harness, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, Model: cfg.Model, Effort: cfg.Effort, Permissions: ports.PermissionModeAuto, SystemPrompt: cfg.SystemPrompt, ProviderConversationID: cfg.ProviderConversationID})
	if err != nil {
		return "", err
	}
	if !sendPrompt || strings.TrimSpace(cfg.Prompt) == "" {
		return started.ProviderConversationID, nil
	}
	if _, err := c.svc.SendForOwner(ctx, owner, ports.ChatUserMessage{Text: cfg.Prompt, Origin: domain.MessageOriginDaemon, ClientMessageID: reviewcore.BatchMessageID(cfg.BatchID)}); err != nil {
		_ = c.svc.StopForOwner(context.Background(), owner)
		return "", err
	}
	return started.ProviderConversationID, nil
}

func (c chatLauncher) SendReviewChat(ctx context.Context, reviewID, message, batchID string) error {
	_, err := c.svc.SendForOwner(ctx, domain.ReviewConversationOwner(reviewID), ports.ChatUserMessage{Text: message, Origin: domain.MessageOriginDaemon, ClientMessageID: reviewcore.BatchMessageID(batchID)})
	return err
}

func (c chatLauncher) ReviewChatAlive(reviewID string) bool {
	return c.svc.HasLiveControllerForOwner(domain.ReviewConversationOwner(reviewID))
}

func (c chatLauncher) InterruptReviewChat(ctx context.Context, reviewID string) error {
	err := c.svc.InterruptForOwner(ctx, domain.ReviewConversationOwner(reviewID))
	if errors.Is(err, chatsvc.ErrNoActiveTurn) || errors.Is(err, ports.ErrChatNoActiveTurn) {
		return nil
	}
	return err
}

func (c chatLauncher) StopReviewChat(ctx context.Context, reviewID string) error {
	return c.svc.StopForOwner(ctx, domain.ReviewConversationOwner(reviewID))
}

func (c chatLauncher) StartChat(ctx context.Context, cfg sessionmanager.ChatStart) (sessionmanager.ChatStarted, error) {
	if err := c.waitForPersistentHostReconcile(ctx); err != nil {
		return sessionmanager.ChatStarted{}, err
	}
	return c.svc.StartChat(ctx, cfg)
}

func (c chatLauncher) RunBackgroundTask(ctx context.Context, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (string, error) {
	return c.svc.RunBackgroundTask(ctx, harness, cfg, prompt)
}

func (c chatLauncher) RunPromptSuggestionTask(ctx context.Context, id domain.SessionID, turnID string, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (string, error) {
	return c.svc.RunPromptSuggestionTask(ctx, id, turnID, harness, cfg, prompt)
}

func (c chatLauncher) StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return c.svc.StartChatTurn(ctx, id, text)
}

func (c chatLauncher) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	return c.svc.RelaySessionChatTurn(ctx, id, text, clientMessageID, options)
}

func (c chatLauncher) QueueChatPrompt(ctx context.Context, id domain.SessionID, text string) (string, error) {
	turn, err := c.svc.QueueUserMessage(ctx, id, ports.ChatUserMessage{
		Text:               text,
		SpawnOpeningPrompt: true,
		Origin:             domain.MessageOriginHuman,
	})
	if err != nil {
		return "", err
	}
	return turn.ID, nil
}

func (c chatLauncher) DrainChatQueue(ctx context.Context, id domain.SessionID) error {
	return c.svc.DrainQueued(ctx, id)
}

func (c chatLauncher) HasLiveChatController(id domain.SessionID) bool {
	return c.svc.HasLiveChatController(id)
}

func (c chatLauncher) HibernateChat(ctx context.Context, id domain.SessionID) (bool, error) {
	return c.svc.HibernateChat(ctx, id)
}

func (c chatLauncher) SwitchToInstalledRelease(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error) {
	return c.svc.SwitchToInstalledRelease(ctx, id, minIdle)
}

func (c chatLauncher) HibernateChatForUpdate(ctx context.Context, id domain.SessionID) (bool, error) {
	return c.svc.HibernateChatForUpdate(ctx, id)
}

func (c chatLauncher) WaitForWake(ctx context.Context, id domain.SessionID) error {
	return c.svc.WaitForWake(ctx, id)
}

var (
	_ sessionmanager.ChatWakeWaiter       = chatLauncher{}
	_ sessionmanager.ChatVersionRefresher = chatLauncher{}
	_ sessionmanager.ChatUpdateHibernator = chatLauncher{}
)

func (c chatLauncher) ArmChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	return c.svc.ArmChatHandoff(ctx, id, policy)
}

func (c chatLauncher) MarkAccountSwitch(ctx context.Context, id domain.SessionID, accountID string) error {
	return c.svc.MarkAccountSwitch(ctx, id, accountID)
}

func (c chatLauncher) PrepareChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	return c.svc.PrepareChatHandoff(ctx, id, policy)
}

func (c chatLauncher) AbortChatHandoff(id domain.SessionID) {
	c.svc.AbortChatHandoff(id)
}

func (c chatLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	return c.svc.StopChat(ctx, id)
}

type reviewPRRefresher struct {
	sessions interface {
		ClaimPR(ctx context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error)
		FindBranchPRs(ctx context.Context, id domain.SessionID) (sessionsvc.BranchPRs, error)
	}
}

func (r reviewPRRefresher) FindBranchPRs(ctx context.Context, workerID domain.SessionID) (reviewsvc.BranchPRs, error) {
	found, err := r.sessions.FindBranchPRs(ctx, workerID)
	return reviewsvc.BranchPRs{Own: found.Own, OtherAuthor: found.OtherAuthor}, err
}

func (r reviewPRRefresher) RefreshPR(ctx context.Context, workerID domain.SessionID, prURL string) error {
	_, err := r.sessions.ClaimPR(ctx, workerID, prURL, sessionsvc.ClaimPROptions{AllowTakeover: false})
	var owned ports.PRClaimedByActiveSessionError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &owned):
		return fmt.Errorf("%w: %s belongs to active session %s", reviewcore.ErrPROwnedElsewhere, prURL, owned.Owner)
	case errors.Is(err, sessionsvc.ErrPRNotFound):
		return fmt.Errorf("%w: pull request %s was not found on the provider", reviewcore.ErrNotFound, prURL)
	case errors.Is(err, sessionsvc.ErrPRNotOpen):
		return fmt.Errorf("%w: pull request %s is not open", reviewcore.ErrInvalid, prURL)
	case errors.Is(err, sessionsvc.ErrInvalidPRRef), errors.Is(err, sessionsvc.ErrProjectMismatch):
		return fmt.Errorf("%w: %s is not a pull request in this project's repository", reviewcore.ErrInvalid, prURL)
	case errors.Is(err, sessionsvc.ErrSCMUnavailable):
		return fmt.Errorf("%w: Orchestrator could not fetch %s from the provider; try again shortly", reviewcore.ErrInvalid, prURL)
	default:
		return err
	}
}
