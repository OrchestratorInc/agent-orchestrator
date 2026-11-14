package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
)

var ErrNoController = errors.New("no live chat controller for session")

var ErrNotChatMode = errors.New("session is not in chat mode")

var (
	ErrExcerptInvalid = errors.New("chat excerpt is invalid")
	ErrExcerptStale   = errors.New("chat excerpt is stale")
)

const (
	maxExcerptReferences      = 8
	maxExcerptTextBytes       = 24 * 1024
	maxExcerptCommentRunes    = 4 * 1024
	maxExcerptPairedTextBytes = 16 * 1024
)

type SessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

type Service struct {
	findMu                 sync.Mutex
	findCache              map[string]cachedFind
	store                  Store
	reader                 SnapshotReader
	pageReader             SnapshotPageReader
	sessions               SessionReader
	drivers                ports.ChatDriverRegistry
	activity               ActivityRecorder
	log                    *slog.Logger
	newID                  IDFactory
	now                    Clock
	onAccountChanged       func(domain.SessionID, string, domain.AgentHarness)
	recheckAccount         func(domain.SessionID, domain.AgentHarness)
	prChanged              func(domain.SessionID)
	onCodexCapacityChanged func(domain.SessionID, string, ports.CodexCapacityObservation)
	onModelChanged         func(domain.SessionID, string)
	stopProviderHost       func(context.Context, domain.SessionID) error
	reports                *reportsvc.Coordinator
	renders                RenderFiles
	dataDir                string
	reconcileOutput        func(context.Context, domain.SessionID) error
	renderCheck            RenderCheck
	renderMeasure          RenderMeasure
	renderMeasures         sync.WaitGroup
	sides                  *sideManager
	wakeChat               func(context.Context, domain.SessionID) error
	hibernationEnabled     func() bool
	versionRefresh         func() (time.Duration, bool)
	versionSwitchMu        sync.Mutex
	versionSwitches        map[domain.SessionID]string
	versionSwitchObserver  func(id domain.SessionID, version, result string)
	viewMu                 sync.Mutex
	viewLeases             map[domain.SessionID]map[string]time.Time
	viewClosedAt           map[domain.SessionID]time.Time
	wakeMu                 sync.Mutex
	waking                 map[domain.SessionID]int
	wakeRuns               map[domain.SessionID]*wakeRun
	backgroundWakes        map[domain.SessionID]bool
	autoResumeMu           sync.Mutex
	autoResumeRetryAt      map[string]time.Time
	autoResumeInFlight     map[string]bool
	autoResumeScanning     bool
	usageLimitAccounts     usageLimitAccountReader
	handoffMu              sync.Mutex
	handoff                accountHandoffDeps
	handoffSince           time.Time
	handoffScanning        bool
	handoffs               map[string]*accountHandoff
	updateStops            map[domain.SessionID]bool

	runPromptSuggestion   func(context.Context, domain.SessionID, string, string, string) (string, error)
	promptSuggestionSlots chan struct{}

	mu                 sync.RWMutex
	controllers        map[domain.SessionID]*Controller
	ownerControllers   map[domain.ConversationOwner]*Controller
	startConfigs       map[domain.ConversationOwner]StartConfig
	awakeInterrupted   map[domain.ConversationOwner]domain.AwakeAgentTarget
	handoffInterrupted map[domain.SessionID]string
	gateMu             sync.Mutex
	gates              map[domain.ConversationOwner]controllerGate
	probeMu            sync.Mutex
	probed             map[domain.AgentHarness]ports.ChatCapabilities
	prewarming         sync.Map
	providerSwitches   sync.Map
	terminalSwitcher   TerminalProviderSwitcher
}

func (s *Service) SetReportCoordinator(coordinator *reportsvc.Coordinator) {
	s.reports = coordinator
}

type controllerGate chan struct{}

