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
// session and terminal surface. The target always starts a fresh native thread
// seeded with bounded AO handoff text; provider-native histories cannot be
// merged across harnesses safely.
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
	if releaseInput != nil {
		defer releaseInput()
	}
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
	// The source is gone, so held input has nothing left to reach. The target
	// starts on the same terminal, and its own startup queries must get through.
	if releaseInput != nil {
		releaseInput()
	}
	// From here on the request's deadline must not cut the switch short: a store
	// write failing half way would leave the session with no provider at all.
	ctx, cancelSwitch := context.WithTimeout(context.WithoutCancel(ctx), tuiSwitchAfterStopLimit)
	defer cancelSwitch()

	source := rec
	source.Metadata.RuntimeHandleID = ""
	source.Metadata.RuntimeLaunchID = ""
	source.Metadata.AgentSessionIDLaunchID = ""
	source.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: m.clock()}
	source.UpdatedAt = m.clock()

	// From here the source is stopped. Any failure brings it back on its old
	// native thread, so the user is never left with a blank terminal.
	// The target's launch choices replace the source's. They are saved before
	// the target starts, and put back if the source has to be restored.
	launchStore, _ := m.store.(launchOptionsStore)
	sourceLaunch := m.sessionLaunchOptions(ctx, id)
	restoreSource := func(cause error, what string) error {
		// A target that used up the switch's time still leaves the restore its own.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*interfaceTransitionStepLimit)
		defer cancel()
		if launchStore != nil {
			_ = launchStore.SetSessionLaunchOptions(ctx, id, sourceLaunch)
		}
		restoreErr := m.store.UpdateSession(ctx, source)
		if restoreErr == nil {
			_, restoreErr = m.relaunchSessionWithPolicy(ctx, "restore source provider", source, project, ws, nil, false, false, domain.SessionInterfaceTransitionHistoryStrict)
		}
		if restoreErr != nil {
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
	targetRecord.Metadata.AgentSessionID = ""
	targetRecord.Metadata.NativeTranscriptPath = ""
	targetRecord.Metadata.NativeIdentityObservedAt = time.Time{}
	targetRecord.Metadata.Model = request.Model
	targetRecord.Metadata.Effort = request.Effort
	targetRecord.Metadata.LatestUserPrompt = ""
	targetRecord.Metadata.LatestAssistantUpdate = ""
	targetRecord.Metadata.ConversationCheckpointState = domain.ConversationCheckpointEmpty
	targetRecord.Metadata.ConversationCheckpointGeneration = ""
	targetRecord.Metadata.ConversationCheckpointNativeID = ""
	targetRecord.Metadata.ConversationCheckpointTurnID = ""
	targetRecord.Metadata.ConversationCheckpointUnsettled = false
	targetRecord.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, targetRecord); err != nil {
		return restoreSource(err, "could not publish target provider ownership")
	}
	if launchStore != nil {
		// The picker shows Codex's fast mode off unless it was turned on, so that is how it starts.
		targetLaunch := domain.AgentLaunchOptions{
			FastMode: request.FastMode, FastModeOff: !request.FastMode && target == domain.HarnessCodex,
			Ultracode: request.Ultracode, AutoCompactWindow: request.AutoCompactWindow,
		}
		if err := launchStore.SetSessionLaunchOptions(ctx, id, targetLaunch); err != nil {
			return restoreSource(err, "could not save the target's launch options")
		}
	}

	started, err := m.relaunchSessionWithPolicyAndGeneration(ctx, "switch TUI provider", targetRecord, project, ws, nil,
		true, false, "", domain.SessionInterfaceTransitionHistoryStrict, &request.HandoffNote)
	if err != nil {
		return restoreSource(err, "target provider failed to start")
	}
	if err := m.confirmTUISwitchTargetAlive(ctx, started.Session); err != nil {
		// The target's terminal outlives it to keep its last output. It has to
		// go before the source can have the session's terminal back.
		m.destroySpawnRuntimeAfterFailure(ctx, runtimeHandle(started.Session.Metadata))
		return restoreSource(err, "target provider did not stay running")
	}
	m.adoptTUISwitchSettings(ctx, id, request)
	return nil
}

// tuiSwitchSettingsStore is the part of a store that keeps the chat's saved
// settings. It is optional, as for a session that has never had a chat.
type tuiSwitchSettingsStore interface {
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	SetConversationSettings(context.Context, string, domain.ConversationSettings, time.Time) error
}

// adoptTUISwitchSettings gives the chat the target's model and effort. The saved
// ones belong to the outgoing agent, and the chat would start the target on a
// model it does not have.
func (m *Manager) adoptTUISwitchSettings(ctx context.Context, id domain.SessionID, request domain.ProviderSwitchRequest) {
	store, ok := m.store.(tuiSwitchSettingsStore)
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

// How long a freshly started target is watched before the switch is reported
// as done. Variables so tests do not have to wait.
var (
	// tuiSwitchAfterStopLimit bounds everything after the source stops: starting
	// the target, and restoring the source if the target fails.
	tuiSwitchAfterStopLimit   = 3 * interfaceTransitionStepLimit
	tuiSwitchTargetSettle     = 1500 * time.Millisecond
	tuiSwitchTargetSettlePoll = 250 * time.Millisecond
)

// confirmTUISwitchTargetAlive watches the target for a moment after it starts.
// An agent that exits straight away (a refused flag or session id, a launcher
// that gives up) would otherwise be reported as a successful switch over a dead
// terminal. The error carries the last line the agent printed, which is usually
// its reason. A runtime that cannot answer does not fail the switch.
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

// preflightTUISwitchTarget refuses a switch whose target cannot launch while
// the current agent is still running. Without it the target's missing binary or
// sign-in is discovered only after the source was stopped, and the user's agent
// is restarted for nothing. A probe that cannot answer does not block: the
// launch itself still validates.
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
