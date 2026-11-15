package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reqid"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type sessionStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	UpdateSession(ctx context.Context, rec domain.SessionRecord) error
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	UpdateSessionFromActivitySignal(ctx context.Context, rec domain.SessionRecord, expectedRevision int64) (bool, error)
	UpdateSessionArtifactOutput(ctx context.Context, id domain.SessionID, artifactDir string, outputType domain.SessionOutputType) (bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	GetPR(ctx context.Context, prURL string) (domain.PullRequest, bool, error)
	ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error)
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
	GetPRLastNudgeSignature(ctx context.Context, prURL string) (string, error)
	UpdatePRLastNudgeSignature(ctx context.Context, prURL, payload string) error
}

type semanticDeliveryActivityStore interface {
	UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(
		context.Context,
		domain.SessionRecord,
		int64,
		string,
	) (bool, error)
}

type controllerEpochStore interface {
	CommitSessionControllerEpoch(
		context.Context,
		domain.SessionID,
		domain.SessionMode,
		domain.SessionMode,
		string,
		time.Time,
	) (bool, error)
	CommitSessionControllerEpochArchivedChat(
		context.Context,
		domain.SessionID,
		domain.SessionMode,
		domain.SessionMode,
		string,
		time.Time,
	) (bool, error)
	RestoreSessionControllerEpoch(
		context.Context,
		domain.SessionID,
		domain.SessionMode,
		domain.SessionMode,
		string,
		time.Time,
	) (bool, error)
}

type interfaceTransitionReader interface {
	GetActiveSessionInterfaceTransition(
		context.Context,
		domain.SessionID,
	) (domain.SessionInterfaceTransition, bool, error)
}

type chatSpawnStore interface {
	CommitChatSpawn(context.Context, domain.SessionRecord, domain.ConversationBranch) error
}

type preparedChatSpawnStore interface {
	CommitChatSpawnPrepared(
		context.Context,
		domain.SessionRecord,
		domain.ConversationBranch,
		*domain.ChatProviderHandoff,
		func(context.Context) error,
	) error
}

type notificationSink interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
	Resolve(ctx context.Context, res ports.NotificationResolution) error
}

type projectConfigLoader interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

type sessionTerminator interface {
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
}

type sessionUsageFinalizer interface {
	FinalizeSession(
		ctx context.Context,
		id domain.SessionID,
		expectedRuntimeLaunchID string,
		expectedSessionRevision int64,
	) error
}

type sessionUsageReactivator interface {
	ReactivateSession(ctx context.Context, id domain.SessionID, expectedRuntimeLaunchID string) error
}

type sessionOperationGate interface {
	SessionMutationInProgress(id domain.SessionID) bool
}

type pendingLaunch struct {
	launchID string
	ready    chan struct{}
}

type Option func(*Manager)

func WithNotificationSink(sink notificationSink) Option {
	return func(m *Manager) { m.notifications = sink }
}

func WithDeliveryTelemetry(r *deliverytelemetry.Recorder) Option {
	return func(m *Manager) { m.delivery = r }
}

func WithTelemetry(sink ports.EventSink) Option {
	return func(m *Manager) { m.telemetry = sink }
}

func WithDataDir(dir string) Option {
	return func(m *Manager) { m.dataDir = dir }
}

func WithContainerReaper(reaper ports.ContainerReaper, projects projectConfigLoader) Option {
	return func(m *Manager) {
		m.containers = reaper
		m.projects = projects
	}
}

func WithActiveSteering(pred func(domain.AgentHarness) bool) Option {
	return func(m *Manager) {
		if pred != nil {
			m.steerActive = pred
		}
	}
}

func WithStartupSignalGate(pred func(domain.AgentHarness) bool) Option {
	return func(m *Manager) {
		if pred != nil {
			m.startupSignalGatesInput = pred
		}
	}
}

func WithUrgentNudgeGate(pred func(domain.AgentHarness) bool) Option {
	return func(m *Manager) {
		if pred != nil {
			m.urgentNudgeWaitingInputSafe = pred
		}
	}
}

type Manager struct {
	store                sessionStore
	reconcileMu          sync.Mutex
	guard                *sessionguard.Guard
	notifications        notificationSink
	completionTerminator sessionTerminator
	usageFinalizer       sessionUsageFinalizer
	usageReactivator     sessionUsageReactivator
	containers           ports.ContainerReaper
	projects             projectConfigLoader
	operationGateMu      sync.RWMutex
	operationGate        sessionOperationGate
	dataDir              string

	mu                          sync.Mutex
	window                      time.Duration
	clock                       func() time.Time
	react                       reactionState
	telemetry                   ports.EventSink
	delivery                    *deliverytelemetry.Recorder
	flights                     map[domain.SessionID]*toolFlight
	steps                       map[domain.SessionID][]domain.SessionStep
	pendingLaunches             map[domain.SessionID]pendingLaunch
	steerActive                 func(domain.AgentHarness) bool
	startupSignalGatesInput     func(domain.AgentHarness) bool
	urgentNudgeWaitingInputSafe func(domain.AgentHarness) bool
}

func New(store sessionStore, messenger ports.AgentMessenger, opts ...Option) *Manager {
	clock := func() time.Time { return time.Now().UTC() }
	m := &Manager{
		store:                       store,
		window:                      defaultRecentActivityWindow,
		clock:                       clock,
		react:                       newReactionState(),
		flights:                     map[domain.SessionID]*toolFlight{},
		steps:                       map[domain.SessionID][]domain.SessionStep{},
		pendingLaunches:             map[domain.SessionID]pendingLaunch{},
		steerActive:                 func(domain.AgentHarness) bool { return false },
		startupSignalGatesInput:     func(domain.AgentHarness) bool { return false },
		urgentNudgeWaitingInputSafe: func(domain.AgentHarness) bool { return false },
	}
	if messenger != nil {
		m.guard = sessionguard.New(store, messenger, nil)
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.guard != nil {
		m.guard.SetStartupSignalGate(m.startupSignalGatesInput)
	}
	return m
}

func (m *Manager) SetCompletionTerminator(terminator sessionTerminator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completionTerminator = terminator
}

func (m *Manager) SetUsageFinalizer(finalizer sessionUsageFinalizer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageFinalizer = finalizer
	m.usageReactivator, _ = finalizer.(sessionUsageReactivator)
}

func (m *Manager) SetSessionOperationGate(gate sessionOperationGate) {
	m.operationGateMu.Lock()
	m.operationGate = gate
	m.operationGateMu.Unlock()
}

func (m *Manager) sessionMutationInProgress(id domain.SessionID) bool {
	m.operationGateMu.RLock()
	gate := m.operationGate
	m.operationGateMu.RUnlock()
	return gate != nil && gate.SessionMutationInProgress(id)
}

func (m *Manager) PrepareLaunch(id domain.SessionID, launchID string) error {
	launchID = strings.TrimSpace(launchID)
	if launchID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if pending, ok := m.pendingLaunches[id]; ok {
		if pending.launchID == launchID {
			return nil
		}
		return fmt.Errorf("lifecycle: session %q already has launch %q in progress", id, pending.launchID)
	}
	m.pendingLaunches[id] = pendingLaunch{launchID: launchID, ready: make(chan struct{})}
	return nil
}

func (m *Manager) CancelLaunch(id domain.SessionID, launchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishLaunchLocked(id, strings.TrimSpace(launchID))
}

func (m *Manager) ReleaseLaunch(id domain.SessionID, launchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishLaunchLocked(id, strings.TrimSpace(launchID))
}

func (m *Manager) finishLaunchLocked(id domain.SessionID, launchID string) {
	if launchID == "" {
		return
	}
	pending, ok := m.pendingLaunches[id]
	if !ok || pending.launchID != launchID {
		return
	}
	delete(m.pendingLaunches, id)
	close(pending.ready)
}

func (m *Manager) mutate(ctx context.Context, id domain.SessionID, fn func(domain.SessionRecord, time.Time) (domain.SessionRecord, bool)) error {
	m.mu.Lock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		m.mu.Unlock()
		return err
	}
	now := m.clock()
	next, changed := fn(rec, now)
	if !changed {
		m.mu.Unlock()
		return nil
	}
	next.UpdatedAt = now
	if err := m.store.UpdateSession(ctx, next); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.resolveNotifications(ctx, needsInputResolutions(rec, next, now)...)
	return nil
}