func (g controllerGate) lock(ctx context.Context) error {
	select {
	case g <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g controllerGate) tryLock() bool {
	select {
	case g <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g controllerGate) unlock() { <-g }

type Options struct {
	AppRunID               string
	Store                  Store
	Reader                 SnapshotReader
	PageReader             SnapshotPageReader
	Sessions               SessionReader
	Drivers                ports.ChatDriverRegistry
	Activity               ActivityRecorder
	Log                    *slog.Logger
	NewID                  IDFactory
	Now                    Clock
	OnAccountChanged       func(domain.SessionID, string, domain.AgentHarness)
	RecheckAccount         func(domain.SessionID, domain.AgentHarness)
	OnCodexCapacityChanged func(domain.SessionID, string, ports.CodexCapacityObservation)
	OnModelChanged         func(domain.SessionID, string)
	StopProviderHost       func(context.Context, domain.SessionID) error
	Renders                RenderFiles
	DataDir                string
	PRChanged              func(domain.SessionID)
	ReconcileOutputType    func(context.Context, domain.SessionID) error
	HibernationEnabled     func() bool
	VersionRefresh         func() (time.Duration, bool)
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	svc := &Service{
		store:                  opts.Store,
		reader:                 opts.Reader,
		pageReader:             opts.PageReader,
		sessions:               opts.Sessions,
		drivers:                opts.Drivers,
		activity:               opts.Activity,
		log:                    log,
		newID:                  opts.NewID,
		now:                    now,
		onAccountChanged:       opts.OnAccountChanged,
		recheckAccount:         opts.RecheckAccount,
		prChanged:              opts.PRChanged,
		onCodexCapacityChanged: opts.OnCodexCapacityChanged,
		onModelChanged:         opts.OnModelChanged,
		stopProviderHost:       opts.StopProviderHost,
		renders:                opts.Renders,
		dataDir:                opts.DataDir,
		reconcileOutput:        opts.ReconcileOutputType,
		hibernationEnabled:     opts.HibernationEnabled,
		versionRefresh:         opts.VersionRefresh,
		controllers:            make(map[domain.SessionID]*Controller),
		handoffInterrupted:     make(map[domain.SessionID]string),
		ownerControllers:       make(map[domain.ConversationOwner]*Controller),
		startConfigs:           make(map[domain.ConversationOwner]StartConfig),
		awakeInterrupted:       make(map[domain.ConversationOwner]domain.AwakeAgentTarget),
		gates:                  make(map[domain.ConversationOwner]controllerGate),
		probed:                 make(map[domain.AgentHarness]ports.ChatCapabilities),
		waking:                 make(map[domain.SessionID]int),
		wakeRuns:               make(map[domain.SessionID]*wakeRun),
		backgroundWakes:        make(map[domain.SessionID]bool),
		promptSuggestionSlots:  make(chan struct{}, 2),
	}
	svc.sides = newSideManager(svc, newMemorySideStore(), opts.AppRunID)
	return svc
}

func (s *Service) controllerGate(owner domain.ConversationOwner) controllerGate {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	gate := s.gates[owner]
	if gate == nil {
		gate = make(controllerGate, 1)
		s.gates[owner] = gate
	}
	return gate
}

func conversationOwner(cfg StartConfig) domain.ConversationOwner {
	if cfg.Owner.Kind != "" && cfg.Owner.ID != "" {
		return cfg.Owner
	}
	return domain.SessionConversationOwner(cfg.SessionID)
}

func providerHostID(cfg StartConfig) domain.SessionID {
	if owner := conversationOwner(cfg); owner.Kind == domain.ConversationOwnerReview {
		return domain.SessionID("review-" + owner.ID)
	}
	return cfg.SessionID
}

type StartConfig = ports.ChatControllerStart

type liveControllerClaimer interface {
	ClaimLiveChatControllerGeneration(ctx context.Context, session domain.SessionID, generation string) error
}

type reviewerConversationStore interface {
	ConversationForReview(context.Context, string) (domain.ConversationRecord, error)
	GetReviewByID(context.Context, string) (domain.Review, bool, error)
}

func (s *Service) claimChatController(ctx context.Context, id domain.SessionID, generation string, liveReconnect bool) error {
	if claimer, ok := s.store.(liveControllerClaimer); ok && liveReconnect {
		return claimer.ClaimLiveChatControllerGeneration(ctx, id, generation)
	}
	return s.store.ClaimChatControllerGeneration(ctx, id, generation)
}

type ControllerCommit = ports.ChatControllerCommit

func conversationReconnectedLive(conv ports.ChatConversation) bool {
	reconnected, ok := conv.(ports.ChatLiveReconnector)
	return ok && reconnected.ReconnectedLive()
}

func controllerStartResult(
	controller *Controller,
	providerBoundary *domain.ConversationBranch,
	commitProviderHistory func(context.Context) error,
) StartResult {
	return StartResult{
		LiveReconnect:          conversationReconnectedLive(controller.conv),
		ProviderConversationID: controller.ProviderConversationID(),
		ControllerGeneration:   controller.Generation(),
		Conversation:           controller.conversation,
		ProviderBoundary:       providerBoundary,
		CommitProviderHistory:  commitProviderHistory,
	}
}

func notifyControllerReady(
	cfg StartConfig,
	controller *Controller,
	providerBoundary *domain.ConversationBranch,
	commitProviderHistory func(context.Context) error,
) (ControllerCommit, error) {
	if cfg.ControllerReady == nil {
		return ControllerCommit{}, nil
	}
	commit, err := cfg.ControllerReady(controllerStartResult(
		controller, providerBoundary, commitProviderHistory,
	))
	if err != nil {
		return ControllerCommit{}, fmt.Errorf("commit chat controller: %w", err)
	}
	return commit, nil
}

const claudeAutoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

func cloneStartConfig(cfg StartConfig) StartConfig {
	cloned := cfg
	if cfg.AwakeReconnectTarget != nil {
		target := *cfg.AwakeReconnectTarget
		cloned.AwakeReconnectTarget = &target
	}
	cloned.Env = make(map[string]string, len(cfg.Env))
	for key, value := range cfg.Env {
		cloned.Env[key] = value
	}
	cloned.AdditionalDirectories = append([]string(nil), cfg.AdditionalDirectories...)
	cloned.MCPServers = make([]ports.ChatMCPServerConfig, len(cfg.MCPServers))
	for index, server := range cfg.MCPServers {
		server.Args = append([]string(nil), server.Args...)
		server.Env = make(map[string]string, len(cfg.MCPServers[index].Env))
		for key, value := range cfg.MCPServers[index].Env {
			server.Env[key] = value
		}
		server.Headers = make(map[string]string, len(cfg.MCPServers[index].Headers))
		for key, value := range cfg.MCPServers[index].Headers {
			server.Headers[key] = value
		}
		cloned.MCPServers[index] = server
	}
	return cloned
}

func (s *Service) settleOrphanedWork(ctx context.Context, session domain.SessionID, conversationID string) {
	now := s.now()
	if err := s.store.SettleOrphanedTurns(ctx, session, now); err != nil {
		s.log.Error("chat start: settle orphaned turns", "session", session, "error", err)
	}
	if err := s.store.FailPendingApprovals(ctx, conversationID, now); err != nil {
		s.log.Error("chat start: close pending approvals", "session", session, "error", err)
	}
	if err := s.store.FailPendingInputs(ctx, conversationID, now); err != nil {
		s.log.Error("chat start: close pending input requests", "session", session, "error", err)
	}
}

func (s *Service) Start(ctx context.Context, cfg StartConfig) (*Controller, error) {
	owner := conversationOwner(cfg)
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return nil, err
	}
	defer gate.unlock()
	return s.startLocked(ctx, cfg)
}

func (s *Service) startLocked(ctx context.Context, cfg StartConfig) (*Controller, error) {
	owner := conversationOwner(cfg)
	if owner.Kind == domain.ConversationOwnerReview {
		cfg.ReadOnly = true
	}
	if target := cfg.AwakeReconnectTarget; target != nil {
		s.mu.Lock()
		candidate, exists := s.awakeInterrupted[owner]
		old, hasConfig := s.startConfigs[owner]
		valid := cfg.ReconnectOnly && !cfg.FreshIfProviderConversationMissing && owner.Kind == domain.ConversationOwnerSession && target.SessionID == cfg.SessionID && target.Owner == cfg.ExpectedControllerOwner && exists && candidate == *target && hasConfig && old.ExpectedControllerOwner == target.Owner && s.ownerControllers[owner] == nil
		if valid {
			delete(s.awakeInterrupted, owner)
		}
		s.mu.Unlock()
		if !valid {
			return nil, fmt.Errorf("%w: interrupted attachment proof expired or was explicitly stopped", ports.ErrChatRecoveryInconclusive)
		}
	}
	if cfg.HistoryMode > ports.ChatHistoryDeferred {
		return nil, errors.New("invalid Chat history mode")
	}
	if handoff := cfg.ProviderHandoff; handoff != nil {
		if handoff.BoundaryID == "" || cfg.ProviderConversationID == "" || cfg.ControllerReady == nil ||
			(cfg.HistoryMode == ports.ChatHistoryDeferred) || (cfg.ProviderScopeID != "" && cfg.ProviderScopeID != handoff.BoundaryID) {
			return nil, errors.New("incomplete native Chat handoff reservation")
		}
		cfg.ProviderScopeID = handoff.BoundaryID
		cfg.HistoryMode = ports.ChatHistoryRequired
	}

	replayCheckpoint := nativeHistoryCheckpoint{harness: cfg.Harness}
	nativeEvidence := ""
	importingHistory := false
	if cfg.HistoryMode == ports.ChatHistoryRequired {
		if s.sessions == nil {
			return nil, errors.New("native history replay requires a session reader")
		}
		rec, found, err := s.sessions.GetSession(ctx, cfg.SessionID)
		if err != nil {
			return nil, fmt.Errorf("read native history checkpoint: %w", err)
		}
		if !found {
			return nil, ports.ErrSessionNotFound
		}
		importingHistory = rec.NeedsImportResume()
		nativeEvidence = rec.Metadata.NativeCheckpointEvidence
		replayCheckpoint.latestUserPromptAt = rec.Metadata.LatestUserPromptAt
		replayCheckpoint.latestAssistantUpdateAt = rec.Metadata.LatestAssistantUpdateAt
		checkpointState := rec.Metadata.ConversationCheckpointState
		if checkpointState == "" {
			checkpointState = domain.ConversationCheckpointLegacy
		}
		trustedProvenance := checkpointState.Trusted() &&
			rec.Metadata.ConversationCheckpointGeneration != "" &&
			rec.Metadata.ConversationCheckpointNativeID != ""
		trusted := trustedProvenance &&
			rec.Metadata.ConversationCheckpointNativeID == cfg.ProviderConversationID
		if rec.Harness == domain.HarnessClaudeCode && trustedProvenance && nativeEvidence == "" {
			replayCheckpoint.hardMismatches = append(replayCheckpoint.hardMismatches, ports.ChatHistoryMismatchUnsettledBoundary)
		}
		if rec.Metadata.ConversationCheckpointUnsettled ||
			(checkpointState == domain.ConversationCheckpointComplete &&
				strings.TrimSpace(rec.Metadata.LatestUserPrompt) == "" &&
				strings.TrimSpace(rec.Metadata.LatestAssistantUpdate) != "") {
			replayCheckpoint.hardMismatches = append(replayCheckpoint.hardMismatches,
				ports.ChatHistoryMismatchUnsettledBoundary)
		}
		switch {
		case trustedProvenance && !trusted:
			replayCheckpoint.hardMismatches = append(replayCheckpoint.hardMismatches,
				ports.ChatHistoryMismatchNativeIdentity)
		case trusted:
			replayCheckpoint.providerTurnID = rec.Metadata.ConversationCheckpointTurnID
			replayCheckpoint.latestUserPrompt = strings.TrimSpace(rec.Metadata.LatestUserPrompt)
			replayCheckpoint.userMismatch = ports.ChatHistoryMismatchTrustedUserText
			if checkpointState == domain.ConversationCheckpointComplete {
				replayCheckpoint.completedUserPrompt = true
				replayCheckpoint.latestAssistantUpdate = strings.TrimSpace(rec.Metadata.LatestAssistantUpdate)
				replayCheckpoint.assistantMismatch = ports.ChatHistoryMismatchTrustedAssistantText
			}
		case cfg.HistoryPolicy != domain.SessionInterfaceTransitionHistoryProvider:
			replayCheckpoint.latestUserPrompt = strings.TrimSpace(rec.Metadata.LatestUserPrompt)
			replayCheckpoint.latestAssistantUpdate = strings.TrimSpace(rec.Metadata.LatestAssistantUpdate)
			replayCheckpoint.userMismatch = ports.ChatHistoryMismatchUntrustedUserText
			replayCheckpoint.assistantMismatch = ports.ChatHistoryMismatchUntrustedAssistantText
		}
	}

	s.mu.RLock()
	existing := s.ownerControllers[owner]
	s.mu.RUnlock()
	if existing != nil && cfg.ProviderSwitch == nil {
		if existing.State() != ports.ChatControllerStopped {
			return existing, nil
		}
		select {
		case <-existing.stopped:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
		if current := s.ownerControllers[owner]; current == existing {
			delete(s.ownerControllers, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, cfg.SessionID)
			}
		}
		s.mu.Unlock()
	}

	driver, err := s.drivers.Driver(cfg.Harness)
	if err != nil {
		return nil, fmt.Errorf("chat driver for %s: %w", cfg.Harness, err)
	}
	resume := driver.Resume
	if cfg.ReconnectOnly {
		reconnector, ok := driver.(ports.ChatDriverReconnector)
		if !ok || cfg.ProviderConversationID == "" {
			return nil, ports.ErrChatHostNotRunning
		}
		resume = reconnector.Reconnect
	}
	if nativeEvidence != "" {
		for _, mismatch := range replayCheckpoint.hardMismatches {
			if mismatch == ports.ChatHistoryMismatchNativeIdentity {
				return nil, &ports.ChatHistoryUnsettledError{Dimensions: replayCheckpoint.hardMismatches}
			}
		}
		verifier, ok := driver.(ports.NativeCheckpointVerifier)
		if !ok {
			return nil, &ports.ChatHistoryUnsettledError{Dimensions: []ports.ChatHistoryMismatchDimension{ports.ChatHistoryMismatchUnsettledBoundary}}
		}
		boundary, verifyErr := verifyNativeCheckpoint(ctx, verifier, ports.NativeCheckpointRequest{
			ProviderConversationID: cfg.ProviderConversationID, Env: cfg.Env, Evidence: nativeEvidence,
		})
		if verifyErr != nil {
			return nil, verifyErr
		}
		verified := nativeHistoryCheckpoint{nativeBoundary: &boundary}
		if replayCheckpoint.userMismatch == ports.ChatHistoryMismatchUntrustedUserText {
			verified.latestUserPrompt = replayCheckpoint.latestUserPrompt
			verified.latestAssistantUpdate = replayCheckpoint.latestAssistantUpdate
			verified.userMismatch = replayCheckpoint.userMismatch
			verified.assistantMismatch = replayCheckpoint.assistantMismatch
		}
		verified.harness = cfg.Harness
		replayCheckpoint = verified
	}

	var caps ports.ChatCapabilities
	if cfg.ProviderConversationID == "" {
		caps, err = s.driverCapabilities(ctx, cfg.Harness, driver)
		if err != nil {
			return nil, err
		}
		if err := chatAdmissionError(driver, cfg.Harness, caps, cfg.Permissions); err != nil {
			return nil, err
		}
	}

	var sessionRecord domain.SessionRecord
	sessionFound := false
	if s.sessions != nil {
		var readErr error
		sessionRecord, sessionFound, readErr = s.sessions.GetSession(ctx, cfg.SessionID)
		if readErr != nil {
			return nil, fmt.Errorf("read chat session before start: %w", readErr)
		}
	}
	firstController := sessionFound && sessionRecord.ProvisionState.IsProvisioning() &&
		sessionRecord.Metadata.ControllerGeneration == "" && sessionRecord.Metadata.ProviderConversationID == ""

	scope := domain.ConversationScopeSession
	if cfg.Kind == domain.KindOrchestrator {
		scope = domain.ConversationScopeProject
	}
	now := s.now()
	var resetBoundary domain.ConversationActivity
	freshProjectContext := scope == domain.ConversationScopeProject &&
		cfg.ProviderConversationID == "" && cfg.ProviderSwitch == nil
	if freshProjectContext {
		detail, marshalErr := json.Marshal(map[string]string{
			"event":  "context.reset",
			"reason": "native conversation was unavailable",
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("encode fresh context boundary: %w", marshalErr)
		}
		resetBoundary = domain.ConversationActivity{
			ID:             s.newID(),
			Kind:           domain.ActivityKindSystem,
			Status:         domain.ActivityStatusCompleted,
			Summary:        "Agent context reset.",
			Detail:         detail,
			ProviderItemID: domain.ConversationContextResetProviderItemID(cfg.SessionID),
		}
	}
	conversationID := s.newID()
	var conversation domain.ConversationRecord
	if owner.Kind == domain.ConversationOwnerReview {
		if cfg.ProviderHandoff != nil || owner.ID == "" {
			return nil, errors.New("reviewer chat does not support provider handoff")
		}
		conversation, err = s.store.CreateReviewConversation(ctx, conversationID, owner.ID, cfg.ProjectID, cfg.SessionID, now)
	} else if cfg.ProviderHandoff != nil {
		handoff := cfg.ProviderHandoff
		conversation, err = s.store.ConversationForSession(ctx, handoff.PreviousSessionID)
		if err == nil && (conversation.ID != handoff.ConversationID ||
			conversation.ActiveBranchID != handoff.PreviousBranchID || conversation.LatestSequence != handoff.PreviousSequence) {
			err = errors.New("native Chat handoff conversation changed")
		}
	} else if providerSwitch := cfg.ProviderSwitch; providerSwitch != nil {
		conversation, err = s.store.ConversationForSession(ctx, cfg.SessionID)
		if err == nil && (conversation.ID != providerSwitch.ConversationID ||
			conversation.ActiveBranchID != providerSwitch.SourceBranchID ||
			conversation.LatestSequence != providerSwitch.SourceSequence) {
			err = errors.New("provider switch conversation changed")
		}
	} else if freshProjectContext && firstController {
		conversation, err = s.store.CreateProjectConversationAdoptingQueue(
			ctx, conversationID, cfg.ProjectID, cfg.SessionID, resetBoundary, now)
	} else if freshProjectContext {
		conversation, err = s.store.CreateProjectConversationWithContextReset(
			ctx, conversationID, cfg.ProjectID, cfg.SessionID, resetBoundary, now)
	} else if scope == domain.ConversationScopeProject &&
		cfg.ProviderConversationID != "" && cfg.ProviderScopeID != "" {
		conversation, err = s.store.ConversationForSession(ctx, cfg.SessionID)
	} else if cfg.ProviderConversationID != "" {
		conversation, err = s.store.OpenNativeConversation(
			ctx, conversationID, scope, cfg.ProjectID, cfg.SessionID, now)
	} else {
		conversation, err = s.store.CreateConversation(
			ctx, conversationID, scope, cfg.ProjectID, cfg.SessionID, now)
	}
	if err != nil {
		return nil, fmt.Errorf("open conversation: %w", err)
	}
	var repairedBranch domain.ConversationBranch
	var restoredProviderOwner bool
	if cfg.ProviderHandoff == nil && cfg.ProviderSwitch == nil && owner.Kind != domain.ConversationOwnerReview {
		repairedBranch, restoredProviderOwner, err = s.store.RepairIncompleteConversationEdit(
			ctx, cfg.SessionID, conversation.ID, s.now())
		if err != nil {
			return nil, fmt.Errorf("repair incomplete conversation edit: %w", err)
		}
	}
	if repairedBranch.ID != "" {
		conversation.ActiveBranchID = repairedBranch.ID
		if restoredProviderOwner && cfg.ProviderScopeID == "" {
			cfg.ProviderConversationID = repairedBranch.ProviderConversationID
		}
	}
	activeBranch, err := s.store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		return nil, fmt.Errorf("load active conversation branch: %w", err)
	}
	if note := strings.TrimSpace(cfg.ForkHandoffNote); note != "" && cfg.ProviderSwitch == nil && activeBranch.HandoffNote == "" {
		keeper, ok := s.store.(forkHandoffNoteKeeper)
		if !ok {
			return nil, errors.New("chat start: store can't keep a fork's copied conversation")
		}
		if err := keeper.SetConversationBranchHandoffNote(ctx, conversation.ID, activeBranch.ID, note); err != nil {
			return nil, fmt.Errorf("keep fork's copied conversation: %w", err)
		}
		activeBranch.HandoffNote = note
	}
	providerScopeID := activeBranch.ProviderScopeID
	providerHandleOwnedByActiveBranch := true
	if cfg.ProviderScopeID != "" {
		providerScopeID = cfg.ProviderScopeID
		providerHandleOwnedByActiveBranch = providerScopeID == activeBranch.ProviderScopeID
	}
	queuedBeforeFirstController := false
	preserveUndispatchedQueue := false
	waking := s.isWaking(cfg.SessionID)
	if sessionFound && sessionRecord.StaleBinary != nil && sessionRecord.StaleBinary.SwitchFailedAt != nil {
		waking = true
	}
	if sessionFound && s.accountMoveAwaitingTarget(ctx, cfg.SessionID) {
		waking = true
	}
	hadProviderHistory := false
	if sessionFound && (sessionRecord.ProvisionState.IsProvisioning() || waking) {
		hadProviderHistory = sessionRecord.Metadata.ProviderConversationID != ""
		running, listErr := s.store.ListVisibleRunningTurnProviderIDs(ctx, conversation.ID)
		if listErr != nil {
			return nil, fmt.Errorf("read running chat turns before start: %w", listErr)
		}
		queuedBeforeFirstController = firstController && len(running) == 0
		preserveUndispatchedQueue = len(running) == 0 && (sessionRecord.ProvisionState.IsProvisioning() || waking)
	}
	providerBoundaryID := ""
	if cfg.ProviderSwitch != nil {
		providerBoundaryID = s.newID()
		providerScopeID = providerBoundaryID
		providerHandleOwnedByActiveBranch = false
	} else if !providerHandleOwnedByActiveBranch {
		providerBoundaryID = providerScopeID
	} else if cfg.ProviderConversationID == "" && cfg.ProviderScopeID == "" &&
		(activeBranch.SessionID != cfg.SessionID || activeBranch.ProviderConversationID != "" || hadProviderHistory ||
			(!preserveUndispatchedQueue && conversation.LatestSequence > 0)) {
		providerBoundaryID = s.newID()
		providerScopeID = providerBoundaryID
		providerHandleOwnedByActiveBranch = false
	}
	if cfg.ProviderConversationID != "" && providerHandleOwnedByActiveBranch {
		if activeBranch.ProviderConversationID != "" &&
			activeBranch.ProviderConversationID != cfg.ProviderConversationID {
			return nil, fmt.Errorf("%w: active conversation branch provider handle %q does not match session handle %q",
				ports.ErrChatResumeFailed, activeBranch.ProviderConversationID, cfg.ProviderConversationID)
		}
	}
	if cfg.ProviderSwitch != nil && cfg.Harness == domain.HarnessClaudeCode {
		if previous := domain.AutoCompactTokens[conversation.Settings.AutoCompactWindow]; previous > 0 &&
			cfg.Env[claudeAutoCompactWindowEnv] == fmt.Sprint(previous) {
			cfg.Env = cloneStartConfig(cfg).Env
			delete(cfg.Env, claudeAutoCompactWindowEnv)
		}
		conversation.Settings.AutoCompactWindow = cfg.ProviderSwitch.AutoCompactWindow
	}
	if tokens := domain.AutoCompactTokens[conversation.Settings.AutoCompactWindow]; cfg.Harness == domain.HarnessClaudeCode && tokens > 0 {
		cfg.Env = cloneStartConfig(cfg).Env
		cfg.Env[claudeAutoCompactWindowEnv] = fmt.Sprint(tokens)
	}
	if cfg.ProviderConversationID != "" && cfg.ForkFirstStart && cfg.ProviderSwitch == nil {
		conversation.Settings.Model = cfg.Model
		conversation.Settings.ReasoningEffort = cfg.Effort
		conversation.Settings.ApprovalMode = cfg.Permissions
		if err := s.store.SetConversationSettings(ctx, conversation.ID, conversation.Settings, s.now()); err != nil {
			return nil, fmt.Errorf("record initial conversation settings: %w", err)
		}
	}
	if cfg.ProviderConversationID != "" && conversation.Settings.Model != "" {
		cfg.Model = conversation.Settings.Model
	}
	if cfg.ProviderConversationID != "" {
		cfg.Effort = conversation.Settings.ReasoningEffort
	}
	if cfg.ProviderConversationID != "" && conversation.Settings.ApprovalMode != "" {
		cfg.Permissions = conversation.Settings.ApprovalMode
	}
	if cfg.ProviderConversationID == "" {
		if err := chatAdmissionError(driver, cfg.Harness, caps, cfg.Permissions); err != nil {
			return nil, err
		}
		conversation.Settings.Model = cfg.Model
		conversation.Settings.ReasoningEffort = cfg.Effort
		conversation.Settings.ApprovalMode = cfg.Permissions
		if cfg.ProviderSwitch == nil {
			if err := s.store.SetConversationSettings(ctx, conversation.ID, conversation.Settings, s.now()); err != nil {
				return nil, fmt.Errorf("record initial conversation settings: %w", err)
			}
		}
	}

	var prepareEnv func(context.Context) (map[string]string, error)
	if cfg.PrepareControllerEnv != nil {
		prepareEnv = func(prepareCtx context.Context) (map[string]string, error) {
			env, prepareErr := cfg.PrepareControllerEnv(prepareCtx, cfg.ExpectedControllerOwner)
			if prepareErr != nil {
				return nil, fmt.Errorf("prepare chat controller environment: %w", prepareErr)
			}
			return env, nil
		}
	}

	handoffNote := activeBranch.HandoffNote
	if cfg.ProviderSwitch != nil {
		handoffNote = cfg.ProviderSwitch.Bootstrap
	}
	providerSystemPrompt := withHandoffNote(cfg.SystemPrompt, handoffNote)

	var conv ports.ChatConversation
	hostID := providerHostID(cfg)
	freshIfMissing := cfg.FreshIfProviderConversationMissing
	if cfg.ProviderConversationID != "" {
		unanswered := s.branchUnanswered(ctx, cfg.SessionID, conversation.ID, activeBranch)
		freshIfMissing = freshIfMissing || unanswered
		conv, err = resume(ctx, ports.ChatResumeConfig{
			SessionID:              hostID,
			ProviderConversationID: cfg.ProviderConversationID,
			FreshIfMissing:         freshIfMissing,
			HasPriorTurns:          conversation.LatestSequence > 0 && !unanswered,
			DataDir:                cfg.DataDir,
			WorkspacePath:          cfg.WorkspacePath,
			Env:                    cfg.Env,
			PrepareEnv:             prepareEnv,
			Model:                  cfg.Model,
			Effort:                 cfg.Effort,
			Permissions:            cfg.Permissions,
			ReadOnly:               cfg.ReadOnly,
			SystemPrompt:           providerSystemPrompt,
			ProviderScopeID:        providerScopeID,
			ProviderIDsScoped:      providerBoundaryID != "" || activeBranch.ProviderIDsScoped,
			AdditionalDirectories:  cfg.AdditionalDirectories,
			MCPServers:             cfg.MCPServers,
		})
	} else {
		conv, err = driver.Start(ctx, ports.ChatStartConfig{
			ProviderIDsScoped:     providerBoundaryID != "" || activeBranch.ProviderIDsScoped,
			SessionID:             hostID,
			DataDir:               cfg.DataDir,
			WorkspacePath:         cfg.WorkspacePath,
			Env:                   cfg.Env,
			PrepareEnv:            prepareEnv,
			Model:                 cfg.Model,
			Effort:                cfg.Effort,
			Permissions:           cfg.Permissions,
			ReadOnly:              cfg.ReadOnly,
			SystemPrompt:          providerSystemPrompt,
			ProviderScopeID:       providerScopeID,
			AdditionalDirectories: cfg.AdditionalDirectories,
			MCPServers:            cfg.MCPServers,
		})
	}
	if err != nil {
		return nil, err
	}
	liveReconnect := false
	if reconnected, ok := conv.(ports.ChatLiveReconnector); ok {
		liveReconnect = reconnected.ReconnectedLive()
	}
	freshReplacement := false
	if cfg.ProviderConversationID != "" &&
		conv.ProviderConversationID() != cfg.ProviderConversationID {
		returned := conv.ProviderConversationID()
		if !freshIfMissing || liveReconnect || returned == "" {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, fmt.Errorf(
				"resumed provider conversation handle %q does not match requested handle %q",
				returned, cfg.ProviderConversationID,
			)
		}
		freshReplacement = true
		if !cfg.FreshIfProviderConversationMissing {
			cfg.ControllerReady = s.rebindBranchOnReady(ctx, cfg.ControllerReady, conversation.ID, activeBranch.ID, cfg.ProviderConversationID)
		}
	}
	if (cfg.HistoryMode == ports.ChatHistoryRequired) && liveReconnect {
		cleanupErr := cleanupUnpublishedConversation(conv, false)
		return nil, errors.Join(fmt.Errorf("%w: native-history handoff cannot adopt an existing live provider; a fresh observation is required",
			ports.ErrChatRecoveryInconclusive), cleanupErr)
	}
	if cfg.ProviderConversationID != "" {
		caps = maps.Clone(conv.Capabilities())
		if liveReconnect {
			if caps == nil {
				caps = make(ports.ChatCapabilities)
			}
			caps[ports.ChatCapabilityResume] = true
		}
		if err := chatAdmissionError(driver, cfg.Harness, caps, cfg.Permissions); err != nil {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, err
		}
	}
	if freshReplacement {
		cfg.ProviderConversationID = ""
	}
	if !liveReconnect && isOpenCodeHarness(cfg.Harness) && conversation.Settings.OpenCodeMode != "" {
		if err := restoreOpenCodeMode(ctx, conv, conversation.Settings.OpenCodeMode); err != nil {
			_ = cleanupUnpublishedConversation(conv, cfg.ProviderConversationID == "")
			return nil, err
		}
	}
	if !liveReconnect && cfg.Harness == domain.HarnessCopilot && cfg.ProviderConversationID != "" {
		if err := sanitizeCopilotLoadedMode(ctx, conv); err != nil {
			_ = cleanupUnpublishedConversation(conv, cfg.ProviderConversationID == "")
			return nil, err
		}
	}
	var liveRows ConversationRows
	if liveReconnect {
		if s.reader == nil {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, fmt.Errorf("%w: durable conversation snapshot is unavailable", ports.ErrChatRecoveryInconclusive)
		}
		liveRows, err = s.reader.LoadConversationSnapshot(ctx, conversation.ID)
		if err != nil {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, fmt.Errorf("%w: load live conversation before reconnect: %w", ports.ErrChatRecoveryInconclusive, err)
		}
	}
	if owner.Kind == domain.ConversationOwnerReview && !liveReconnect {
		if reviewStore, ok := s.store.(reviewerConversationStore); ok {
			review, found, readErr := reviewStore.GetReviewByID(ctx, owner.ID)
			if readErr != nil {
				_ = cleanupUnpublishedConversation(conv, cfg.ProviderConversationID == "")
				return nil, fmt.Errorf("read reviewer Chat owner before start: %w", readErr)
			}
			if found {
				if _, cleanupErr := s.store.CleanupOwnedReviewControllerWork(ctx, owner.ID, conversation.ID, review.ControllerGeneration, s.now()); cleanupErr != nil {
					_ = cleanupUnpublishedConversation(conv, cfg.ProviderConversationID == "")
					return nil, fmt.Errorf("settle previous reviewer Chat work: %w", cleanupErr)
				}
			}
		}
	}

	if _, err := s.store.ReconcileConversationAuthentication(ctx, conversation.ID, "", "", s.now()); err != nil {
		_ = cleanupUnpublishedConversation(conv, false)
		return nil, fmt.Errorf("reconcile conversation authentication: %w", err)
	}

	generation := strings.TrimSpace(cfg.ControllerGeneration)
	if generation == "" {
		generation = s.newID()
	}
	if providerBoundaryID == "" {
		if owner.Kind == domain.ConversationOwnerReview {
			claimed, claimErr := s.store.ClaimReviewChatController(ctx, owner.ID, conv.ProviderConversationID(), generation, s.now())
			if claimErr != nil || !claimed {
				_ = cleanupUnpublishedConversation(conv, cfg.ProviderConversationID == "")
				if claimErr != nil {
					return nil, fmt.Errorf("claim reviewer chat controller: %w", claimErr)
				}
				return nil, errors.New("reviewer chat controller ownership changed")
			}
		} else if err := s.claimChatController(ctx, cfg.SessionID, generation, liveReconnect); err != nil {
			_ = cleanupUnpublishedConversation(conv, cfg.ProviderConversationID == "")
			return nil, fmt.Errorf("claim chat controller: %w", err)
		}
	}
	if liveReconnect {
		for _, turn := range liveRows.Turns {
			if turn.State != domain.TurnStateRunning || turn.ProviderTurnID != "" || turn.RolledBackAt != nil {
				continue
			}
			if err := s.store.SettleUnboundRunningTurn(ctx, conversation.ID, cfg.SessionID, turn.ID, s.now()); err != nil {
				s.log.Error("chat start: settle unbound running turn", "session", cfg.SessionID, "turn", turn.ID, "error", err)
			}
		}
	}
	providerBoundary := (*domain.ConversationBranch)(nil)
	if providerBoundaryID != "" {
		providerBoundary = &domain.ConversationBranch{
			ID: providerBoundaryID, ConversationID: conversation.ID, SessionID: cfg.SessionID,
			ProviderConversationID: conv.ProviderConversationID(), ParentBranchID: activeBranch.ID,
			ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: providerScopeID,
			ProviderIDsScoped: true,
			CreatedAt:         s.now(),
		}
		if cfg.ProviderSwitch != nil {
			providerBoundary.Strategy = domain.ConversationBranchStrategyApproximateContext
			providerBoundary.ReplayCutoffSequence = cfg.ProviderSwitch.SourceSequence
			providerBoundary.ReplayTruncated = true
			providerBoundary.HandoffNote = cfg.ProviderSwitch.Bootstrap
		}
	}
	if !liveReconnect && cfg.ProviderHandoff == nil &&
		owner.Kind != domain.ConversationOwnerReview &&
		!queuedBeforeFirstController && !preserveUndispatchedQueue {
		s.settleOrphanedWork(ctx, cfg.SessionID, conversation.ID)
	}
	controller := newController(
		cfg.SessionID, owner, conversation, generation, cfg.Harness, cfg.WorkspacePath, conv, s.store, s.activity, s.log, s.newID, s.now, s.onAccountChanged, s.recheckAccount, s.prChanged, s.onCodexCapacityChanged, s.reader)
	if owner.Kind != domain.ConversationOwnerReview {
		controller.artifactDir = s.sessionArtifactDir(ctx, cfg.SessionID)
	}
	var commitProviderHistory func(context.Context) error
	if liveReconnect {
		if err := s.store.FailReservedQueuedBatch(ctx, conversation.ID, s.now()); err != nil {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, fmt.Errorf("settle uncertain queued batch: %w", err)
		}
		providerTurnID := controller.restoreLiveTurnOwnership(liveRows.Turns)
		if activator, ok := conv.(ports.ChatLiveReconnectActivator); ok {
			if err := activator.ActivateLiveReconnect(ctx, providerTurnID); err != nil {
				_ = cleanupUnpublishedConversation(conv, false)
				return nil, err
			}
		}
	}
	if cfg.ProviderConversationID != "" && cfg.HistoryMode != ports.ChatHistoryDeferred && !liveReconnect {
		var existing ConversationRows
		if s.reader != nil {
			existing, err = s.reader.LoadConversationSnapshot(ctx, conversation.ID)
			if err != nil {
				_ = cleanupUnpublishedConversation(conv, false)
				return nil, fmt.Errorf("load conversation before native history import: %w", err)
			}
		}
		retained := existing
		if cfg.ProviderHandoff != nil {
			existing = ConversationRows{}
		} else {
			existing, err = s.nativeReplayRows(ctx, activeBranch, existing)
			if err != nil {
				_ = cleanupUnpublishedConversation(conv, false)
				return nil, err
			}
			if activeBranch.ReplacedTurnID != "" {
				if replayCheckpoint.userMismatch == ports.ChatHistoryMismatchUntrustedUserText &&
					!replayCheckpoint.latestUserPromptAt.IsZero() && replayCheckpoint.latestUserPromptAt.Before(activeBranch.CreatedAt) {
					replayCheckpoint.latestUserPrompt = ""
				}
				if replayCheckpoint.assistantMismatch == ports.ChatHistoryMismatchUntrustedAssistantText &&
					!replayCheckpoint.latestAssistantUpdateAt.IsZero() && replayCheckpoint.latestAssistantUpdateAt.Before(activeBranch.CreatedAt) {
					replayCheckpoint.latestAssistantUpdate = ""
				}
			}
		}
		if cfg.HistoryMode == ports.ChatHistoryRequired {
			replayCheckpoint.captureAOHighWater(
				cfg.SessionID, existing.Turns, existing.Messages, existing.Activities,
			)
		}
		events, historyErr := controller.readNativeHistory(
			ctx, existing.Turns, existing.Messages, existing.Activities,
			(cfg.HistoryMode == ports.ChatHistoryRequired), replayCheckpoint,
		)
		if historyErr == nil && importingHistory && !slices.ContainsFunc(events, func(event ports.ChatEvent) bool {
			return (event.Kind == ports.ChatEventUserMessageCompleted || event.Kind == ports.ChatEventMessageCompleted) &&
				strings.TrimSpace(event.Text) != ""
		}) {
			historyErr = fmt.Errorf("%w: the provider returned no readable messages for the imported conversation", ports.ErrChatHistoryUnavailable)
		}
		if historyErr != nil {
			if cleanupErr := cleanupUnpublishedConversation(conv, false); cleanupErr != nil && (cfg.HistoryMode == ports.ChatHistoryRequired) {
				return nil, fmt.Errorf("%w: failed history target shutdown: %w",
					ports.ErrChatRecoveryInconclusive, errors.Join(historyErr, cleanupErr))
			}
			return nil, historyErr
		}
		events, err = s.withoutInheritedHistory(ctx, conv, activeBranch, retained, events)
		if err != nil {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, err
		}
		events = reconcileNativeHistoryForHarness(cfg.Harness, events, existing.Turns, existing.Messages, existing.Activities)
		if providerBoundary != nil {
			if cfg.ProviderHandoff != nil {
				events = append([]ports.ChatEvent{{
					Kind:            ports.ChatEventActivityCompleted,
					ProviderEventID: providerBoundary.ID + ":context-boundary",
					ProviderItemID:  providerBoundary.ID + ":context-boundary",
					ActivityKind:    domain.ActivityKindSystem, ActivityStatus: domain.ActivityStatusCompleted,
					Summary: "Native conversation changed. Earlier messages are retained; continuity with this agent's context is not verified.",
					Detail:  []byte(`{"event":"context.boundary","reason":"native_terminal_handoff"}`),
				}}, events...)
			}
			commitProviderHistory = func(commitCtx context.Context) error {
				if handoff := cfg.ProviderHandoff; handoff != nil {
					if err := s.store.SettleOrphanedTurns(commitCtx, handoff.PreviousSessionID, s.now()); err != nil {
						return err
					}
					if err := s.store.FailPendingApprovals(commitCtx, conversation.ID, s.now()); err != nil {
						return err
					}
					if err := s.store.FailPendingInputs(commitCtx, conversation.ID, s.now()); err != nil {
						return err
					}
				}
				return controller.projectNativeHistory(commitCtx, events)
			}
		} else if err := controller.projectNativeHistory(ctx, events); err != nil {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, err
		}
	}
	commit, err := notifyControllerReady(
		cfg, controller, providerBoundary, commitProviderHistory,
	)
	if err != nil {
		_ = cleanupUnpublishedConversation(conv, cfg.ProviderConversationID == "")
		return nil, err
	}
	if providerBoundary != nil {
		if cfg.ControllerReady == nil {
			if commitProviderHistory != nil {
				_ = cleanupUnpublishedConversation(conv, false)
				return nil, errors.New("commit native history: atomic provider-boundary lifecycle is unavailable")
			}
			var err error
			if owner.Kind == domain.ConversationOwnerReview {
				err = s.store.CreateAndActivateReviewConversationBranch(ctx, owner.ID, *providerBoundary, generation, s.now())
			} else {
				err = s.store.CreateAndActivateConversationBranch(ctx, cfg.SessionID, *providerBoundary, generation, s.now())
			}
			if err != nil {
				_ = cleanupUnpublishedConversation(conv, false)
				return nil, fmt.Errorf("commit fresh provider boundary: %w", err)
			}
			commit.Conversation = controller.conversation
			commit.Conversation.ActiveBranchID = providerBoundary.ID
			commit.Conversation.UpdatedAt = s.now()
		} else if commit.Conversation.ActiveBranchID != providerBoundary.ID {
			_ = cleanupUnpublishedConversation(conv, false)
			return nil, fmt.Errorf("commit chat controller: provider boundary %s was not activated", providerBoundary.ID)
		}
	}
	if commit.Conversation.ID != "" {
		controller.conversation.ActiveBranchID = commit.Conversation.ActiveBranchID
		controller.conversation.Settings = commit.Conversation.Settings
		controller.conversation.UpdatedAt = commit.Conversation.UpdatedAt
		controller.settings = commit.Conversation.Settings
	}
	if commit.ControllerOwner != (domain.SessionControllerOwner{}) {
		cfg.ExpectedControllerOwner = commit.ControllerOwner
	} else {
		cfg.ExpectedControllerOwner.Harness = cfg.Harness
		cfg.ExpectedControllerOwner.Mode = domain.SessionModeChat
		cfg.ExpectedControllerOwner.IsTerminated = false
		cfg.ExpectedControllerOwner.RuntimeLaunchID = ""
		cfg.ExpectedControllerOwner.ProviderConversationID = controller.ProviderConversationID()
		cfg.ExpectedControllerOwner.ControllerGeneration = controller.Generation()
	}
	s.mu.Lock()
	s.ownerControllers[owner] = controller
	if owner.Kind == domain.ConversationOwnerSession {
		s.controllers[cfg.SessionID] = controller
	}
	cfg.ProviderHandoff = nil
	cfg.ProviderSwitch = nil
	cfg.ProviderScopeID = ""
	cfg.AwakeReconnectTarget = nil
	cfg.HistoryMode = ports.ChatHistoryImport
	s.startConfigs[owner] = cloneStartConfig(cfg)
	delete(s.awakeInterrupted, owner)
	controller.start()
	s.mu.Unlock()

	go func() {
		controller.Wait()
		controller.waitForBranchHandoff()
		s.mu.Lock()
		if current, ok := s.ownerControllers[owner]; ok && current == controller {
			controller.mu.Lock()
			interrupted := controller.transportInterrupted && !controller.explicitClose
			controller.mu.Unlock()
			if interrupted && owner.Kind == domain.ConversationOwnerSession && cfg.ExpectedControllerOwner.ControllerGeneration != "" {
				s.awakeInterrupted[owner] = domain.AwakeAgentTarget{SessionID: cfg.SessionID, Owner: cfg.ExpectedControllerOwner}
			}
			delete(s.ownerControllers, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, cfg.SessionID)
			}
		}
		s.mu.Unlock()
	}()

	return controller, nil
}

