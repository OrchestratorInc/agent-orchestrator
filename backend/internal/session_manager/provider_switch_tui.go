package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SwitchTUIProvider replaces one terminal provider with another in the same AO
// session and terminal. The target starts a fresh native thread seeded with the
// handoff note: native histories cannot be merged across harnesses.
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

	// Everything the relaunch needs is read before the source is stopped, so a
	// failed lookup cannot leave the session without a provider.
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	ws := workspaceInfo(rec)
	if err := preflightTUISwitchTarget(ctx, target, targetAgent); err != nil {
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
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "AO could not confirm that the source provider stopped: " + err.Error(), Cause: err}
	}
	// Held input has nothing left to reach, and the target's startup queries
	// on the same terminal must get through.
	releaseInput()
	// The request's deadline must not cut the switch short from here: a store
	// write failing half way would leave the session with no provider at all.
	ctx, cancelSwitch := context.WithTimeout(context.WithoutCancel(ctx), tuiSwitchAfterStopLimit)
	defer cancelSwitch()
	// The rows written from here are whole. A rename, pin or preview change made
	// while the source stopped is the user's, so it is kept.
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

	// Any failure from here brings the source back on its old native thread, so
	// the user is never left with a blank terminal. The target's launch choices
	// are saved before it starts, and the source's put back if it is restored.
	launchStore, _ := m.store.(launchOptionsStore)
	sourceLaunch := m.sessionLaunchOptions(ctx, id)
	// The picker shows Codex's fast mode off unless it was turned on, so that is how it starts.
	targetLaunch := domain.AgentLaunchOptions{
		FastMode: request.FastMode, FastModeOff: !request.FastMode && target == domain.HarnessCodex,
		Ultracode: request.Ultracode, AutoCompactWindow: request.AutoCompactWindow,
	}
	restoreSource := func(cause error, what string) error {
		// A target that used up the switch's time still leaves the restore its own.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*interfaceTransitionStepLimit)
		defer cancel()
		// So does one made while the target was starting.
		source := source
		keepUserEdits(ctx, &source)
		// Ownership and the source's launch choices go back in one write, as they
		// moved: a stop between two writes would leave the target owning the
		// session with neither its handoff nor the source's native identity.
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
			// No agent runs now, so the session must not read as idle.
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
	// The note stays saved with the target's choices until it has started, so a
	// relaunch before then (the daemon stopped) hands over again instead of
	// replaying the task. Both move with ownership in one write.
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
		// The target's terminal outlives it to keep its last output; it must go
		// before the source can have the session's terminal back.
		m.destroySpawnRuntimeAfterFailure(ctx, runtimeHandle(started.Session.Metadata))
		return restoreSource(err, "target provider did not stay running")
	}
	m.adoptTUISwitchSettings(ctx, id, request)
	m.clearHandoffNote(ctx, id)
	return nil
}

// clearHandoffNote drops a switch target's saved note once the target has a
// native thread to resume: a later restore must not start it over from the
// switch. A target still alive is not enough. Until its first native hook
// arrives, a restore would start it fresh, and the note is what keeps that
// start from replaying the session's original task. A restore resuming a
// native thread ignores the note.
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

// adoptTUISwitchSettings gives the chat, if the store keeps one, the target's
// model and effort: the saved ones belong to the outgoing agent, and the chat
// would start the target on a model it does not have.
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

// Variables so tests do not have to wait. tuiSwitchAfterStopLimit bounds all
// work after the source stops; the settle values say how long a fresh target
// is watched before the switch is reported as done.
var (
	tuiSwitchAfterStopLimit   = 3 * interfaceTransitionStepLimit
	tuiSwitchTargetSettle     = 1500 * time.Millisecond
	tuiSwitchTargetSettlePoll = 250 * time.Millisecond
)

// confirmTUISwitchTargetAlive catches a target that exits straight away (a
// refused flag or session id, a launcher that gives up), which would otherwise
// be a successful switch over a dead terminal. The error carries the agent's
// last line, usually its reason. A runtime that cannot answer does not fail it.
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

// preflightTUISwitchTarget refuses a target with no binary or sign-in while the
// source still runs, so the user's agent is not stopped for nothing. A probe
// that cannot answer does not block: the launch itself still validates.
func preflightTUISwitchTarget(ctx context.Context, harness domain.AgentHarness, target ports.Agent) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if resolver, ok := target.(ports.AgentBinaryResolver); ok {
		if _, err := resolver.ResolveBinary(ctx); errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnavailable, Detail: fmt.Sprintf("%s is not installed", harness), Cause: err}
		}
	}
	if checker, ok := target.(ports.AgentAuthChecker); ok {
		if status, err := checker.AuthStatus(ctx); err == nil && status == ports.AgentAuthStatusUnauthorized {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnavailable, Detail: fmt.Sprintf("%s is not signed in", harness), Cause: ports.ErrAgentAuthRequired}
		}
	}
	return nil
}