func needsInputResolutions(prev, next domain.SessionRecord, now time.Time) []ports.NotificationResolution {
	if !prev.Activity.State.NeedsInput() {
		return nil
	}
	if next.Activity.State.NeedsInput() && !next.IsTerminated {
		return nil
	}
	return []ports.NotificationResolution{{
		Type:       domain.NotificationNeedsInput,
		SessionID:  next.ID,
		ResolvedAt: now,
	}}
}

func (m *Manager) ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	matchesLaunch := func(cur domain.SessionRecord) bool {
		currentLaunch := cur.Metadata.RuntimeLaunchID
		return currentLaunch == "" || f.LaunchID == currentLaunch
	}
	var (
		finalizer           sessionUsageFinalizer
		terminationLaunch   string
		terminationRevision int64
		shouldTerminate     bool
	)
	if err := m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated || !matchesLaunch(cur) {
			return cur, false
		}
		currentLaunch := cur.Metadata.RuntimeLaunchID
		if currentLaunch != "" && f.Runtime == ports.ProbeAlive && f.Workload == ports.ProbeDead {
			if cur.Activity.State == domain.ActivityExited {
				return cur, false
			}
			next := cur
			next.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: timeOr(f.ObservedAt, now)}
			delete(m.flights, id)
			return next, true
		}
		if cur.RuntimeReleased() {
			return cur, false
		}
		if !runtimeClearlyDead(f, cur.Activity, now, m.window) {
			return cur, false
		}
		if m.sessionMutationInProgress(id) {
			return cur, false
		}
		finalizer = m.usageFinalizer
		terminationLaunch = currentLaunch
		terminationRevision = cur.Revision
		shouldTerminate = true
		return cur, false
	}); err != nil || !shouldTerminate {
		return err
	}

	finalizeSessionUsage(ctx, id, terminationLaunch, terminationRevision, finalizer)

	terminated := false
	var beforeEnd domain.SessionRecord
	err := m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated || cur.Revision != terminationRevision ||
			cur.Metadata.RuntimeLaunchID != terminationLaunch || !matchesLaunch(cur) ||
			!runtimeClearlyDead(f, cur.Activity, now, m.window) || m.sessionMutationInProgress(id) {
			return cur, false
		}
		next := cur
		next.IsTerminated = true
		next.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: timeOr(f.ObservedAt, now)}
		delete(m.flights, id)
		delete(m.steps, id)
		terminated = true
		beforeEnd = cur
		return next, true
	})
	if err != nil {
		return err
	}
	if terminated {
		m.delivery.SessionEnded(ctx, beforeEnd, deliverytelemetry.EndRuntimeDied)
		m.reapSessionContainers(ctx, id)
	}
	return nil
}

const maxActivitySignalProjectionRetries = 3

func nonInteractiveNativeEntrypoint(entrypoint string) bool {
	switch strings.TrimSpace(entrypoint) {
	case "sdk-cli", "sdk-ts", "sdk-py":
		return true
	}
	return false
}