func cleanupUnpublishedConversation(conv ports.ChatConversation, fresh bool) error {
	terminator, canTerminate := conv.(ports.ChatProviderTerminator)
	liveReconnect := false
	if reconnected, ok := conv.(ports.ChatLiveReconnector); ok {
		liveReconnect = reconnected.ReconnectedLive()
	}
	if canTerminate && (fresh || !liveReconnect) {
		return terminator.Terminate()
	}
	return conv.Close()
}

func (s *Service) Controller(sessionID domain.SessionID) (*Controller, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	controller, ok := s.controllers[sessionID]
	if !ok {
		return nil, ErrNoController
	}
	return controller, nil
}

func (s *Service) ControllerForOwner(owner domain.ConversationOwner) (*Controller, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	controller, ok := s.ownerControllers[owner]
	if !ok {
		return nil, ErrNoController
	}
	return controller, nil
}

func (s *Service) HasLiveChatController(sessionID domain.SessionID) bool {
	s.mu.RLock()
	controller := s.controllers[sessionID]
	s.mu.RUnlock()
	return controller != nil && controller.State() != ports.ChatControllerStopped
}

func (s *Service) HasLiveControllerForOwner(owner domain.ConversationOwner) bool {
	controller, err := s.ControllerForOwner(owner)
	return err == nil && controller.State() != ports.ChatControllerStopped
}

