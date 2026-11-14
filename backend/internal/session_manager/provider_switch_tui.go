package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func (m *Manager) SwitchTUIProvider(ctx context.Context, id domain.SessionID, request domain.ProviderSwitchRequest) error {
	target := request.TargetHarness
	if err := m.beginAgentOperation(ctx, id, agentOperationProviderSwitch); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "another session operation is in progress", Cause: err}
		}
		return err
	}
	defer m.endAgentOperation(id, agentOperationProviderSwitch)

	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: "provider switching for this session is handled by the Chat controller"}
	}
	if rec.IsTerminated {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "terminated sessions must be restored before switching"}
	}
	if rec.Harness == target {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: "target provider is already active"}
	}
	if rec.NeedsImportResume() {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "open the imported chat before switching providers"}
	}
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return err
	} else if active {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "an interface switch is already in progress"}
	}
	targetAgent, ok := m.agents.Agent(target)
	if !ok || targetAgent == nil {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: fmt.Sprintf("%s is not available for Terminal UI", target)}
	}
	if rec.Activity.State == domain.ActivityActive {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "the provider is still working"}
	}
	if rec.Activity.State == domain.ActivityWaitingInput || rec.Activity.State == domain.ActivityBlocked {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalPendingInteraction, Detail: "answer the pending provider interaction before switching"}
	}

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	ws := workspaceInfo(rec)
	if err := preflightTUISwitchTarget(ctx, target, targetAgent, m.dataDir); err != nil {
		return err
	}

	lastInputAt, releaseInput := m.beginTerminalInputDrain(rec)
	if releaseInput == nil {
		releaseInput = func() {}
	}
	defer releaseInput()
	if err := m.prepareSourceHandoff(ctx, rec, domain.SessionInterfaceTransitionDrain, lastInputAt); err != nil {
		kind := domain.ProviderSwitchRefusalBusy
		if errors.Is(err, errDrainDecisionPending) {
			kind = domain.ProviderSwitchRefusalPendingInteraction
		}
		return &domain.ProviderSwitchError{Kind: kind, Detail: err.Error(), Cause: err}
	}
	if err := m.stopSourceControllerConclusive(rec); err != nil {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "Orchestrator could not confirm that the source provider stopped: " + err.Error(), Cause: err}
	}
	releaseInput()
	ctx, cancelSwitch := context.WithTimeout(context.WithoutCancel(ctx), tuiSwitchAfterStopLimit)
	defer cancelSwitch()
	keepUserEdits := func(ctx context.Context, rec *domain.SessionRecord) {
		if current, found, err := m.store.GetSession(ctx, id); err == nil && found {
			rec.DisplayName, rec.IsPinned, rec.PinnedAt = current.DisplayName, current.IsPinned, current.PinnedAt
			rec.Metadata.PreviewURL, rec.Metadata.PreviewRevision = current.Metadata.PreviewURL, current.Metadata.PreviewRevision
		}
	}
	keepUserEdits(ctx, &rec)

	source := rec
	source.Metadata.RuntimeHandleID, source.Metadata.RuntimeLaunchID, source.Metadata.AgentSessionIDLaunchID = "", "", ""
	source.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: m.clock()}
	source.UpdatedAt = m.clock()

	launchStore, _ := m.store.(launchOptionsStore)
	sourceLaunch := m.sessionLaunchOptions(ctx, id)
	targetLaunch := domain.AgentLaunchOptions{
		FastMode: request.FastMode, FastModeOff: !request.FastMode && target == domain.HarnessCodex,
		Ultracode: request.Ultracode, AutoCompactWindow: request.AutoCompactWindow,
	}
	restoreSource := func(cause error, what string) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*interfaceTransitionStepLimit)
		defer cancel()
		source := source
		keepUserEdits(ctx, &source)
		var restoreErr error
		if launchStore != nil {
			restoreErr = launchStore.UpdateSessionWithLaunchOptions(ctx, source, sourceLaunch)
		} else {
			restoreErr = m.store.UpdateSession(ctx, source)
		}
		if restoreErr == nil {
			_, restoreErr = m.relaunchSessionWithPolicy(ctx, "restore source provider", source, project, ws, nil, false, false, domain.SessionInterfaceTransitionHistoryStrict)
		}
		if restoreErr != nil {
			source.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: m.clock()}
			_ = m.store.UpdateSession(ctx, source)
			return &domain.ProviderSwitchError{
				Kind:   domain.ProviderSwitchRefusalRecoveryRequired,
				Detail: fmt.Sprintf("%s and source restoration also failed: %v; %v", what, cause, restoreErr),
				Cause:  errors.Join(cause, restoreErr),
			}
		}
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnavailable, Detail: what + ": " + cause.Error(), Cause: cause}
	}

	targetRecord := source
	targetRecord.Harness = target
	md := &targetRecord.Metadata
	md.AgentSessionID, md.NativeTranscriptPath, md.NativeIdentityObservedAt = "", "", time.Time{}
	md.Model, md.Effort, md.LatestUserPrompt, md.LatestAssistantUpdate = request.Model, request.Effort, "", ""
	md.ConversationCheckpointState, md.ConversationCheckpointGeneration = domain.ConversationCheckpointEmpty, ""
	md.ConversationCheckpointNativeID, md.ConversationCheckpointTurnID, md.ConversationCheckpointUnsettled = "", "", false
	targetRecord.UpdatedAt = m.clock()
	targetLaunch.HandoffNote = request.HandoffNote
	publish := func() error { return m.store.UpdateSession(ctx, targetRecord) }
	if launchStore != nil {
		publish = func() error { return launchStore.UpdateSessionWithLaunchOptions(ctx, targetRecord, targetLaunch) }
	}
	if err := publish(); err != nil {
		return restoreSource(err, "could not publish target provider ownership")
	}

	if preparer, ok := targetAgent.(interface {
		PrepareSwitchTarget(context.Context, string)
	}); ok {
		preparer.PrepareSwitchTarget(ctx, ws.Path)
	}
	started, err := m.relaunchSessionWithPolicyAndGeneration(ctx, "switch TUI provider", targetRecord, project, ws, nil,
		true, false, "", domain.SessionInterfaceTransitionHistoryStrict, &request.HandoffNote)
	if err != nil {
		return restoreSource(err, "target provider failed to start")
	}
	if err := m.confirmTUISwitchTargetAlive(ctx, started.Session); err != nil {
		m.destroySpawnRuntimeAfterFailure(ctx, runtimeHandle(started.Session.Metadata))
		return restoreSource(err, "target provider did not stay running")
	}
	m.adoptTUISwitchSettings(ctx, id, request)
	m.clearHandoffNote(ctx, id)
	return nil
}