func (m *Manager) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	s.SubagentID = strings.TrimSpace(s.SubagentID)
	if s.Event == "subagent-stop" && s.SubagentID == "" {
		return nil
	}
	s.AgentSessionID = strings.TrimSpace(s.AgentSessionID)
	s.LatestUserPrompt = strings.TrimSpace(s.LatestUserPrompt)
	s.LatestAssistantUpdate = strings.TrimSpace(s.LatestAssistantUpdate)
	s.TranscriptPath = strings.TrimSpace(s.TranscriptPath)
	s.LaunchID = strings.TrimSpace(s.LaunchID)
	s.ControllerGeneration = strings.TrimSpace(s.ControllerGeneration)
	if s.ControllerGeneration == "" && nonInteractiveNativeEntrypoint(s.NativeEntrypoint) {
		return nil
	}
	s.ProviderTurnID = strings.TrimSpace(s.ProviderTurnID)
	s.SubmissionID = strings.TrimSpace(s.SubmissionID)
	s.CoordinationID = strings.TrimSpace(s.CoordinationID)
	if s.SubagentID != "" {
		s.AgentSessionID = ""
		s.TranscriptPath = ""
		s.LatestUserPrompt = ""
		s.LatestAssistantUpdate = ""
		s.FinalAssistantMessage = ""
		s.TurnUserRequest = ""
		s.ProviderTurnID = ""
	}
	if !s.ConversationCheckpointOrigin.Valid() {
		s.ConversationCheckpointOrigin = domain.ConversationCheckpointOriginUnknown
	}
	var semanticDeliveryStore semanticDeliveryActivityStore
	semanticDeliverySignal := s.Event == "user-prompt-submit" && s.SubagentID == "" && s.CoordinationID != ""
	switch s.Event {
	case "user-prompt-submit":
		s.LatestAssistantUpdate = ""
	case "stop":
		s.LatestUserPrompt = ""
	default:
		s.LatestUserPrompt = ""
		s.LatestAssistantUpdate = ""
		s.ProviderTurnID = ""
	}
	if !s.Valid && s.SubagentID == "" && s.AgentSessionID == "" && s.LatestUserPrompt == "" && s.LatestAssistantUpdate == "" && s.TranscriptPath == "" {
		return nil
	}
	var intent *ports.NotificationIntent
	m.mu.Lock()
	for {
		pending, ok := m.pendingLaunches[id]
		if !ok || s.LaunchID == "" || pending.launchID != s.LaunchID {
			break
		}
		ready := pending.ready
		m.mu.Unlock()
		select {
		case <-ready:
			m.mu.Lock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	projectionAttempts := 0
	terminalTurnRecorded := false
	originalSignal := s
retryProjection:
	s = originalSignal
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ports.ErrSessionNotFound, id)
	}
	observedRevision := rec.Revision
	now := m.clock()
	if rec.IsTerminated {
		delete(m.flights, id)
		delete(m.steps, id)
		m.mu.Unlock()
		return nil
	}
	if s.ExpectedHarness != "" && s.ExpectedHarness != rec.Harness {
		m.mu.Unlock()
		return nil
	}
	mode := domain.NormalizeSessionMode(rec.Mode)
	if mode == domain.SessionModeTUI && rec.Metadata.RuntimeLaunchID == "" && s.LaunchID == "" {
		if reader, ok := m.store.(interfaceTransitionReader); ok {
			_, active, transitionErr := reader.GetActiveSessionInterfaceTransition(ctx, id)
			if transitionErr != nil {
				m.mu.Unlock()
				return fmt.Errorf("lifecycle: read active interface transition: %w", transitionErr)
			}
			if active {
				m.mu.Unlock()
				return nil
			}
		}
	}
	currentChatController := mode == domain.SessionModeChat &&
		s.ControllerGeneration != "" &&
		s.ControllerGeneration == rec.Metadata.ControllerGeneration
	if mode != domain.SessionModeChat &&
		(s.LaunchID != "" || rec.Metadata.RuntimeLaunchID != "") &&
		s.LaunchID != rec.Metadata.RuntimeLaunchID {
		m.mu.Unlock()
		return nil
	}
	if (mode == domain.SessionModeChat && !currentChatController) ||
		(mode != domain.SessionModeChat && s.ControllerGeneration != "") {
		m.mu.Unlock()
		return nil
	}
	if semanticDeliverySignal && mode != domain.SessionModeChat && s.LaunchID != "" {
		semanticDeliveryStore, _ = m.store.(semanticDeliveryActivityStore)
	}
	if s.ExpectedRevision != nil && rec.Revision != *s.ExpectedRevision {
		m.mu.Unlock()
		return nil
	}
	checkpoint := rec.Metadata
	previousNativeID := rec.Metadata.AgentSessionID
	if previousNativeID == "" {
		previousNativeID = rec.Metadata.ProviderConversationID
	}
	nativeIdentityChanged := s.AgentSessionID != "" && previousNativeID != "" && s.AgentSessionID != previousNativeID
	if nativeIdentityChanged && !s.Timestamp.IsZero() &&
		!rec.Metadata.NativeIdentityObservedAt.IsZero() && !s.Timestamp.After(rec.Metadata.NativeIdentityObservedAt) {
		m.mu.Unlock()
		return nil
	}
	if !terminalTurnRecorded && mode == domain.SessionModeTUI {
		terminalTurnRecorded = true
		m.recordTerminalTurn(ctx, rec, s, now)
	}
	resetConversationCheckpoint :=
		nativeIdentityChanged ||
			(s.AgentSessionID != "" && s.LaunchID != "" &&
				s.LaunchID != rec.Metadata.AgentSessionIDLaunchID) ||
			(s.Event == "session-start" && s.LaunchID != "" &&
				s.LaunchID != rec.Metadata.AgentSessionIDLaunchID)
	if resetConversationCheckpoint {
		checkpoint.LatestUserPrompt = ""
		checkpoint.LatestAssistantUpdate = ""
		checkpoint.LatestAssistantUpdateAt = time.Time{}
		checkpoint.ConversationCheckpointState = domain.ConversationCheckpointEmpty
		checkpoint.ConversationCheckpointGeneration = ""
		checkpoint.ConversationCheckpointNativeID = ""
		checkpoint.ConversationCheckpointTurnID = ""
		if nativeIdentityChanged {
			checkpoint.NativeTranscriptPath = ""
			checkpoint.ConversationCheckpointUnsettled = false
			checkpoint.NativeCheckpointEvidence = ""
		}
	}
	ownerGeneration := ""
	switch mode {
	case domain.SessionModeTUI:
		if s.LaunchID != "" {
			ownerGeneration = s.LaunchID
		}
	case domain.SessionModeChat:
		if s.ControllerGeneration != "" {
			ownerGeneration = s.ControllerGeneration
		}
	}
	checkpointNativeID := s.AgentSessionID
	if checkpointNativeID == "" && ownerGeneration != "" &&
		rec.Metadata.AgentSessionIDLaunchID == ownerGeneration {
		checkpointNativeID = rec.Metadata.AgentSessionID
	}
	claudeNativeBoundary := rec.Harness == domain.HarnessClaudeCode && mode == domain.SessionModeTUI &&
		ownerGeneration != "" && checkpointNativeID != "" && (s.Event == "user-prompt-submit" || s.Event == "stop")
	if claudeNativeBoundary {
		if checkpoint.NativeCheckpointEvidence == "" && !nativeIdentityChanged &&
			(rec.Metadata.ConversationCheckpointState.Trusted() || rec.Metadata.ConversationCheckpointUnsettled) {
			checkpoint.NativeCheckpointEvidence = `{"invalid":true}`
		}
		text := s.LatestAssistantUpdate
		if s.Event == "user-prompt-submit" {
			text = s.LatestUserPrompt
		}
		checkpoint.NativeCheckpointEvidence = domain.AppendNativeCheckpoint(
			checkpoint.NativeCheckpointEvidence, checkpointNativeID, domain.NativeCheckpointObservation{
				Generation: ownerGeneration, PromptID: s.ProviderTurnID, Submission: s.Event == "user-prompt-submit", SubmissionID: s.SubmissionID,
				Text: text, Coordination: s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination,
			})
	}
	switch s.Event {
	case "user-prompt-submit":
		if s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination {
			checkpoint.ConversationCheckpointState = domain.ConversationCheckpointCoordination
			checkpoint.ConversationCheckpointGeneration = ownerGeneration
			checkpoint.ConversationCheckpointNativeID = checkpointNativeID
			checkpoint.ConversationCheckpointTurnID = s.CoordinationID
		} else {
			promptAt := timeOr(s.Timestamp, now)
			sameCheckpointOwner := !resetConversationCheckpoint && ownerGeneration != "" &&
				checkpoint.ConversationCheckpointGeneration == ownerGeneration &&
				checkpoint.ConversationCheckpointNativeID == checkpointNativeID
			if sameCheckpointOwner && (promptAt.Before(checkpoint.LatestUserPromptAt) ||
				(promptAt.Equal(checkpoint.LatestUserPromptAt) && s.LatestUserPrompt == checkpoint.LatestUserPrompt)) {
				if s.LatestUserPrompt != checkpoint.LatestUserPrompt {
					checkpoint.ConversationCheckpointUnsettled = true
				}
				break
			}
			checkpoint.LatestUserPrompt = s.LatestUserPrompt
			if promptAt.After(checkpoint.LatestUserPromptAt) {
				checkpoint.LatestUserPromptAt = promptAt
			}
			checkpoint.LatestAssistantUpdate = ""
			checkpoint.LatestAssistantUpdateAt = time.Time{}
			checkpoint.ConversationCheckpointUnsettled = false
			checkpoint.ConversationCheckpointGeneration = ""
			checkpoint.ConversationCheckpointNativeID = ""
			checkpoint.ConversationCheckpointTurnID = ""
			if ownerGeneration != "" && checkpointNativeID != "" {
				checkpoint.ConversationCheckpointState = domain.ConversationCheckpointPrompt
				checkpoint.ConversationCheckpointGeneration = ownerGeneration
				checkpoint.ConversationCheckpointNativeID = checkpointNativeID
				if rec.Harness == domain.HarnessCodex {
					checkpoint.ConversationCheckpointTurnID = s.ProviderTurnID
				}
			} else {
				checkpoint.ConversationCheckpointState = domain.ConversationCheckpointLegacy
			}
		}
	case "stop":
		if claudeNativeBoundary {
			checkpoint.ConversationCheckpointUnsettled = true
			s.LatestAssistantUpdate = ""
			break
		}
		coordinationStop := s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination ||
			(checkpoint.ConversationCheckpointState == domain.ConversationCheckpointCoordination &&
				ownerGeneration != "" && checkpointNativeID != "" &&
				checkpoint.ConversationCheckpointGeneration == ownerGeneration &&
				checkpoint.ConversationCheckpointNativeID == checkpointNativeID)
		if coordinationStop {
			s.LatestAssistantUpdate = ""
			checkpoint.ConversationCheckpointState = domain.ConversationCheckpointCoordination
			checkpoint.ConversationCheckpointGeneration = ownerGeneration
			checkpoint.ConversationCheckpointNativeID = checkpointNativeID
			if s.CoordinationID != "" {
				checkpoint.ConversationCheckpointTurnID = s.CoordinationID
			}
		} else if checkpoint.ConversationCheckpointState == domain.ConversationCheckpointPrompt &&
			!checkpoint.ConversationCheckpointUnsettled &&
			(s.Timestamp.IsZero() || !s.Timestamp.Before(checkpoint.LatestUserPromptAt)) &&
			ownerGeneration != "" && checkpointNativeID != "" &&
			checkpoint.ConversationCheckpointGeneration == ownerGeneration &&
			checkpoint.ConversationCheckpointNativeID == checkpointNativeID &&
			(checkpoint.ConversationCheckpointTurnID == "" || checkpoint.ConversationCheckpointTurnID == s.ProviderTurnID) {
			checkpoint.LatestAssistantUpdate = s.LatestAssistantUpdate
			checkpoint.LatestAssistantUpdateAt = timeOr(s.Timestamp, now)
			checkpoint.ConversationCheckpointState = domain.ConversationCheckpointComplete
		} else if ownerGeneration != "" && checkpointNativeID != "" {
			if rec.Harness == domain.HarnessAgy && mode == domain.SessionModeTUI {
				s.LatestAssistantUpdate = ""
				break
			}
			checkpoint.ConversationCheckpointUnsettled = true
			s.LatestAssistantUpdate = ""
		} else if checkpoint.LatestUserPrompt == "" && s.LatestAssistantUpdate != "" {
			checkpoint.LatestAssistantUpdate = s.LatestAssistantUpdate
			checkpoint.LatestAssistantUpdateAt = timeOr(s.Timestamp, now)
			checkpoint.ConversationCheckpointState = domain.ConversationCheckpointLegacy
			checkpoint.ConversationCheckpointGeneration = ""
			checkpoint.ConversationCheckpointNativeID = ""
			checkpoint.ConversationCheckpointTurnID = ""
		} else {
			s.LatestAssistantUpdate = ""
		}
	}
	if rec.Activity.State == domain.ActivityExited && (s.Valid || s.SubagentID != "") && s.State != domain.ActivityExited &&
		(s.State != domain.ActivityActive || s.Event != "user-prompt-submit") && !currentChatController &&
		s.Event != "liveness-probe" {
		m.mu.Unlock()
		return nil
	}
	s, subagentFacts, err := reduceSubagentActivity(rec, s, now)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	storedSubagentFacts := ""
	switch rec.Harness {
	case domain.HarnessClaudeCode:
		storedSubagentFacts = rec.Metadata.ClaudeActivityFacts
		checkpoint.ClaudeActivityFacts = subagentFacts
	case domain.HarnessCodex:
		storedSubagentFacts = rec.Metadata.CodexActivityFacts
		checkpoint.CodexActivityFacts = subagentFacts
	}
	checkpointChanged := checkpoint.LatestUserPrompt != rec.Metadata.LatestUserPrompt ||
		!checkpoint.LatestAssistantUpdateAt.Equal(rec.Metadata.LatestAssistantUpdateAt) ||
		!checkpoint.LatestUserPromptAt.Equal(rec.Metadata.LatestUserPromptAt) ||
		checkpoint.LatestAssistantUpdate != rec.Metadata.LatestAssistantUpdate ||
		checkpoint.ConversationCheckpointState != rec.Metadata.ConversationCheckpointState ||
		checkpoint.ConversationCheckpointGeneration != rec.Metadata.ConversationCheckpointGeneration ||
		checkpoint.ConversationCheckpointNativeID != rec.Metadata.ConversationCheckpointNativeID ||
		checkpoint.ConversationCheckpointTurnID != rec.Metadata.ConversationCheckpointTurnID ||
		checkpoint.NativeCheckpointEvidence != rec.Metadata.NativeCheckpointEvidence ||
		checkpoint.ConversationCheckpointUnsettled != rec.Metadata.ConversationCheckpointUnsettled
	metadataChanged := (s.AgentSessionID != "" && rec.Metadata.AgentSessionID != s.AgentSessionID) ||
		(s.AgentSessionID != "" && s.Timestamp.After(rec.Metadata.NativeIdentityObservedAt)) ||
		(s.AgentSessionID != "" && rec.Metadata.AgentSessionIDLaunchID != s.LaunchID) ||
		(s.TranscriptPath != "" && rec.Metadata.NativeTranscriptPath != s.TranscriptPath) ||
		checkpointChanged || subagentFacts != storedSubagentFacts
	toolFlightBeforeProjection := cloneToolFlight(m.flights[id])
	nativeSessionChanged := s.AgentSessionID != "" &&
		(rec.Metadata.AgentSessionID != s.AgentSessionID || rec.Metadata.AgentSessionIDLaunchID != s.LaunchID)
	var usageReactivator sessionUsageReactivator
	if nativeSessionChanged {
		usageReactivator = m.usageReactivator
	}
	if s.Valid {
		s = m.applyToolPrecedenceLocked(id, rec.Harness, rec.Activity.State, s)
	}
	if !s.Valid && !metadataChanged {
		m.mu.Unlock()
		return nil
	}
	stepsBeforeProjection := m.stepsSnapshotLocked(id)
	m.recordStepLocked(id, s, now)
	project := func(next domain.SessionRecord) (applied, retry bool, err error) {
		if semanticDeliveryStore != nil {
			applied, err = semanticDeliveryStore.UpdateSessionFromActivitySignalAndAcceptSemanticDelivery(
				ctx, next, observedRevision, s.CoordinationID,
			)
		} else {
			applied, err = m.store.UpdateSessionFromActivitySignal(ctx, next, observedRevision)
		}
		if applied {
			return applied, false, err
		}
		m.restoreToolFlightLocked(id, toolFlightBeforeProjection)
		m.restoreStepsLocked(id, stepsBeforeProjection)
		if err != nil {
			return false, false, err
		}
		current, found, err := m.store.GetSession(ctx, id)
		if err != nil || !found || current.Revision == observedRevision {
			return false, false, err
		}
		if projectionAttempts >= maxActivitySignalProjectionRetries {
			slog.Default().Warn("lifecycle: activity projection contention", "session", id, "event", s.Event, "attempts", projectionAttempts+1)
			return false, false, fmt.Errorf("%w for %s: concurrent session writes exhausted %d attempts", ports.ErrActivityProjectionContention, id, projectionAttempts+1)
		}
		projectionAttempts++
		return false, true, nil
	}
	if !s.Valid {
		rec.Metadata = checkpoint
		applyActivityMetadata(&rec.Metadata, s)
		rec.UpdatedAt = now
		_, retry, err := project(rec)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		if retry {
			goto retryProjection
		}
		m.mu.Unlock()
		if nativeSessionChanged {
			reactivateSessionUsage(ctx, id, rec.Metadata.RuntimeLaunchID, usageReactivator)
		}
		return nil
	}
	if metadataChanged {
		rec.Metadata = checkpoint
		applyActivityMetadata(&rec.Metadata, s)
	}
	prevState := rec.Activity.State
	prevAt := rec.Activity.LastActivityAt
	act := domain.Activity{State: s.State, LastActivityAt: timeOr(s.Timestamp, now)}
	sameState := sameActivity(rec.Activity, act)
	completedIdleTurn := s.State == domain.ActivityIdle && s.Event == "post-agent" &&
		s.LaunchID != "" && s.LaunchID == rec.Metadata.RuntimeLaunchID &&
		s.AgentSessionID != "" && s.AgentSessionID == rec.Metadata.AgentSessionID &&
		act.LastActivityAt.After(rec.Activity.LastActivityAt)
	if sameState && !rec.FirstSignalAt.IsZero() && !completedIdleTurn {
		if metadataChanged || s.Event == "user-prompt-submit" {
			rec.UpdatedAt = now
			applied, retry, err := project(rec)
			if err != nil {
				m.mu.Unlock()
				return err
			}
			if retry {
				goto retryProjection
			}
			if !applied {
				m.mu.Unlock()
				return nil
			}
			m.mu.Unlock()
			if nativeSessionChanged {
				reactivateSessionUsage(ctx, id, rec.Metadata.RuntimeLaunchID, usageReactivator)
			}
			return nil
		}
		m.mu.Unlock()
		return nil
	}
	next := rec
	next.Activity = act
	if next.FirstSignalAt.IsZero() {
		next.FirstSignalAt = timeOr(s.Timestamp, now)
	}
	if s.State == domain.ActivityExited {
		delete(m.flights, id)
	}
	next.UpdatedAt = now
	applied, retry, err := project(next)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if retry {
		goto retryProjection
	}
	if !applied {
		m.mu.Unlock()
		return nil
	}
	if !rec.Activity.State.NeedsInput() && next.Activity.State.NeedsInput() && !next.IsTerminated {
		intent = &ports.NotificationIntent{
			Type:               domain.NotificationNeedsInput,
			SessionID:          next.ID,
			ProjectID:          next.ProjectID,
			CreatedAt:          next.Activity.LastActivityAt,
			SessionDisplayName: next.DisplayName,
		}
	}
	resolutions := needsInputResolutions(rec, next, now)
	waitingEvents := m.waitingInputEvents(next, prevState, prevAt, now)
	m.mu.Unlock()
	if nativeSessionChanged {
		reactivateSessionUsage(ctx, id, next.Metadata.RuntimeLaunchID, usageReactivator)
	}
	if prevState != domain.ActivityIdle && next.Activity.State == domain.ActivityIdle {
		m.operationGateMu.RLock()
		gate := m.operationGate
		m.operationGateMu.RUnlock()
		if ender, ok := gate.(interface{ SessionTurnEnded(domain.SessionRecord) }); ok {
			ender.SessionTurnEnded(next)
		}
	}
	for _, ev := range waitingEvents {
		m.emitTelemetry(ctx, ev)
	}
	m.emitNotification(ctx, intent)
	m.resolveNotifications(ctx, resolutions...)
	return nil
}