func (s *Service) PreservesProviderOnRestart(sessionID domain.SessionID) bool {
	controller, err := s.Controller(sessionID)
	if err != nil || controller.State() == ports.ChatControllerStopped {
		return false
	}
	preserver, ok := controller.conv.(ports.ChatProviderPreserver)
	return ok && preserver.PreservesProviderOnClose()
}

func (s *Service) requireChatSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("read session %s: %w", id, err)
	}
	if !found {
		return domain.SessionRecord{}, ports.ErrSessionNotFound
	}
	if domain.NormalizeSessionMode(record.Mode) != domain.SessionModeChat {
		return domain.SessionRecord{}, ErrNotChatMode
	}
	return record, nil
}

func (s *Service) Send(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
) (domain.ConversationTurn, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if err := validateFileReferenceContent(msg, string(id)); err != nil {
		return domain.ConversationTurn{}, err
	}
	if s.switchBeforeSend(ctx, record) {
		if record, err = s.requireChatSession(ctx, id); err != nil {
			return domain.ConversationTurn{}, err
		}
	}
	behindAccountMove := !record.ProvisionState.IsProvisioning() && record.HibernatedAt == nil &&
		s.accountMoveAwaitingTarget(ctx, id)
	if msg.ClientMessageID != "" && len(msg.Excerpts) > 0 {
		if controller, controllerErr := s.Controller(id); controllerErr == nil {
			if existing, found, lookupErr := controller.store.ConversationMessageByClientID(ctx, controller.conversation.ID, msg.ClientMessageID); lookupErr != nil {
				return domain.ConversationTurn{}, fmt.Errorf("read message delivery: %w", lookupErr)
			} else if found {
				if !fileReferenceContentsMatch(existing.DeliveryContentJSON, msg.Content) {
					return domain.ConversationTurn{}, domain.ErrClientMessageConflict
				}
				if err := validateSnapshotRetry(msg, existing); err != nil {
					return domain.ConversationTurn{}, err
				}
				msg.ClientPayloadHash = existing.ClientPayloadHash
				return controller.Send(ctx, msg)
			}
		}
	}
	msg.ClientPayloadHash, err = clientPayloadHash(msg)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	var reports reportsvc.PreparedBatch
	if s.reports != nil && msg.Origin != domain.MessageOriginAutomation {
		var err error
		reports, err = s.reports.PreparePiggyback(ctx, id)
		if err != nil {
			return domain.ConversationTurn{}, fmt.Errorf("prepare worker reports: %w", err)
		}
		msg.Text = reports.AppendToUserMessage(msg.Text)
	}
	var turn domain.ConversationTurn
	if record.ProvisionState.IsProvisioning() || record.HibernatedAt != nil || behindAccountMove {
		turn, err = s.queueWithoutController(ctx, record, msg, behindAccountMove)
		if errors.Is(err, ErrNotProvisioning) {
			latest, readErr := s.requireChatSession(ctx, id)
			if readErr == nil && !latest.IsTerminated && latest.ProvisionState.WithDefault() == domain.SessionProvisionReady {
				var controller *Controller
				var release func()
				controller, release, err = s.workingController(ctx, id)
				if err == nil {
					if hydrateErr := hydrateExcerptReferences(ctx, controller, &msg); hydrateErr != nil {
						err = hydrateErr
					} else {
						turn, err = controller.Send(ctx, msg)
					}
					release()
				}
			}
		}
		if err == nil && record.HibernatedAt != nil && turn.ID != "" {
			s.startBackgroundWake(id)
		}
	} else {
		var controller *Controller
		var release func()
		controller, release, err = s.workingController(ctx, id)
		if err == nil {
			if hydrateErr := hydrateExcerptReferences(ctx, controller, &msg); hydrateErr != nil {
				err = hydrateErr
			} else {
				turn, err = controller.Send(ctx, msg)
			}
			release()
		}
	}
	if err != nil {
		if s.reports != nil {
			_ = s.reports.ReleasePiggyback(ctx, reports, err)
		}
		return turn, err
	}
	if s.reports != nil {
		if turn.ID == "" {
			_ = s.reports.ReleasePiggyback(ctx, reports, errors.New("duplicate chat message"))
			return turn, nil
		}
		if err := s.reports.AcceptPiggyback(ctx, reports); err != nil {
			return domain.ConversationTurn{}, fmt.Errorf("acknowledge worker reports: %w", err)
		}
	}
	return turn, nil
}