func (m *Manager) clearHandoffNote(ctx context.Context, id domain.SessionID) {
	launchStore, ok := m.store.(launchOptionsStore)
	if !ok {
		return
	}
	if rec, found, err := m.store.GetSession(ctx, id); err != nil || !found || rec.Metadata.AgentSessionID == "" {
		return
	}
	options, err := launchStore.SessionLaunchOptions(ctx, id)
	if err != nil || options.HandoffNote == "" {
		return
	}
	options.HandoffNote = ""
	if err := launchStore.SetSessionLaunchOptions(ctx, id, options); err != nil {
		m.logger.Warn("clear terminal switch handoff note", "session", id, "error", err)
	}
}

func (m *Manager) adoptTUISwitchSettings(ctx context.Context, id domain.SessionID, request domain.ProviderSwitchRequest) {
	store, ok := m.store.(interface {
		ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
		SetConversationSettings(context.Context, string, domain.ConversationSettings, time.Time) error
	})
	if !ok {
		return
	}
	conversation, err := store.ConversationForSession(ctx, id)
	if err != nil {
		return
	}
	settings := conversation.Settings
	settings.Model, settings.ReasoningEffort = request.Model, request.Effort
	settings.AutoCompactWindow = ""
	if request.TargetHarness == domain.HarnessClaudeCode {
		settings.AutoCompactWindow = request.AutoCompactWindow
	}
	if err := store.SetConversationSettings(ctx, conversation.ID, settings, m.clock()); err != nil {
		m.logger.Warn("adopt terminal switch settings", "session", id, "error", err)
	}
}

var (
	tuiSwitchAfterStopLimit   = 3 * interfaceTransitionStepLimit
	tuiSwitchTargetSettle     = 1500 * time.Millisecond
	tuiSwitchTargetSettlePoll = 250 * time.Millisecond
)

func (m *Manager) confirmTUISwitchTargetAlive(ctx context.Context, rec domain.SessionRecord) error {
	inspector, ok := m.runtime.(ports.RuntimeChildInspector)
	handle := runtimeHandle(rec.Metadata)
	if !ok || handle.ID == "" {
		return nil
	}
	deadline := time.Now().Add(tuiSwitchTargetSettle)
	for {
		if alive, err := inspector.IsChildAlive(ctx, handle); err == nil && !alive {
			reason := "it exited as soon as it started"
			if output, outputErr := m.runtime.GetOutput(ctx, handle, 8); outputErr == nil {
				lines := strings.Split(strings.TrimSpace(domain.SanitizeControlChars(output)), "\n")
				const limit = 240
				if last := []rune(strings.TrimSpace(lines[len(lines)-1])); len(last) > limit {
					reason += ": " + string(last[:limit]) + "…"
				} else if len(last) > 0 {
					reason += ": " + string(last)
				}
			}
			return errors.New(reason)
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(tuiSwitchTargetSettlePoll)
	}
}

func preflightTUISwitchTarget(ctx context.Context, harness domain.AgentHarness, target ports.Agent, dataDir string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if resolver, ok := target.(ports.AgentBinaryResolver); ok {
		if _, err := resolver.ResolveBinary(ctx); errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnavailable, Detail: fmt.Sprintf("%s is not installed", harness), Cause: err}
		}
	}
	status := ports.AgentAuthStatusUnknown
	var err error
	if runtimeChecker, ok := target.(ports.AgentRuntimeAuthChecker); ok && dataDir != "" {
		probeCtx := ports.WithAuthEnvironment(ctx, providerkeys.Withheld(dataDir, string(harness)))
		status, _, _, err = runtimeChecker.AuthStatusForRuntime(probeCtx, dataDir)
	} else if checker, ok := target.(ports.AgentAuthChecker); ok {
		status, err = checker.AuthStatus(ctx)
	}
	if err == nil && status == ports.AgentAuthStatusUnauthorized {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnavailable, Detail: fmt.Sprintf("%s is not signed in", harness), Cause: ports.ErrAgentAuthRequired}
	}
	return nil
}