type toolFlight struct {
	inflight             map[string]string
	blockedCandidate     string
	cursorPending        map[string]int
	kimiApprovals        map[string]bool
	kimiApprovalOrder    []string
	kimiApprovalOverflow bool
}

func cloneToolFlight(flight *toolFlight) *toolFlight {
	if flight == nil {
		return nil
	}
	clone := &toolFlight{
		inflight:             make(map[string]string, len(flight.inflight)),
		blockedCandidate:     flight.blockedCandidate,
		cursorPending:        make(map[string]int, len(flight.cursorPending)),
		kimiApprovals:        make(map[string]bool, len(flight.kimiApprovals)),
		kimiApprovalOrder:    append([]string(nil), flight.kimiApprovalOrder...),
		kimiApprovalOverflow: flight.kimiApprovalOverflow,
	}
	for id, name := range flight.inflight {
		clone.inflight[id] = name
	}
	for key, count := range flight.cursorPending {
		clone.cursorPending[key] = count
	}
	for id, completed := range flight.kimiApprovals {
		clone.kimiApprovals[id] = completed
	}
	return clone
}

func (m *Manager) restoreToolFlightLocked(id domain.SessionID, snapshot *toolFlight) {
	if snapshot == nil {
		delete(m.flights, id)
		return
	}
	m.flights[id] = cloneToolFlight(snapshot)
}