func (s *Service) SendForOwner(ctx context.Context, owner domain.ConversationOwner, msg ports.ChatUserMessage) (domain.ConversationTurn, error) {
	for _, block := range msg.Content {
		if block.Type == "file_reference" {
			return domain.ConversationTurn{}, fmt.Errorf("%w: file annotations require the main chat", ErrExcerptInvalid)
		}
	}
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if msg.ClientMessageID != "" && len(msg.Excerpts) > 0 {
		if existing, found, lookupErr := controller.store.ConversationMessageByClientID(
			ctx, controller.conversation.ID, msg.ClientMessageID,
		); lookupErr != nil {
			return domain.ConversationTurn{}, fmt.Errorf("read message delivery: %w", lookupErr)
		} else if found {
			if !fileReferenceContentsMatch(existing.DeliveryContentJSON, msg.Content) {
				return domain.ConversationTurn{}, domain.ErrClientMessageConflict
			}
			if err := validateSnapshotRetry(msg, existing); err != nil {
				return domain.ConversationTurn{}, err
			}
			msg.ClientPayloadHash = existing.ClientPayloadHash
			return controller.Send(ctx, msg)
		}
	}
	if hasExcerptSnapshots(msg.Excerpts) {
		hash, err := clientPayloadHash(msg)
		if err != nil {
			return domain.ConversationTurn{}, err
		}
		msg.ClientPayloadHash = hash
	}
	if err := hydrateExcerptReferences(ctx, controller, &msg); err != nil {
		return domain.ConversationTurn{}, err
	}
	return controller.Send(ctx, msg)
}

func hydrateExcerptReferences(ctx context.Context, controller *Controller, msg *ports.ChatUserMessage) error {
	if len(msg.Excerpts) == 0 {
		return nil
	}
	if err := validateExcerptReferences(msg.Excerpts, controller.conversation.ID); err != nil {
		return err
	}
	if hasExcerptSnapshots(msg.Excerpts) {
		var legacy []ports.ChatExcerptReference
		for _, ref := range msg.Excerpts {
			if ref.Snapshot != nil {
				msg.Content = append(msg.Content, snapshotExcerptContent(ref))
			} else {
				legacy = append(legacy, ref)
			}
		}
		msg.Excerpts = legacy
		if len(legacy) == 0 {
			return nil
		}
	}
	messages, err := controller.store.ConversationMessages(ctx, controller.conversation.ID)
	if err != nil {
		return fmt.Errorf("read excerpt transcript: %w", err)
	}
	for _, excerpt := range msg.Excerpts {
		text := strings.TrimSpace(excerpt.Text)
		var source domain.ConversationMessage
		found := false
		for _, candidate := range messages {
			if candidate.ID == excerpt.MessageID {
				source, found = candidate, true
				break
			}
		}
		if !found || source.Revision != excerpt.Revision || source.Streaming || !sourceContainsExcerptSelection(source, text) || (source.Role != domain.MessageRoleUser && source.Role != domain.MessageRoleAssistant) {
			return fmt.Errorf("%w: source message changed or no longer contains the selection; reselect it", ErrExcerptStale)
		}
		var userText, assistantText string
		if source.TurnID == "" {
			if source.Role == domain.MessageRoleUser {
				userText = source.Text
			} else {
				assistantText = source.Text
			}
		} else if turn, err := controller.store.TurnByID(ctx, source.TurnID); err != nil || turn.ConversationID != controller.conversation.ID || turn.State != domain.TurnStateCompleted || turn.RolledBackAt != nil {
			return fmt.Errorf("%w: source turn is not complete", ErrExcerptStale)
		}
		for _, candidate := range messages {
			if source.TurnID == "" || candidate.TurnID != source.TurnID || candidate.Streaming {
				continue
			}
			switch candidate.Role {
			case domain.MessageRoleUser:
				if userText != "" {
					userText += "\n\n"
				}
				userText += candidate.Text
			case domain.MessageRoleAssistant:
				if assistantText != "" {
					assistantText += "\n\n"
				}
				assistantText += candidate.Text
			}
		}
		if source.TurnID != "" && (userText == "" || assistantText == "") {
			return fmt.Errorf("%w: source turn has no complete user and assistant messages", ErrExcerptStale)
		}
		msg.Content = append(msg.Content, ports.ChatContent{
			Type: "excerpt", URI: ports.ChatExcerptResourceURIPrefix + source.ID,
			Name: fmt.Sprintf("%s message", source.Role), MIMEType: "text/plain", Text: text,
			Excerpt: &ports.ChatExcerptContext{
				Reference: excerpt, SelectedText: text,
				UserMessage:      truncateExcerptContext(userText),
				AssistantMessage: truncateExcerptContext(assistantText),
			},
		})
	}
	msg.Excerpts = nil
	return nil
}

func validateExcerptReferences(excerpts []ports.ChatExcerptReference, conversationID string) error {
	if len(excerpts) > maxExcerptReferences {
		return fmt.Errorf("%w: at most %d excerpts may be attached", ErrExcerptInvalid, maxExcerptReferences)
	}
	total, rawTotal := 0, 0
	for _, excerpt := range excerpts {
		text := strings.TrimSpace(excerpt.Text)
		if excerpt.ConversationID != conversationID || excerpt.MessageID == "" || (text == "" && !isFlowchartOnlySnapshot(excerpt.Snapshot)) || excerpt.Revision < 0 {
			return fmt.Errorf("%w: source conversation, message, revision, and text are required", ErrExcerptInvalid)
		}
		if utf8.RuneCountInString(excerpt.Comment) > maxExcerptCommentRunes {
			return fmt.Errorf("%w: annotation comment exceeds %d characters", ErrExcerptInvalid, maxExcerptCommentRunes)
		}
		if excerpt.Snapshot != nil {
			if err := validateExcerptSnapshot(excerpt.Snapshot); err != nil {
				return err
			}
			rawTotal += len(excerpt.Snapshot.RawText)
			if rawTotal > maxExcerptTextBytes {
				return fmt.Errorf("%w: raw excerpts exceed %d bytes", ErrExcerptInvalid, maxExcerptTextBytes)
			}
		}
		total += len(excerpt.Text)
		if total > maxExcerptTextBytes {
			return fmt.Errorf("%w: attached excerpts exceed %d bytes", ErrExcerptInvalid, maxExcerptTextBytes)
		}
	}
	return nil
}

func truncateExcerptContext(text string) string {
	if len(text) <= maxExcerptPairedTextBytes {
		return text
	}
	return truncateUTF8Head(text, maxExcerptPairedTextBytes) + "\n[truncated]"
}

func (s *Service) Resolve(
	ctx context.Context,
	id domain.SessionID,
	requestID string,
	decision ports.ChatDecision,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.Resolve(ctx, requestID, decision)
}

func (s *Service) ResolveForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, decision ports.ChatDecision) error {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return err
	}
	return controller.Resolve(ctx, requestID, decision)
}

func (s *Service) ResolveInput(
	ctx context.Context,
	id domain.SessionID,
	requestID string,
	response ports.ChatInputResponse,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.ResolveInput(ctx, requestID, response)
}

func (s *Service) ResolveInputForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, response ports.ChatInputResponse) error {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return err
	}
	return controller.ResolveInput(ctx, requestID, response)
}

func (s *Service) Interrupt(ctx context.Context, id domain.SessionID) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.Interrupt(ctx)
}

func (s *Service) InterruptForOwner(ctx context.Context, owner domain.ConversationOwner) error {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return err
	}
	return controller.Interrupt(ctx)
}

func (s *Service) ArmChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	controller, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		return nil
	}
	if err != nil {
		return err
	}
	if controller.State() == ports.ChatControllerStopped {
		return nil
	}
	return controller.ArmHandoff(ctx, policy)
}

func (s *Service) PrepareChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	controller, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		return nil
	}
	if err != nil {
		return err
	}
	if controller.State() == ports.ChatControllerStopped {
		return nil
	}
	if err := controller.BeginHandoff(ctx, policy); err != nil {
		return err
	}
	s.mu.Lock()
	if turn := controller.HandoffInterruptedTurn(); turn != "" {
		s.handoffInterrupted[id] = turn
	} else {
		delete(s.handoffInterrupted, id)
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) AbortChatHandoff(id domain.SessionID) {
	s.mu.Lock()
	delete(s.handoffInterrupted, id)
	s.mu.Unlock()
	controller, err := s.Controller(id)
	if err == nil {
		controller.AbortHandoff()
	}
}

func (s *Service) Stop(ctx context.Context, id domain.SessionID) error {
	owner := domain.SessionConversationOwner(id)
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()
	if err := s.closeSessionSides(ctx, id); err != nil {
		s.log.Warn("side chat cleanup deferred while stopping session", "session", id, "error", err)
	}
	s.viewMu.Lock()
	delete(s.viewClosedAt, id)
	delete(s.viewLeases, id)
	s.viewMu.Unlock()

	s.mu.RLock()
	controller, ok := s.controllers[id]
	s.mu.RUnlock()
	if !ok {
		s.mu.Lock()
		delete(s.startConfigs, owner)
		delete(s.awakeInterrupted, owner)
		s.mu.Unlock()
		var stopErr error
		if s.stopProviderHost != nil {
			stopErr = s.stopProviderHost(ctx, id)
		}
		return errors.Join(stopErr, s.clearHibernation(ctx, id))
	}
	controller.mu.Lock()
	preserved := controller.preserveProviderOnStop
	hibernating := controller.handoff == controllerHandoffHibernate
	controller.mu.Unlock()
	var hostErr error
	if hibernating && s.stopProviderHost != nil {
		hostErr = s.stopProviderHost(ctx, id)
	}
	err := errors.Join(hostErr, controller.Terminate(ctx))
	if preserved && !hibernating && s.stopProviderHost != nil {
		err = errors.Join(err, s.stopProviderHost(ctx, id))
	}

	select {
	case <-controller.stopped:
		if hibernating {
			err = errors.Join(err, controller.cleanupOwnedControllerWork(ctx, s.now()))
		}
		s.mu.Lock()
		if current, found := s.controllers[id]; found && current == controller {
			delete(s.controllers, id)
		}
		delete(s.startConfigs, owner)
		delete(s.awakeInterrupted, owner)
		s.mu.Unlock()
	default:
	}
	return errors.Join(err, s.clearHibernation(ctx, id))
}