const maxInflightTools = 128

func isToolUseEvent(event string) bool {
	return event == "pre-tool-use" || isPostToolUseEvent(event)
}

func isPostToolUseEvent(event string) bool {
	return event == "post-tool-use" || event == "post-tool-use-failure" || event == "post-tool-use-fail"
}

func cursorBeforeExecutionKey(s ports.ActivitySignal) (string, bool) {
	if s.ToolName == "" {
		return "", false
	}
	switch s.Event {
	case "before-shell-execution":
		return "shell\x00" + s.ToolName, true
	case "before-mcp-execution":
		return "mcp\x00" + s.ToolName, true
	default:
		return "", false
	}
}

func cursorResolvedExecutionKey(s ports.ActivitySignal) (string, bool) {
	if s.ToolName == "" {
		return "", false
	}
	switch s.Event {
	case "after-shell-execution", "cursor-shell-terminal-failure":
		return "shell\x00" + s.ToolName, true
	case "after-mcp-execution", "cursor-mcp-terminal-failure":
		return "mcp\x00" + s.ToolName, true
	default:
		return "", false
	}
}

func isTurnBoundaryEvent(event string) bool {
	return event == "user-prompt-submit" || event == "stop" || event == "stop-failure" || event == "interrupt" || event == "session-end" ||
		event == "process-exited" || event == "chat.controller.stopped" || event == "permission-resolved" ||
		event == "liveness-probe"
}

func (m *Manager) applyToolPrecedenceLocked(id domain.SessionID, harness domain.AgentHarness, cur domain.ActivityState, s ports.ActivitySignal) ports.ActivitySignal {
	if s.Event == "" {
		return s
	}
	suppressed := s
	suppressed.Valid = false

	fl := m.flights[id]
	ensure := func() *toolFlight {
		if fl == nil {
			fl = &toolFlight{inflight: map[string]string{}}
			m.flights[id] = fl
		}
		return fl
	}
	if harness == domain.HarnessKimi {
		if s.Event == "permission-request" || s.Event == "permission-resolved" {
			return ensure().applyKimiApproval(s)
		}
		if isTurnBoundaryEvent(s.Event) && fl != nil {
			fl.settleKimiApprovals()
			fl.inflight = map[string]string{}
			fl.blockedCandidate = ""
			fl.cursorPending = nil
			return s
		}
	}

	switch s.Event {
	case "pre-tool-use":
		if s.ToolUseID != "" {
			f := ensure()
			if len(f.inflight) >= maxInflightTools {
				f.inflight = map[string]string{}
			}
			f.inflight[s.ToolUseID] = s.ToolName
		}
	case "post-tool-use", "post-tool-use-failure", "post-tool-use-fail":
		if fl != nil {
			delete(fl.inflight, s.ToolUseID)
		}
	}

	switch {
	case s.State == domain.ActivityBlocked:
		if key, ok := cursorBeforeExecutionKey(s); ok {
			f := ensure()
			f.blockedCandidate = ""
			if f.cursorPending == nil {
				f.cursorPending = map[string]int{}
			}
			f.cursorPending[key]++
			return s
		}
		f := ensure()
		f.cursorPending = nil
		if s.ToolUseID != "" || s.ToolName != "" {
			f.blockedCandidate = ""
		}
		if s.ToolUseID != "" {
			if _, ok := f.inflight[s.ToolUseID]; ok {
				f.blockedCandidate = s.ToolUseID
			}
		}
		if f.blockedCandidate == "" && s.ToolName != "" {
			for useID, name := range f.inflight {
				if name != s.ToolName {
					continue
				}
				if f.blockedCandidate != "" {
					f.blockedCandidate = ""
					break
				}
				f.blockedCandidate = useID
			}
		}
		return s

	case cur == domain.ActivityBlocked:
		switch {
		case isTurnBoundaryEvent(s.Event):
			delete(m.flights, id)
			return s
		default:
			if fl != nil {
				if key, ok := cursorResolvedExecutionKey(s); ok && fl.cursorPending[key] > 0 {
					if fl.cursorPending[key] == 1 {
						delete(fl.cursorPending, key)
					} else {
						fl.cursorPending[key]--
					}
					if len(fl.cursorPending) == 0 {
						delete(m.flights, id)
						return s
					}
					return suppressed
				}
				if isPostToolUseEvent(s.Event) && fl.blockedCandidate != "" && s.ToolUseID == fl.blockedCandidate {
					fl.blockedCandidate = ""
					return s
				}
			}
			return suppressed
		}

	case cur.IsSticky() && isToolUseEvent(s.Event):
		return suppressed

	default:
		if isTurnBoundaryEvent(s.Event) {
			delete(m.flights, id)
		}
		return s
	}
}

func (m *Manager) waitingInputEvents(next domain.SessionRecord, prevState domain.ActivityState, prevAt, now time.Time) []ports.TelemetryEvent {
	if m.telemetry == nil {
		return nil
	}
	projectID := next.ProjectID
	sessionID := next.ID
	var events []ports.TelemetryEvent
	if !prevState.NeedsInput() && next.Activity.State.NeedsInput() && !next.IsTerminated {
		events = append(events, ports.TelemetryEvent{
			Name:       "ao.sessions.input_wait_started",
			Source:     "lifecycle",
			OccurredAt: now.UTC(),
			Level:      ports.TelemetryLevelInfo,
			ProjectID:  &projectID,
			SessionID:  &sessionID,
			Payload: map[string]any{
				"state": string(next.Activity.State),
			},
		})
	}
	if prevState.NeedsInput() && !next.Activity.State.NeedsInput() {
		payload := map[string]any{
			"state":     string(next.Activity.State),
			"dwell_ms":  now.Sub(prevAt).Milliseconds(),
			"exited_to": string(next.Activity.State),
		}
		events = append(events, ports.TelemetryEvent{
			Name:       "ao.sessions.input_wait_ended",
			Source:     "lifecycle",
			OccurredAt: now.UTC(),
			Level:      ports.TelemetryLevelInfo,
			ProjectID:  &projectID,
			SessionID:  &sessionID,
			Payload:    payload,
		})
	}
	return events
}

func (m *Manager) emitTelemetry(ctx context.Context, ev ports.TelemetryEvent) {
	if m.telemetry == nil {
		return
	}
	if ev.RequestID == "" {
		ev.RequestID = reqid.FromContext(ctx)
	}
	if ev.Name == "ao.sessions.input_wait_started" && ev.SessionID != nil {
		m.countWaitingInput(ctx, *ev.SessionID)
	}
	m.telemetry.Emit(ctx, ev)
}

func (m *Manager) emitNotification(ctx context.Context, intent *ports.NotificationIntent) {
	if intent == nil || m.notifications == nil {
		return
	}
	if err := m.notifications.Notify(ctx, *intent); err != nil {
		slog.Default().Warn("lifecycle: notification failed", "session", intent.SessionID, "type", intent.Type, "err", err)
	}
}

func (m *Manager) resolveNotifications(ctx context.Context, resolutions ...ports.NotificationResolution) {
	if m.notifications == nil {
		return
	}
	for _, res := range resolutions {
		if err := m.notifications.Resolve(ctx, res); err != nil {
			slog.Default().Warn(
				"lifecycle: notification resolve failed",
				"session", res.SessionID, "pr", res.PRURL, "type", res.Type, "err", err,
			)
		}
	}
}