func (s *Service) StopForOwner(ctx context.Context, owner domain.ConversationOwner) error {
	if owner.Kind == domain.ConversationOwnerSession {
		return s.Stop(ctx, domain.SessionID(owner.ID))
	}
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()

	controller, err := s.ControllerForOwner(owner)
	if errors.Is(err, ErrNoController) {
		s.mu.Lock()
		delete(s.startConfigs, owner)
		delete(s.awakeInterrupted, owner)
		s.mu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	err = controller.Terminate(ctx)
	select {
	case <-controller.stopped:
		s.mu.Lock()
		if s.ownerControllers[owner] == controller {
			delete(s.ownerControllers, owner)
			delete(s.startConfigs, owner)
			delete(s.awakeInterrupted, owner)
			if owner.Kind == domain.ConversationOwnerSession {
				delete(s.controllers, controller.sessionID)
			}
		}
		s.mu.Unlock()
	default:
	}
	return err
}

func (s *Service) TerminateAll(ctx context.Context) error {
	if s.sides != nil {
		s.sides.stopAll()
	}
	s.mu.RLock()
	owners := make([]domain.ConversationOwner, 0, len(s.ownerControllers))
	for owner := range s.ownerControllers {
		owners = append(owners, owner)
	}
	s.mu.RUnlock()
	errs := make(chan error, len(owners))
	var workers sync.WaitGroup
	for _, owner := range owners {
		workers.Go(func() {
			if err := s.StopForOwner(ctx, owner); err != nil {
				errs <- fmt.Errorf("stop chat owner %s/%s: %w", owner.Kind, owner.ID, err)
			}
		})
	}
	workers.Wait()
	close(errs)
	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (s *Service) StopAll(ctx context.Context) {
	if s.sides != nil {
		s.sides.stopAll()
	}
	s.mu.Lock()
	type shutdownTarget struct {
		owner      domain.ConversationOwner
		id         domain.SessionID
		controller *Controller
	}
	targets := make([]shutdownTarget, 0, len(s.ownerControllers))
	for owner, controller := range s.ownerControllers {
		targets = append(targets, shutdownTarget{owner: owner, id: controller.sessionID, controller: controller})
	}
	s.mu.Unlock()
	slices.SortFunc(targets, func(a, b shutdownTarget) int {
		return strings.Compare(string(a.owner.Kind)+":"+a.owner.ID, string(b.owner.Kind)+":"+b.owner.ID)
	})

	for _, target := range targets {
		gate := s.controllerGate(target.owner)
		if !gate.tryLock() {
			if err := gate.lock(ctx); err != nil {
				s.log.Error("failed to lock chat controller gate during shutdown", "session", target.id, "error", err)
				continue
			}
		}
		s.mu.RLock()
		current, ok := s.ownerControllers[target.owner]
		s.mu.RUnlock()
		if !ok || current != target.controller {
			gate.unlock()
			continue
		}
		if err := target.controller.Close(ctx); err != nil {
			s.log.Error("failed to close chat controller", "session", target.id, "error", err)
		}
		select {
		case <-target.controller.stopped:
			s.mu.Lock()
			if current, ok := s.ownerControllers[target.owner]; ok && current == target.controller {
				delete(s.ownerControllers, target.owner)
				delete(s.startConfigs, target.owner)
				if target.owner.Kind == domain.ConversationOwnerSession {
					delete(s.controllers, target.id)
				}
			}
			s.mu.Unlock()
		default:
		}
		gate.unlock()
	}
}

type Snapshot struct {
	Conversation                     domain.ConversationRecord
	ActiveBranch                     domain.ConversationBranch
	EditFloorSequence                int64
	NativeForkAvailableAfterSequence int64
	SessionID                        domain.SessionID
	Harness                          domain.AgentHarness
	Mode                             domain.SessionMode
	Controller                       ports.ChatControllerState
	Turns                            []domain.ConversationTurn
	Messages                         []domain.ConversationMessage
	Activities                       []domain.ConversationActivity
	BranchPoints                     []domain.ConversationBranchPoint
	BranchedFromEarlierMessage       bool
	OldestSequence                   int64
	HasMoreBefore                    bool
	AfterSequence                    int64
	BatchedDeliveries                []domain.ConversationBatchedDelivery
	Usage                            *domain.ConversationUsage
	RateLimits                       *domain.ConversationRateLimits
	Capabilities                     ports.ChatCapabilities
}

type SnapshotReader interface {
	LoadConversationSnapshot(ctx context.Context, conversationID string) (ConversationRows, error)
}

type SnapshotPageReader interface {
	LoadConversationSnapshotPage(ctx context.Context, conversationID string, beforeSequence, afterSequence, limit int64) (ConversationRows, error)
}

type ConversationRows struct {
	Conversation                     domain.ConversationRecord
	ActiveBranch                     domain.ConversationBranch
	EditFloorSequence                int64
	NativeForkAvailableAfterSequence int64
	Turns                            []domain.ConversationTurn
	Messages                         []domain.ConversationMessage
	Activities                       []domain.ConversationActivity
	BranchPoints                     []domain.ConversationBranchPoint
	BranchedFromEarlierMessage       bool
	OldestSequence                   int64
	HasMoreBefore                    bool
	AfterSequence                    int64
	BatchedDeliveries                []domain.ConversationBatchedDelivery
}

func idleControllerState(record domain.SessionRecord) ports.ChatControllerState {
	if record.IsTerminated {
		return ports.ChatControllerStopped
	}
	if record.ProvisionState.IsProvisioning() {
		return ports.ChatControllerConnecting
	}
	if record.HibernatedAt != nil {
		return ports.ChatControllerHibernated
	}
	return ports.ChatControllerStopped
}

func withDispatchingTurnRunning(turns []domain.ConversationTurn, dispatching ...string) []domain.ConversationTurn {
	var out []domain.ConversationTurn
	for i := range turns {
		if turns[i].State != domain.TurnStateQueued || !slices.Contains(dispatching, turns[i].ID) {
			continue
		}
		if out == nil {
			out = append([]domain.ConversationTurn(nil), turns...)
		}
		out[i].State = domain.TurnStateRunning
	}
	if out == nil {
		return turns
	}
	return out
}

func (s *Service) Snapshot(ctx context.Context, id domain.SessionID) (Snapshot, error) {
	record, err := s.requireReadableConversation(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}

	if record.NeedsImportResume() {
		return s.importedSnapshot(ctx, record, 0, 100)
	}

	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return Snapshot{
			SessionID:  id,
			Harness:    record.Harness,
			Mode:       domain.NormalizeSessionMode(record.Mode),
			Controller: idleControllerState(record),
		}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("conversation for %s: %w", id, err)
	}

	rows, err := s.reader.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load conversation %s: %w", conversation.ID, err)
	}

	waking := s.isWaking(id)
	state := idleControllerState(record)
	if waking && !record.IsTerminated && state == ports.ChatControllerStopped {
		state = ports.ChatControllerHibernated
	}
	var caps ports.ChatCapabilities
	var dispatching []string
	if controller, err := s.Controller(id); err == nil {
		dispatching = controller.DispatchingTurnIDs()
		if live := controller.State(); (record.HibernatedAt == nil && !waking) || live != ports.ChatControllerStopped {
			state = live
			caps = controller.Capabilities()
		}
		live := controller.Settings()
		rows.Conversation.Settings.FastMode, rows.Conversation.Settings.PlanMode = live.FastMode, live.PlanMode
	}

	return Snapshot{
		Conversation:                     rows.Conversation,
		ActiveBranch:                     rows.ActiveBranch,
		EditFloorSequence:                rows.EditFloorSequence,
		NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
		SessionID:                        id,
		Harness:                          record.Harness,
		Mode:                             domain.NormalizeSessionMode(record.Mode),
		Controller:                       state,
		Turns:                            withDispatchingTurnRunning(rows.Turns, dispatching...),
		Messages:                         rows.Messages,
		Activities:                       rows.Activities,
		BranchPoints:                     rows.BranchPoints,
		BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
		BatchedDeliveries:                rows.BatchedDeliveries,
		Capabilities:                     caps,
		Usage:                            rows.Conversation.Usage,
		RateLimits:                       rows.Conversation.RateLimits,
	}, nil
}

func (s *Service) SnapshotForReview(ctx context.Context, reviewID string) (Snapshot, error) {
	store, ok := s.store.(reviewerConversationStore)
	if !ok {
		return Snapshot{}, errors.New("reviewer conversation store is unavailable")
	}
	review, found, err := store.GetReviewByID(ctx, reviewID)
	if err != nil {
		return Snapshot{}, err
	}
	if !found {
		return Snapshot{}, ports.ErrSessionNotFound
	}
	if review.InterfaceMode != domain.ReviewerInterfaceChat {
		return Snapshot{}, ErrNotChatMode
	}
	conversation, err := store.ConversationForReview(ctx, reviewID)
	if errors.Is(err, domain.ErrNoConversation) {
		return Snapshot{SessionID: review.SessionID, Harness: domain.AgentHarness(review.Harness), Mode: domain.SessionModeChat, Controller: ports.ChatControllerStopped}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshotForReviewRows(review, rows), nil
}

func (s *Service) snapshotForReviewRows(review domain.Review, rows ConversationRows) Snapshot {
	state := ports.ChatControllerStopped
	var caps ports.ChatCapabilities
	var dispatching []string
	if controller, err := s.ControllerForOwner(domain.ReviewConversationOwner(review.ID)); err == nil {
		state, caps = controller.State(), controller.Capabilities()
		dispatching = controller.DispatchingTurnIDs()
	}
	return Snapshot{Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, EditFloorSequence: rows.EditFloorSequence, NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence, SessionID: review.SessionID, Harness: domain.AgentHarness(review.Harness), Mode: domain.SessionModeChat, Controller: state, Turns: withDispatchingTurnRunning(rows.Turns, dispatching...), Messages: rows.Messages, Activities: rows.Activities, BranchPoints: rows.BranchPoints, BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage, OldestSequence: rows.OldestSequence, HasMoreBefore: rows.HasMoreBefore, BatchedDeliveries: rows.BatchedDeliveries, Capabilities: caps, Usage: rows.Conversation.Usage, RateLimits: rows.Conversation.RateLimits}
}

func (s *Service) SnapshotPage(ctx context.Context, id domain.SessionID, beforeSequence, afterSequence, limit int64) (Snapshot, error) {
	record, err := s.requireReadableConversation(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	if record.NeedsImportResume() {
		return s.importedSnapshot(ctx, record, beforeSequence, limit)
	}

	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return Snapshot{
			SessionID:  id,
			Harness:    record.Harness,
			Mode:       domain.NormalizeSessionMode(record.Mode),
			Controller: idleControllerState(record),
		}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("conversation for %s: %w", id, err)
	}
	if s.pageReader == nil {
		return s.Snapshot(ctx, id)
	}
	rows, err := s.pageReader.LoadConversationSnapshotPage(ctx, conversation.ID, beforeSequence, afterSequence, limit)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load conversation page %s: %w", conversation.ID, err)
	}
	if source := record.Metadata.ImportSource; source != nil && source.HistoryThrough > 0 && !rows.HasMoreBefore && rows.AfterSequence == 0 {
		if rows, err = s.withImportedHistory(ctx, id, source.HistoryThrough, rows, beforeSequence, limit); err != nil {
			return Snapshot{}, err
		}
	}
	waking := s.isWaking(id)
	state := idleControllerState(record)
	if waking && !record.IsTerminated && state == ports.ChatControllerStopped {
		state = ports.ChatControllerHibernated
	}
	var caps ports.ChatCapabilities
	var dispatching []string
	if controller, err := s.Controller(id); err == nil {
		dispatching = controller.DispatchingTurnIDs()
		if live := controller.State(); (record.HibernatedAt == nil && !waking) || live != ports.ChatControllerStopped {
			state = live
			caps = controller.Capabilities()
		}
		live := controller.Settings()
		rows.Conversation.Settings.FastMode, rows.Conversation.Settings.PlanMode = live.FastMode, live.PlanMode
	}
	return Snapshot{
		Conversation:                     rows.Conversation,
		ActiveBranch:                     rows.ActiveBranch,
		EditFloorSequence:                rows.EditFloorSequence,
		NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
		SessionID:                        id,
		Harness:                          record.Harness,
		Mode:                             domain.NormalizeSessionMode(record.Mode),
		Controller:                       state,
		Turns:                            withDispatchingTurnRunning(rows.Turns, dispatching...),
		Messages:                         rows.Messages,
		Activities:                       rows.Activities,
		BranchPoints:                     rows.BranchPoints,
		BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
		OldestSequence:                   rows.OldestSequence,
		HasMoreBefore:                    rows.HasMoreBefore,
		AfterSequence:                    rows.AfterSequence,
		BatchedDeliveries:                rows.BatchedDeliveries,
		Capabilities:                     caps,
		Usage:                            rows.Conversation.Usage,
		RateLimits:                       rows.Conversation.RateLimits,
	}, nil
}