func (m *Manager) MarkChatReconnected(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok || rec.IsTerminated || rec.Mode != domain.SessionModeChat ||
		metadata.ProviderConversationID == "" || metadata.ProviderConversationID != rec.Metadata.ProviderConversationID ||
		metadata.ControllerGeneration == "" || metadata.ControllerGeneration != rec.Metadata.ControllerGeneration {
		return fmt.Errorf("lifecycle: live Chat reconnect for %q no longer owns the session", id)
	}
	return nil
}

func (m *Manager) MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	return m.markSpawned(ctx, id, metadata, nil, nil, nil)
}

func (m *Manager) MarkChatSpawned(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	boundary domain.ConversationBranch,
) error {
	if boundary.ID == "" || boundary.ConversationID == "" || boundary.SessionID != id ||
		boundary.ProviderConversationID == "" || boundary.ProviderScopeID != boundary.ID ||
		metadata.ProviderConversationID != boundary.ProviderConversationID ||
		strings.TrimSpace(metadata.ControllerGeneration) == "" {
		return fmt.Errorf("lifecycle: Chat provider boundary for %q has incomplete or mismatched ownership", id)
	}
	return m.markSpawned(ctx, id, metadata, &boundary, nil, nil)
}

func (m *Manager) MarkChatSpawnedPrepared(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	boundary domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	if prepare == nil {
		return errors.New("lifecycle: Chat provider-history preparation is missing")
	}
	if boundary.ID == "" || boundary.ConversationID == "" || boundary.SessionID != id ||
		boundary.ProviderConversationID == "" || boundary.ProviderScopeID != boundary.ID ||
		metadata.ProviderConversationID != boundary.ProviderConversationID ||
		strings.TrimSpace(metadata.ControllerGeneration) == "" {
		return fmt.Errorf("lifecycle: Chat provider boundary for %q has incomplete or mismatched ownership", id)
	}
	return m.markSpawned(ctx, id, metadata, &boundary, handoff, prepare)
}

func (m *Manager) markSpawned(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	boundary *domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	launchID := strings.TrimSpace(metadata.RuntimeLaunchID)
	reactivator, err := func() (sessionUsageReactivator, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		defer m.finishLaunchLocked(id, launchID)
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("lifecycle: MarkSpawned for unknown session %q", id)
		}
		now := m.clock()
		if rec.IsTerminated {
			rec.CleanupGeneration++
		}
		rec.IsTerminated = false
		rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
		rec.FirstSignalAt = time.Time{}
		rec.Metadata = mergeMetadata(rec.Metadata, metadata)
		if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
			strings.TrimSpace(metadata.ControllerGeneration) != "" {
			rec.Metadata.RuntimeHandleID = ""
			rec.Metadata.RuntimeLaunchID = ""
		}
		rec.UpdatedAt = now
		if boundary == nil {
			if err := m.store.UpdateSession(ctx, rec); err != nil {
				return nil, err
			}
		} else if prepare == nil {
			writer, ok := m.store.(chatSpawnStore)
			if !ok {
				return nil, errors.New("lifecycle: atomic Chat spawn persistence is unavailable")
			}
			if err := writer.CommitChatSpawn(ctx, rec, *boundary); err != nil {
				return nil, err
			}
		} else {
			writer, ok := m.store.(preparedChatSpawnStore)
			if !ok {
				return nil, errors.New("lifecycle: atomic Chat provider-history persistence is unavailable")
			}
			if err := writer.CommitChatSpawnPrepared(ctx, rec, *boundary, handoff, prepare); err != nil {
				return nil, err
			}
		}
		if rec.Harness == domain.HarnessKimi {
			delete(m.flights, id)
		}
		return m.usageReactivator, nil
	}()
	if err != nil {
		return err
	}
	reactivateSessionUsage(ctx, id, launchID, reactivator)
	return nil
}

func (m *Manager) CommitControllerEpoch(
	ctx context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	startFresh bool,
) (bool, error) {
	return m.changeControllerEpoch(
		ctx, id, source, target, nativeConversationID, startFresh, false, false,
	)
}

func (m *Manager) CommitControllerEpochArchivedChat(
	ctx context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	startFresh bool,
) (bool, error) {
	return m.changeControllerEpoch(
		ctx, id, source, target, nativeConversationID, startFresh, false, true,
	)
}

func (m *Manager) RestoreControllerEpoch(
	ctx context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	startFresh bool,
) (bool, error) {
	return m.changeControllerEpoch(
		ctx, id, source, target, nativeConversationID, startFresh, true, false,
	)
}

func (m *Manager) changeControllerEpoch(
	ctx context.Context,
	id domain.SessionID,
	source, target domain.SessionMode,
	nativeConversationID string,
	startFresh, restore, archivedChat bool,
) (bool, error) {
	if !source.Valid() || !target.Valid() || source == target {
		return false, fmt.Errorf("lifecycle: invalid controller epoch %q -> %q", source, target)
	}
	nativeConversationID = strings.TrimSpace(nativeConversationID)
	if nativeConversationID == "" && !startFresh {
		return false, fmt.Errorf("lifecycle: controller epoch for %q has no native conversation id", id)
	}
	if nativeConversationID != "" && startFresh {
		return false, fmt.Errorf("lifecycle: fresh controller epoch for %q also supplied a native conversation id", id)
	}
	writer, ok := m.store.(controllerEpochStore)
	if !ok {
		return false, fmt.Errorf("lifecycle: controller epoch persistence is unavailable")
	}

	m.mu.Lock()
	previous, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		m.mu.Unlock()
		return false, err
	}
	if !found {
		m.mu.Unlock()
		return false, fmt.Errorf("%w: %s", ports.ErrSessionNotFound, id)
	}
	if previous.IsTerminated || domain.NormalizeSessionMode(previous.Mode) != source {
		m.mu.Unlock()
		return false, nil
	}
	now := m.clock()
	var changed bool
	if restore {
		changed, err = writer.RestoreSessionControllerEpoch(
			ctx, id, source, target, nativeConversationID, now,
		)
	} else if archivedChat {
		changed, err = writer.CommitSessionControllerEpochArchivedChat(
			ctx, id, source, target, nativeConversationID, now,
		)
	} else {
		changed, err = writer.CommitSessionControllerEpoch(
			ctx, id, source, target, nativeConversationID, now,
		)
	}
	if err != nil || !changed {
		m.mu.Unlock()
		return changed, err
	}

	next := previous
	next.Mode = target
	next.Metadata.RuntimeHandleID = ""
	next.Metadata.RuntimeLaunchID = ""
	next.Metadata.AgentSessionID = nativeConversationID
	next.Metadata.AgentSessionIDLaunchID = ""
	next.Metadata.ProviderConversationID = nativeConversationID
	next.Metadata.ControllerGeneration = ""
	if !restore && target == domain.SessionModeTUI {
		next.Metadata.LatestUserPrompt = ""
		next.Metadata.LatestAssistantUpdate = ""
		next.Metadata.ConversationCheckpointState = domain.ConversationCheckpointEmpty
		next.Metadata.ConversationCheckpointGeneration = ""
		next.Metadata.ConversationCheckpointNativeID = ""
		next.Metadata.ConversationCheckpointTurnID = ""
		next.Metadata.ConversationCheckpointUnsettled = false
	}
	next.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	next.UpdatedAt = now
	delete(m.flights, id)
	delete(m.steps, id)
	resolutions := needsInputResolutions(previous, next, now)
	waitingEvents := m.waitingInputEvents(
		next, previous.Activity.State, previous.Activity.LastActivityAt, now,
	)
	m.mu.Unlock()

	for _, ev := range waitingEvents {
		m.emitTelemetry(ctx, ev)
	}
	m.resolveNotifications(ctx, resolutions...)
	return true, nil
}

func (m *Manager) MarkTerminated(ctx context.Context, id domain.SessionID) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil || !ok {
			return err
		}
		if rec.IsTerminated {
			m.reapSessionContainers(ctx, id)
			return nil
		}

		launchID := rec.Metadata.RuntimeLaunchID
		sessionRevision := rec.Revision
		m.mu.Lock()
		finalizer := m.usageFinalizer
		m.mu.Unlock()
		finalizeSessionUsage(ctx, id, launchID, sessionRevision, finalizer)

		const (
			terminationChanged = iota
			terminationApplied
			terminationAlreadyApplied
			terminationLaunchChanged
		)
		outcome := terminationChanged
		err = m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
			switch {
			case cur.IsTerminated:
				outcome = terminationAlreadyApplied
				return cur, false
			case cur.Metadata.RuntimeLaunchID != launchID:
				outcome = terminationLaunchChanged
				return cur, false
			case cur.Revision != sessionRevision:
				return cur, false
			default:
				cur.IsTerminated = true
				cur.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: now}
				delete(m.flights, id)
				delete(m.steps, id)
				outcome = terminationApplied
				return cur, true
			}
		})
		if err != nil {
			return err
		}
		switch outcome {
		case terminationApplied, terminationAlreadyApplied:
			m.reapSessionContainers(ctx, id)
			return nil
		case terminationLaunchChanged:
			return fmt.Errorf("lifecycle: runtime launch changed while terminating session %q", id)
		default:
			continue
		}
	}
}

func (m *Manager) reapSessionContainers(ctx context.Context, id domain.SessionID) {
	if m.containers == nil {
		return
	}
	if m.projects != nil {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil || !ok {
			slog.Default().Warn("lifecycle: container reap: session lookup failed, skipping", "session", id, "err", err)
			return
		}
		if !rec.IsStandalone() {
			project, ok, err := m.projects.GetProject(ctx, string(rec.ProjectID))
			if err != nil || !ok {
				slog.Default().Warn("lifecycle: container reap: project lookup failed or missing, skipping rather than guessing", "session", id, "project", rec.ProjectID, "err", err)
				return
			}
			if project.Config.ContainerReap.Disabled {
				return
			}
		}
	}
	removed, err := m.containers.ReapSessionContainers(ctx, id)
	if err != nil {
		slog.Default().Warn("lifecycle: container reap failed", "session", id, "err", err)
		return
	}
	if removed > 0 {
		slog.Default().Info("lifecycle: reaped session containers", "session", id, "removed", removed)
	}
}

func finalizeSessionUsage(
	ctx context.Context,
	id domain.SessionID,
	expectedRuntimeLaunchID string,
	expectedSessionRevision int64,
	finalizer sessionUsageFinalizer,
) {
	if finalizer == nil {
		return
	}
	if err := finalizer.FinalizeSession(ctx, id, expectedRuntimeLaunchID, expectedSessionRevision); err != nil {
		slog.Default().Warn("lifecycle: finalize session usage before termination", "session", id, "err", err)
	}
}

func reactivateSessionUsage(
	ctx context.Context,
	id domain.SessionID,
	expectedRuntimeLaunchID string,
	reactivator sessionUsageReactivator,
) {
	if reactivator == nil {
		return
	}
	if err := reactivator.ReactivateSession(ctx, id, expectedRuntimeLaunchID); err != nil {
		slog.Default().Warn("lifecycle: reactivate session usage after launch", "session", id, "err", err)
	}
}

func sameActivity(a, b domain.Activity) bool {
	return a.State == b.State
}

func mergeMetadata(base, in domain.SessionMetadata) domain.SessionMetadata {
	if in.ImportSource != nil {
		source := *in.ImportSource
		base.ImportSource = &source
	}
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&base.Branch, in.Branch)
	set(&base.WorkspacePath, in.WorkspacePath)
	set(&base.WorkspaceRepoPath, in.WorkspaceRepoPath)
	set(&base.DiffBaseSHA, in.DiffBaseSHA)
	set(&base.DiffBaseRef, in.DiffBaseRef)
	set(&base.RuntimeHandleID, in.RuntimeHandleID)
	base.RuntimeLaunchID = in.RuntimeLaunchID
	set(&base.AgentSessionID, in.AgentSessionID)
	set(&base.AgentSessionIDLaunchID, in.AgentSessionIDLaunchID)
	set(&base.Prompt, in.Prompt)
	set(&base.LatestUserPrompt, in.LatestUserPrompt)
	if !in.LatestUserPromptAt.IsZero() {
		base.LatestUserPromptAt = in.LatestUserPromptAt
	}
	if !in.LatestAssistantUpdateAt.IsZero() {
		base.LatestAssistantUpdateAt = in.LatestAssistantUpdateAt
	}
	if !in.NativeIdentityObservedAt.IsZero() {
		base.NativeIdentityObservedAt = in.NativeIdentityObservedAt
	}
	set(&base.LatestAssistantUpdate, in.LatestAssistantUpdate)
	set(&base.NativeTranscriptPath, in.NativeTranscriptPath)
	set(&base.Model, in.Model)
	set(&base.Effort, in.Effort)
	set(&base.BrowserCapabilityVerifier, in.BrowserCapabilityVerifier)
	set(&base.ProviderConversationID, in.ProviderConversationID)
	base.ControllerGeneration = in.ControllerGeneration
	return base
}

func applyActivityMetadata(meta *domain.SessionMetadata, signal ports.ActivitySignal) {
	if signal.AgentSessionID != "" {
		meta.AgentSessionID = signal.AgentSessionID
		meta.AgentSessionIDLaunchID = signal.LaunchID
		if signal.Timestamp.After(meta.NativeIdentityObservedAt) {
			meta.NativeIdentityObservedAt = signal.Timestamp
		}
	}
	if signal.TranscriptPath != "" {
		meta.NativeTranscriptPath = signal.TranscriptPath
	}
}