func (s *Service) SnapshotPageForReview(ctx context.Context, reviewID string, beforeSequence, limit int64) (Snapshot, error) {
	if s.pageReader == nil {
		return s.SnapshotForReview(ctx, reviewID)
	}
	store, ok := s.store.(reviewerConversationStore)
	if !ok {
		return Snapshot{}, errors.New("reviewer conversation store is unavailable")
	}
	review, found, err := store.GetReviewByID(ctx, reviewID)
	if err != nil {
		return Snapshot{}, err
	}
	if !found {
		return Snapshot{}, ports.ErrSessionNotFound
	}
	if review.InterfaceMode != domain.ReviewerInterfaceChat {
		return Snapshot{}, ErrNotChatMode
	}
	conversation, err := store.ConversationForReview(ctx, reviewID)
	if errors.Is(err, domain.ErrNoConversation) {
		return s.SnapshotForReview(ctx, reviewID)
	}
	if err != nil {
		return Snapshot{}, err
	}
	rows, err := s.pageReader.LoadConversationSnapshotPage(ctx, conversation.ID, beforeSequence, 0, limit)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshotForReviewRows(review, rows), nil
}

type SnapshotReaderFunc func(ctx context.Context, conversationID string) (ConversationRows, error)

func (f SnapshotReaderFunc) LoadConversationSnapshot(
	ctx context.Context,
	conversationID string,
) (ConversationRows, error) {
	return f(ctx, conversationID)
}

type SnapshotPageReaderFunc func(ctx context.Context, conversationID string, beforeSequence, afterSequence, limit int64) (ConversationRows, error)

func (f SnapshotPageReaderFunc) LoadConversationSnapshotPage(
	ctx context.Context,
	conversationID string,
	beforeSequence, afterSequence, limit int64,
) (ConversationRows, error) {
	return f(ctx, conversationID, beforeSequence, afterSequence, limit)
}

func (s *Service) SupportsChat(harness domain.AgentHarness) bool {
	return s.drivers.SupportsChat(harness)
}

func (s *Service) PreflightChat(
	ctx context.Context,
	harness domain.AgentHarness,
	permissions ports.PermissionMode,
) error {
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return fmt.Errorf("%w: %s has no chat driver", ports.ErrChatUnsupported, harness)
	}
	caps, err := s.driverCapabilities(ctx, harness, driver)
	if err != nil {
		return err
	}
	return chatAdmissionError(driver, harness, caps, permissions)
}

func (s *Service) PermissionModes(ctx context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error) {
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return nil, fmt.Errorf("%w: %s has no chat driver", ports.ErrChatUnsupported, harness)
	}
	caps, err := s.driverCapabilities(ctx, harness, driver)
	if err != nil {
		return nil, err
	}
	checker, _ := driver.(ports.ChatPermissionModeChecker)
	var modes []ports.PermissionMode
	for _, mode := range ports.PermissionModesStrictestFirst {
		if len(ports.MissingCapabilitiesForPermissions(caps, mode)) == 0 && (checker == nil || checker.SupportsPermissionMode(mode)) {
			modes = append(modes, mode)
		}
	}
	return modes, nil
}

func chatAdmissionError(driver ports.ChatDriver, harness domain.AgentHarness, caps ports.ChatCapabilities, permissions ports.PermissionMode) error {
	if err := capabilityAdmissionError(harness, caps, permissions); err != nil {
		return err
	}
	if checker, ok := driver.(ports.ChatPermissionModeChecker); ok && !checker.SupportsPermissionMode(ports.NormalizePermissionMode(permissions)) {
		return fmt.Errorf("%w: %s cannot run with %q", ports.ErrChatPermissionModeUnsupported, harness, permissions)
	}
	return nil
}

// PreflightChatWithEnv checks the task's native prerequisites separately from
// cached global capabilities, before the session manager allocates durable state.
func (s *Service) PreflightChatWithEnv(
	ctx context.Context,
	harness domain.AgentHarness,
	permissions ports.PermissionMode,
	env map[string]string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return fmt.Errorf("%w: %s has no chat driver", ports.ErrChatUnsupported, harness)
	}
	if validator, ok := driver.(ports.ChatEnvironmentValidator); ok {
		if err := validator.ValidateEnvironment(ctx, env); err != nil {
			return err
		}
	}
	return s.PreflightChat(ctx, harness, permissions)
}

func capabilityAdmissionError(
	harness domain.AgentHarness,
	caps ports.ChatCapabilities,
	permissions ports.PermissionMode,
) error {
	missing := ports.MissingCapabilitiesForPermissions(caps, permissions)
	if len(missing) == 0 {
		return nil
	}
	var allowed []ports.PermissionMode
	if ports.NormalizePermissionMode(permissions) != ports.PermissionModeBypassPermissions &&
		len(ports.MissingCapabilitiesForPermissions(caps, ports.PermissionModeBypassPermissions)) == 0 {
		allowed = []ports.PermissionMode{ports.PermissionModeBypassPermissions}
	}
	return &ports.ChatCapabilityError{
		Harness:                harness,
		Missing:                append([]ports.ChatCapability(nil), missing...),
		AllowedPermissionModes: allowed,
	}
}

func (s *Service) ChatUnavailable(ctx context.Context, harnesses []domain.AgentHarness) map[domain.AgentHarness]string {
	out := map[domain.AgentHarness]string{}
	for _, harness := range harnesses {
		driver, err := s.drivers.Driver(harness)
		if err != nil {
			continue
		}
		if checker, ok := driver.(ports.ChatInstallChecker); ok {
			if err := checker.CheckChatInstall(ctx); err != nil {
				out[harness] = err.Error()
			}
		}
	}
	return out
}

type branchProviderRebinder interface {
	RebindUnansweredBranchProvider(ctx context.Context, conversationID, branchID, previous, provider string) error
}

func (s *Service) rebindBranchOnReady(
	ctx context.Context,
	ready func(StartResult) (ControllerCommit, error),
	conversationID, branchID, previous string,
) func(StartResult) (ControllerCommit, error) {
	return func(started StartResult) (ControllerCommit, error) {
		commit := ControllerCommit{Conversation: started.Conversation}
		if ready != nil {
			var err error
			if commit, err = ready(started); err != nil {
				return commit, err
			}
		}
		if rebinder, ok := s.store.(branchProviderRebinder); ok {
			if err := rebinder.RebindUnansweredBranchProvider(context.WithoutCancel(ctx), conversationID, branchID, previous, started.ProviderConversationID); err != nil {
				s.log.Warn("rebind unanswered switch branch", "branch", branchID, "error", err)
			}
		}
		return commit, nil
	}
}

type forkHandoffNoteKeeper interface {
	SetConversationBranchHandoffNote(ctx context.Context, conversationID, branchID, note string) error
}

func withHandoffNote(systemPrompt, note string) string {
	if note == "" {
		return systemPrompt
	}
	return strings.TrimSpace(strings.TrimSpace(systemPrompt) + "\n\nAO provider handoff context:\n\n" + note)
}

func (s *Service) branchUnanswered(ctx context.Context, session domain.SessionID, conversationID string, branch domain.ConversationBranch) bool {
	switchBranch := branch.Strategy == domain.ConversationBranchStrategyApproximateContext && branch.HandoffNote != ""
	ownBoundary := branch.ParentBranchID != "" && branch.SessionID == session
	if s.reader == nil || !switchBranch && !ownBoundary {
		return false
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		return false
	}
	for _, turn := range rows.Turns {
		if turn.BranchID == branch.ID {
			return false
		}
	}
	return true
}

func (s *Service) PrewarmChat(harness domain.AgentHarness) {
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return
	}
	if _, running := s.prewarming.LoadOrStore(harness, struct{}{}); running {
		return
	}
	go func() {
		defer s.prewarming.Delete(harness)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = s.driverCapabilities(ctx, harness, driver)
	}()
}

func (s *Service) driverCapabilities(
	ctx context.Context,
	harness domain.AgentHarness,
	driver ports.ChatDriver,
) (ports.ChatCapabilities, error) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if caps, ok := s.probed[harness]; ok {
		return caps, nil
	}
	caps, err := driver.Probe(ctx)
	if err != nil {
		return nil, err
	}
	s.probed[harness] = caps
	return caps, nil
}

func (s *Service) StartChat(ctx context.Context, cfg StartConfig) (StartResult, error) {
	controller, err := s.Start(ctx, cfg)
	if err != nil {
		return StartResult{}, err
	}
	return controllerStartResult(controller, nil, nil), nil
}

type StartResult = ports.ChatControllerStarted

func (s *Service) StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	controller, err := s.Controller(id)
	if err != nil {
		return "", err
	}
	turn, err := controller.Send(ctx, ports.ChatUserMessage{
		Text:               text,
		SpawnOpeningPrompt: true,
		Origin:             domain.MessageOriginHuman,
	})
	if err != nil {
		return "", err
	}
	return turn.ID, nil
}

var ErrModelsUnsupported = errors.New("chat driver cannot list models")

var ErrConfigOptionsUnsupported = errors.New("chat driver has no session config options")

func (s *Service) Models(ctx context.Context, id domain.SessionID) ([]ports.ChatModel, domain.ConversationSettings, error) {
	controller, _, release, err := s.readingController(ctx, id)
	if err != nil {
		return nil, domain.ConversationSettings{}, err
	}
	defer release()
	return listModels(ctx, controller)
}

func (s *Service) ModelsForOwner(ctx context.Context, owner domain.ConversationOwner) ([]ports.ChatModel, domain.ConversationSettings, error) {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return nil, domain.ConversationSettings{}, err
	}
	return listModels(ctx, controller)
}

func listModels(ctx context.Context, controller *Controller) ([]ports.ChatModel, domain.ConversationSettings, error) {
	lister, ok := controller.conv.(ports.ChatModelLister)
	if !ok {
		return nil, controller.Settings(), ErrModelsUnsupported
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		return nil, controller.Settings(), err
	}
	return models, controller.Settings(), nil
}

func (s *Service) ConfigOptions(ctx context.Context, id domain.SessionID) ([]ports.ChatConfigOption, error) {
	controller, record, release, err := s.readingController(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	configurer, ok := controller.conv.(ports.ChatConfigOptionController)
	if !ok {
		return nil, ErrConfigOptionsUnsupported
	}
	options, err := configurer.ListConfigOptions(ctx)
	return permissionConfigOptions(record.Harness, options), err
}

func (s *Service) SetConfigOption(
	ctx context.Context,
	id domain.SessionID,
	configID string,
	value ports.ChatConfigOptionValue,
) ([]ports.ChatConfigOption, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return nil, err
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	configurer, ok := controller.conv.(ports.ChatConfigOptionController)
	if !ok {
		return nil, ErrConfigOptionsUnsupported
	}
	if record.Harness == domain.HarnessCopilot {
		if err := copilotConfigOptionAllowed(configID, value); err != nil {
			return nil, err
		}
	}
	controller.configMu.Lock()
	defer controller.configMu.Unlock()
	previous := controller.Settings()
	options, err := configurer.SetConfigOption(ctx, configID, value)
	if err != nil {
		return nil, err
	}
	if len(options) == 0 {
		if listed, listErr := configurer.ListConfigOptions(ctx); listErr == nil {
			options = listed
		}
	}
	options = permissionConfigOptions(record.Harness, options)
	settings, _ := settingsFromConfigOptions(previous, options)
	if record.Harness == domain.HarnessClaudeCode {
		if settings.Model == "default" {
			settings.Model = ""
		}
		if settings.ReasoningEffort == "default" {
			settings.ReasoningEffort = ""
		}
	}
	if isOpenCodeHarness(record.Harness) && configID == "mode" {
		for _, option := range options {
			if option.ID == "mode" {
				settings.OpenCodeMode = option.Current.Select
			}
		}
	}
	if settings != previous {
		if err := controller.SetSettings(ctx, settings); err != nil {
			return nil, err
		}
		s.persistPickedModel(id, previous, settings)
	}
	return options, nil
}

func isOpenCodeHarness(harness domain.AgentHarness) bool {
	return harness == domain.HarnessOpenCode || harness == domain.HarnessOpenCodeV2
}

func restoreOpenCodeMode(ctx context.Context, conv ports.ChatConversation, mode string) error {
	configurer, ok := conv.(ports.ChatConfigOptionController)
	if !ok {
		return fmt.Errorf("restore OpenCode mode %q: %w", mode, ErrConfigOptionsUnsupported)
	}
	options, err := configurer.SetConfigOption(ctx, "mode", ports.ChatConfigOptionValue{Select: mode})
	if err != nil {
		return fmt.Errorf("restore OpenCode mode %q: %w", mode, err)
	}
	for _, option := range options {
		if option.ID == "mode" && option.Current.Select == mode {
			return nil
		}
	}
	return fmt.Errorf("restore OpenCode mode %q: provider did not confirm selected mode", mode)
}

func settingsFromConfigOptions(
	settings domain.ConversationSettings,
	options []ports.ChatConfigOption,
) (domain.ConversationSettings, bool) {
	next := settings
	hasEffort := false
	for _, option := range options {
		for _, choice := range option.Choices {
			if choice.Value == option.Current.Select && choice.PermissionMode != "" {
				next.ApprovalMode = choice.PermissionMode
			}
		}
		switch {
		case option.ID == "model" || option.Category == "model":
			if option.Current.Select != "" {
				next.Model = option.Current.Select
			}
		case option.ID == "effort" || option.Category == "thought_level":
			hasEffort = true
			next.ReasoningEffort = option.Current.Select
		}
	}
	if !hasEffort {
		next.ReasoningEffort = ""
	}
	return next, next != settings
}

func (s *Service) Compact(ctx context.Context, id domain.SessionID) (ports.ChatCompactionResult, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return ports.ChatCompactionResult{}, err
	}
	defer release()
	return controller.Compact(ctx)
}

func (s *Service) ReloadMCPServers(
	ctx context.Context,
	id domain.SessionID,
) ([]domain.ConversationMCPServer, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	return controller.ReloadMCPServers(ctx)
}

func (s *Service) RetryTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
) (domain.ConversationTurn, error) {
	return s.retryTurn(ctx, id, turnID, false)
}

func (s *Service) retryTurn(ctx context.Context, id domain.SessionID, turnID string, autoResume bool) (domain.ConversationTurn, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	defer release()
	var result domain.ConversationTurn
	if autoResume {
		result, err = controller.retryUsageLimitTurn(ctx, turnID)
	} else {
		result, err = controller.RetryTurn(ctx, turnID)
	}
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	return result, nil
}

func (s *Service) SetTurnSettings(
	ctx context.Context,
	id domain.SessionID,
	settings domain.ConversationSettings,
) (domain.ConversationSettings, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return domain.ConversationSettings{}, err
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return domain.ConversationSettings{}, err
	}
	defer release()
	return s.setTurnSettings(ctx, controller, domain.SessionConversationOwner(id), settings)
}

var ErrReviewerPermissionsFixed = errors.New("reviewer approval permissions are fixed")

func (s *Service) SetTurnSettingsForOwner(ctx context.Context, owner domain.ConversationOwner, settings domain.ConversationSettings) (domain.ConversationSettings, error) {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return domain.ConversationSettings{}, err
	}
	return s.setTurnSettings(ctx, controller, owner, settings)
}

func (s *Service) setTurnSettings(ctx context.Context, controller *Controller, owner domain.ConversationOwner, settings domain.ConversationSettings) (domain.ConversationSettings, error) {
	controller.configMu.Lock()
	defer controller.configMu.Unlock()
	previous := controller.Settings()
	if owner.Kind == domain.ConversationOwnerReview && settings.ApprovalMode != previous.ApprovalMode {
		return domain.ConversationSettings{}, ErrReviewerPermissionsFixed
	}
	settings.OpenCodeMode = previous.OpenCodeMode
	if err := controller.SetSettings(ctx, settings); err != nil {
		return domain.ConversationSettings{}, err
	}
	if owner.Kind == domain.ConversationOwnerSession {
		s.persistPickedModel(domain.SessionID(owner.ID), previous, settings)
	}
	return controller.Settings(), nil
}

func (s *Service) persistPickedModel(id domain.SessionID, previous, next domain.ConversationSettings) {
	model := strings.TrimSpace(next.Model)
	if model == strings.TrimSpace(previous.Model) || s.onModelChanged == nil {
		return
	}
	s.onModelChanged(id, model)
}

func (s *Service) RelayChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return s.relayChatTurn(ctx, id, text, "", ports.MessageDeliveryOptions{})
}

func (s *Service) RelayChatTurnWithID(
	ctx context.Context,
	id domain.SessionID,
	text, clientMessageID string,
) (string, error) {
	return s.relayChatTurn(ctx, id, text, clientMessageID, ports.MessageDeliveryOptions{})
}

func (s *Service) RelayUserAuthoredChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return s.relayChatTurn(ctx, id, text, "", ports.MessageDeliveryOptions{AuthoredByUser: true})
}

func (s *Service) relayChatTurn(
	ctx context.Context,
	id domain.SessionID,
	text, clientMessageID string,
	options ports.MessageDeliveryOptions,
) (string, error) {
	msg := s.resolveSteerSender(ctx, ports.ChatUserMessage{
		Text: text, ClientMessageID: clientMessageID,
		SenderSessionID: options.SenderSessionID, InteractionAt: options.InteractionAt,
		Origin: domain.MessageOriginAutomation, AuthoredByUser: options.AuthoredByUser,
	})
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		record, err := s.requireChatSession(ctx, id)
		if err != nil {
			return "", err
		}
		if record.HibernatedAt != nil {
			turn, err := s.queueWithoutController(ctx, record, msg, false)
			if err == nil {
				if turn.ID != "" {
					s.startBackgroundWake(id)
				}
				return turn.ID, nil
			}
			if !errors.Is(err, ErrNotProvisioning) {
				return "", err
			}
		}
		controller, release, err := s.admitController(ctx, id, false)
		if errors.Is(err, errAsleep) {
			continue
		}
		if err != nil {
			return "", err
		}
		turn, err := controller.Send(ctx, msg)
		release()
		if err != nil {
			return "", err
		}
		return turn.ID, nil
	}
}

func (s *Service) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	return s.relayChatTurn(ctx, id, text, clientMessageID, options)
}

func (s *Service) StopChat(ctx context.Context, id domain.SessionID) error {
	return s.Stop(ctx, id)
}

func permissionConfigOptions(harness domain.AgentHarness, options []ports.ChatConfigOption) []ports.ChatConfigOption {
	if harness == domain.HarnessCopilot {
		options = copilotComposerOptions(options)
	}
	out := append([]ports.ChatConfigOption(nil), options...)
	for i := range out {
		out[i].Choices = append([]ports.ChatConfigOptionChoice(nil), out[i].Choices...)
		if harness == domain.HarnessCommandCode && out[i].ID == "mode" {
			choices := out[i].Choices[:0]
			for _, choice := range out[i].Choices {
				mode, ok := commandCodeApprovalMode(choice.Value)
				if !ok {
					continue
				}
				choice.PermissionMode = mode
				choices = append(choices, choice)
			}
			out[i].Choices = choices
			continue
		}
		for j := range out[i].Choices {
			choice := &out[i].Choices[j]
			choice.PermissionMode = ""
			if out[i].ID != "mode" {
				continue
			}
			switch harness {
			case domain.HarnessClaudeCode:
				switch choice.Value {
				case "manual", "default":
					choice.PermissionMode = domain.PermissionModeDefault
				case "acceptEdits":
					choice.PermissionMode = domain.PermissionModeAcceptEdits
				case "auto":
					choice.PermissionMode = domain.PermissionModeAuto
				case "bypassPermissions":
					choice.PermissionMode = domain.PermissionModeBypassPermissions
				}
			case domain.HarnessOpenCode, domain.HarnessOpenCodeV2:
				if mode, label, ok := openCodeApprovalTier(choice.Value); ok {
					choice.PermissionMode = mode
					choice.Name = label
				}
			case domain.HarnessAgy:
				if mode, label, ok := agyApprovalTier(choice.Value); ok {
					choice.PermissionMode = mode
					choice.Name = label
				}
			}
		}
	}
	return out
}

func copilotComposerOptions(options []ports.ChatConfigOption) []ports.ChatConfigOption {
	out := make([]ports.ChatConfigOption, 0, len(options))
	for _, option := range options {
		if option.ID == copilotAllowAllOption {
			continue
		}
		if option.ID == "mode" {
			choices := make([]ports.ChatConfigOptionChoice, 0, len(option.Choices))
			for _, choice := range option.Choices {
				if !isCopilotAutopilot(choice.Value) {
					choices = append(choices, choice)
				}
			}
			option.Choices = choices
		}
		out = append(out, option)
	}
	return out
}

func copilotDefaultModeChoice(choices []ports.ChatConfigOptionChoice) string {
	for _, choice := range choices {
		if isCopilotAutopilot(choice.Value) {
			continue
		}
		if strings.EqualFold(choice.Name, "agent") || strings.HasSuffix(choice.Value, "#agent") || choice.Value == "agent" {
			return choice.Value
		}
	}
	for _, choice := range choices {
		if strings.EqualFold(choice.Name, "plan") || strings.HasSuffix(choice.Value, "#plan") || choice.Value == "plan" {
			return choice.Value
		}
	}
	return ""
}

func sanitizeCopilotLoadedMode(ctx context.Context, conv ports.ChatConversation) error {
	configurer, ok := conv.(ports.ChatConfigOptionController)
	if !ok {
		return fmt.Errorf("read Copilot session mode: config options unavailable for resumed conversation")
	}
	options, err := configurer.ListConfigOptions(ctx)
	if err != nil {
		return fmt.Errorf("read Copilot session mode: %w", err)
	}
	modeFound := false
	for _, option := range options {
		if option.ID == "mode" {
			modeFound = true
		}
		if option.ID == "mode" && isCopilotAutopilot(option.Current.Select) {
			target := copilotDefaultModeChoice(option.Choices)
			if target == "" {
				return fmt.Errorf("%w: Copilot session is in Autopilot mode and has no safe mode fallback", ports.ErrChatConfigOptionInvalid)
			}
			updated, err := configurer.SetConfigOption(ctx, "mode", ports.ChatConfigOptionValue{Select: target})
			if err != nil {
				return fmt.Errorf("reset Copilot Autopilot mode to %q: %w", target, err)
			}
			updatedModeFound := false
			for _, opt := range updated {
				if opt.ID != "mode" {
					continue
				}
				updatedModeFound = true
				if opt.Current.Select != target {
					return fmt.Errorf("reset Copilot Autopilot mode: provider reports %q, want %q", opt.Current.Select, target)
				}
			}
			if !updatedModeFound {
				return fmt.Errorf("reset Copilot Autopilot mode: provider did not report the resulting mode")
			}
			return nil
		}
	}
	if !modeFound {
		return fmt.Errorf("read Copilot session mode: mode option unavailable for resumed conversation")
	}
	return nil
}

func copilotConfigOptionAllowed(configID string, value ports.ChatConfigOptionValue) error {
	if configID == copilotAllowAllOption {
		return fmt.Errorf("%w: Copilot's allow_all follows the session's approval mode", ports.ErrChatConfigOptionInvalid)
	}
	if configID == "mode" && isCopilotAutopilot(value.Select) {
		return fmt.Errorf("%w: Copilot Autopilot bypasses Orchestrator approvals; choose bypass permissions instead", ports.ErrChatConfigOptionInvalid)
	}
	return nil
}

const copilotAllowAllOption = "allow_all"

func isCopilotAutopilot(value string) bool {
	value = strings.TrimSpace(value)
	return value == "autopilot" || strings.HasSuffix(value, "#autopilot")
}

func commandCodeApprovalMode(value string) (domain.PermissionMode, bool) {
	switch value {
	case "default":
		return domain.PermissionModeDefault, true
	case "auto-accept":
		return domain.PermissionModeAuto, true
	case "bypass":
		return domain.PermissionModeBypassPermissions, true
	default:
		return "", false
	}
}

func agyApprovalTier(value string) (domain.PermissionMode, string, bool) {
	switch value {
	case "default":
		return domain.PermissionModeDefault, "Use agent permissions", true
	case "auto_edit":
		return domain.PermissionModeAcceptEdits, "Accept edits", true
	case "yolo":
		return domain.PermissionModeAuto, "Auto-approve", true
	default:
		return "", "", false
	}
}

func openCodeApprovalTier(value string) (domain.PermissionMode, string, bool) {
	switch value {
	case "ao-default":
		return domain.PermissionModeDefault, "Use agent permissions", true
	case "ao-accept-edits":
		return domain.PermissionModeAcceptEdits, "Accept edits", true
	case "ao-auto":
		return domain.PermissionModeAuto, "Auto-approve", true
	case "ao-bypass":
		return domain.PermissionModeBypassPermissions, "Bypass permissions", true
	default:
		return "", "", false
	}
}

func truncateUTF8Head(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

func (s *Service) InterruptedAwakeAgents() []domain.AwakeAgentTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []domain.AwakeAgentTarget{}
	for owner, target := range s.awakeInterrupted {
		cfg, ok := s.startConfigs[owner]
		if ok && cfg.ExpectedControllerOwner == target.Owner && s.ownerControllers[owner] == nil {
			result = append(result, target)
		}
	}
	return result
}
