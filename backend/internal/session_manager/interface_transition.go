package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	interfaceTransitionPoll               = 150 * time.Millisecond
	interfaceTransitionIdleSettle         = 750 * time.Millisecond
	interfaceTransitionSurfaceIdleSamples = 3
	interfaceTransitionStaleIdleLimit     = 5 * time.Second
	interfaceTransitionOutputLines        = 40
	interfaceInterruptSettle              = 2 * time.Second
	interfaceTransitionStopLimit          = 15 * time.Second
	interfaceTransitionStepLimit          = 45 * time.Second
	interfaceDeliveryRetry                = 2 * time.Second
	interfaceDeliveryIdlePoll             = 30 * time.Second
)

var (
	errDrainQuiescenceUnverified = errors.New("the Orchestrator daemon could not verify that the terminal was idle after the latest input. The source interface was left untouched; retry after the terminal settles")
	errDrainDecisionPending      = errors.New("the Orchestrator daemon found a provider decision waiting in Terminal. The source interface was left untouched; answer it in Terminal and retry, or choose Cancel request and switch")
)

type interfaceTransitionStore interface {
	CreateSessionInterfaceTransition(context.Context, domain.SessionInterfaceTransition) (domain.SessionInterfaceTransition, bool, error)
	CreateSessionInterfaceTransitionAfter(context.Context, domain.SessionInterfaceTransition, string) (domain.SessionInterfaceTransition, bool, bool, error)
	GetSessionInterfaceTransition(context.Context, string) (domain.SessionInterfaceTransition, bool, error)
	GetActiveSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
	GetLatestSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
	ListActiveSessionInterfaceTransitions(context.Context) ([]domain.SessionInterfaceTransition, error)
	ListDeliverableSessionInterfaceTransitions(context.Context) ([]domain.SessionInterfaceTransition, error)
	AdvanceSessionInterfaceTransition(context.Context, string, domain.SessionInterfaceTransitionPhase, domain.SessionInterfaceTransitionPhase, string, string, string, time.Time) (bool, error)
	CompleteSessionInterfaceTransition(context.Context, string, domain.SessionInterfaceTransitionPhase, string, time.Time) (bool, error)
	AcknowledgeSessionInterfaceTransitionNotice(context.Context, domain.SessionID, string, time.Time) (domain.SessionInterfaceTransition, bool, error)
	EnqueueSessionInterfaceTransitionMessage(context.Context, string, string, string, time.Time, ports.MessageDeliveryOptions) error
	ListPendingSessionInterfaceTransitionMessages(context.Context, string) ([]domain.SessionInterfaceTransitionMessage, error)
	MarkSessionInterfaceTransitionMessageDelivered(context.Context, int64, time.Time) error
}

type chatHandoffLauncher interface {
	ArmChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	PrepareChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	AbortChatHandoff(domain.SessionID)
}

type chatHandoffHistoryStore interface {
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	ConversationBranch(context.Context, string, string) (domain.ConversationBranch, error)
	HasConversationTurns(context.Context, string) (bool, error)
}

type runtimeInterrupter interface {
	Interrupt(context.Context, ports.RuntimeHandle) error
}

type interfaceTransitionRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type InterfaceTransitionStatus struct {
	Supported  bool
	TargetMode domain.SessionMode
	ReasonCode string
	Reason     string
	Transition *domain.SessionInterfaceTransition
}

func (m *Manager) InterfaceTransitionStatus(
	ctx context.Context,
	id domain.SessionID,
) (InterfaceTransitionStatus, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return InterfaceTransitionStatus{}, fmt.Errorf("interface transition status %s: %w", id, err)
	}
	if !ok {
		return InterfaceTransitionStatus{}, ErrNotFound
	}
	target := oppositeSessionMode(rec.Mode)
	status := InterfaceTransitionStatus{TargetMode: target}
	if rec.IsTerminated {
		status.ReasonCode = "SESSION_TERMINATED"
		status.Reason = "Terminated sessions must be restored before switching interfaces."
	} else if interfaceWorkspaceUnavailable(rec) {
		status.ReasonCode = "WORKSPACE_UNAVAILABLE"
		status.Reason = "This session's workspace was never set up. Retry start before switching interfaces."
	} else if rec.NeedsImportResume() {
		status.ReasonCode = "WORKSPACE_UNAVAILABLE"
		status.Reason = "Open this imported chat once before switching interfaces."
	} else if target == domain.SessionModeChat && (m.chat == nil || !m.chat.SupportsChat(rec.Harness)) {
		status.ReasonCode = "CHAT_UNSUPPORTED"
		status.Reason = fmt.Sprintf("%s does not support Chat UI.", rec.Harness)
	} else if target == domain.SessionModeChat {
		if err := m.preflightInterfaceTarget(ctx, rec, domain.SessionInterfaceTransition{TargetMode: target}); err != nil {
			status.ReasonCode = interfaceTransitionErrorCode(err)
			status.Reason = err.Error()
		}
	}
	if status.ReasonCode == "" {
		if _, err := m.handoffNativeConversationID(ctx, rec); err != nil {
			if errors.Is(err, ErrInterfaceHandoffUnsupported) {
				if target == domain.SessionModeTUI && m.hermesChatToTUIDegraded(rec) {
					status.Supported = true
				} else if target == domain.SessionModeChat && m.hermesIdentityDegraded(rec, domain.NormalizeSessionMode(rec.Mode)) {
					status.Supported = true
				} else {
					status.ReasonCode = "INTERFACE_HANDOFF_UNSUPPORTED"
				}
			} else if errors.Is(err, ErrNativeConversationMissing) {
				status.ReasonCode = "NATIVE_SESSION_MISSING"
			} else {
				status.ReasonCode = "NATIVE_SESSION_UNVERIFIED"
			}
			status.Reason = err.Error()
		} else {
			status.Supported = true
		}
	}
	if store, ok := m.store.(interfaceTransitionStore); ok {
		latest, found, err := store.GetLatestSessionInterfaceTransition(ctx, id)
		if err != nil {
			return InterfaceTransitionStatus{}, err
		}
		if found {
			status.Transition = &latest
		}
	}
	return status, nil
}

func interfaceWorkspaceUnavailable(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.WorkspacePath) == "" && !rec.ProvisionState.IsProvisioning()
}

func (m *Manager) StartInterfaceTransition(
	ctx context.Context,
	id domain.SessionID,
	target domain.SessionMode,
	policy domain.SessionInterfaceTransitionPolicy,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
) (domain.SessionInterfaceTransition, error) {
	if !target.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("target mode %q is invalid", target)
	}
	if !policy.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("transition policy %q is invalid", policy)
	}
	if !historyPolicy.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("transition history policy %q is invalid", historyPolicy)
	}
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return domain.SessionInterfaceTransition{}, ErrInterfaceHandoffUnsupported
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !found {
		return domain.SessionInterfaceTransition{}, ErrNotFound
	}
	if rec.IsTerminated {
		return domain.SessionInterfaceTransition{}, ErrTerminated
	}
	source := domain.NormalizeSessionMode(rec.Mode)
	if target == source {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: session %s is already in %s mode",
			ErrInterfaceAlreadySelected, id, source)
	}
	if interfaceWorkspaceUnavailable(rec) {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: session %s", ErrInterfaceWorkspaceUnavailable, id)
	}
	if rec.NeedsImportResume() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: imported session %s has not been opened yet", ErrInterfaceWorkspaceUnavailable, id)
	}
	nativeID, err := m.handoffNativeConversationID(ctx, rec)
	degradedChatToTUI := err != nil && errors.Is(err, ErrInterfaceHandoffUnsupported) &&
		target == domain.SessionModeTUI && m.hermesChatToTUIDegraded(rec)
	degradedTUItoChat := err != nil && errors.Is(err, ErrInterfaceHandoffUnsupported) &&
		target == domain.SessionModeChat && m.hermesIdentityDegraded(rec, source)
	if err != nil && !degradedChatToTUI && !degradedTUItoChat {
		return domain.SessionInterfaceTransition{}, err
	}
	var degradedKind string
	if degradedChatToTUI {
		if resolved, found := m.hermesNativeTerminalResumeID(ctx, rec); found {
			nativeID = resolved
		}
		degradedKind = domain.SessionInterfaceTransitionDegradedIdentityReset
	} else if degradedTUItoChat || (err == nil && nativeID == "" && m.hermesIdentityDegraded(rec, source)) {
		degradedKind = domain.SessionInterfaceTransitionDegradedIdentityReset
	}
	expectedPredecessorID := ""
	if historyPolicy == domain.SessionInterfaceTransitionHistoryProvider {
		latest, found, latestErr := store.GetLatestSessionInterfaceTransition(ctx, id)
		if latestErr != nil {
			return domain.SessionInterfaceTransition{}, latestErr
		}
		if source != domain.SessionModeTUI || target != domain.SessionModeChat ||
			!providerHistoryRecoveryAuthorized(id, nativeID, latest, found) {
			return domain.SessionInterfaceTransition{}, ErrInterfaceProviderHistoryRecoveryUnavailable
		}
		expectedPredecessorID = latest.ID
	}
	now := m.clock()
	pending := domain.SessionInterfaceTransition{
		ID: m.newLaunchID(), SessionID: id, SourceMode: source, TargetMode: target,
		Policy: policy, HistoryPolicy: historyPolicy, Phase: domain.SessionInterfaceTransitionRequested,
		NativeConversationID: nativeID, DegradedKind: degradedKind, CreatedAt: now, UpdatedAt: now,
	}
	var transition domain.SessionInterfaceTransition
	var created bool
	if expectedPredecessorID != "" {
		var predecessorMatched bool
		transition, created, predecessorMatched, err = store.CreateSessionInterfaceTransitionAfter(
			ctx, pending, expectedPredecessorID,
		)
		if err == nil && !predecessorMatched {
			return domain.SessionInterfaceTransition{}, ErrInterfaceProviderHistoryRecoveryUnavailable
		}
	} else {
		transition, created, err = store.CreateSessionInterfaceTransition(ctx, pending)
	}
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !created {
		return transition, ErrInterfaceTransitionInProgress
	}
	if err := m.launchInterfaceTransition(ctx, transition); err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	return transition, nil
}

func (m *Manager) launchInterfaceTransition(ctx context.Context, transition domain.SessionInterfaceTransition) error {
	if transition.SourceMode == domain.SessionModeChat {
		handoff, ok := m.chat.(chatHandoffLauncher)
		if !ok {
			err := ErrInterfaceHandoffUnsupported
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionFailed,
				"SOURCE_FENCE_FAILED", err.Error())
			return err
		}
		if err := handoff.ArmChatHandoff(ctx, transition.SessionID, transition.Policy); err != nil {
			finishErr := m.finishInterfaceTransition(transition.ID,
				domain.SessionInterfaceTransitionFailed, "SOURCE_FENCE_FAILED", err.Error())
			if finishErr != nil {
				return errors.Join(err, fmt.Errorf("record source fence failure: %w", finishErr))
			}
			return err
		}
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	run := &interfaceTransitionRun{cancel: cancel, done: make(chan struct{})}
	m.transitionMu.Lock()
	m.transitions[transition.SessionID] = run
	m.transitionMu.Unlock()
	go m.runInterfaceTransition(runCtx, transition, run)
	return nil
}

func providerHistoryRecoveryAuthorized(
	sessionID domain.SessionID,
	nativeConversationID string,
	latest domain.SessionInterfaceTransition,
	found bool,
) bool {
	nativeConversationID = strings.TrimSpace(nativeConversationID)
	if !found || latest.SessionID != sessionID ||
		latest.SourceMode != domain.SessionModeTUI || latest.TargetMode != domain.SessionModeChat ||
		nativeConversationID == "" ||
		strings.TrimSpace(latest.NativeConversationID) != nativeConversationID {
		return false
	}
	if latest.Phase == domain.SessionInterfaceTransitionFailed &&
		latest.ErrorCode == "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH" {
		return true
	}
	return latest.Phase == domain.SessionInterfaceTransitionRecovery &&
		latest.ErrorCode == "DAEMON_RESTARTED" &&
		latest.HistoryPolicy == domain.SessionInterfaceTransitionHistoryProvider
}

func (m *Manager) CancelInterfaceTransition(ctx context.Context, id domain.SessionID) error {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return ErrInterfaceHandoffUnsupported
	}
	transition, found, err := store.GetActiveSessionInterfaceTransition(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ErrInterfaceTransitionNotFound
	}
	switch transition.Phase {
	case domain.SessionInterfaceTransitionRequested,
		domain.SessionInterfaceTransitionPreflighting,
		domain.SessionInterfaceTransitionDraining:
	default:
		return ErrInterfaceTransitionNotCancellable
	}
	moved, err := store.AdvanceSessionInterfaceTransition(
		ctx,
		transition.ID,
		transition.Phase,
		domain.SessionInterfaceTransitionCancelled,
		transition.NativeConversationID,
		"TRANSITION_CANCELLED",
		transitionDetail(transition, "TRANSITION_CANCELLED", "The interface switch was cancelled."),
		m.clock(),
	)
	if err != nil {
		return err
	}
	if !moved {
		return ErrInterfaceTransitionNotCancellable
	}
	m.transitionMu.Lock()
	run := m.transitions[id]
	m.transitionMu.Unlock()
	if run != nil {
		run.cancel()
	}
	m.wakeTransitionMessageDispatcher()
	return nil
}

func (m *Manager) AcknowledgeInterfaceTransitionNotice(
	ctx context.Context,
	id domain.SessionID,
	transitionID string,
) (domain.SessionInterfaceTransition, error) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return domain.SessionInterfaceTransition{}, ErrInterfaceHandoffUnsupported
	}
	transition, found, err := store.GetSessionInterfaceTransition(ctx, transitionID)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !found || transition.SessionID != id {
		return domain.SessionInterfaceTransition{}, ErrInterfaceTransitionNotFound
	}
	if transition.Phase != domain.SessionInterfaceTransitionFailed &&
		transition.Phase != domain.SessionInterfaceTransitionRecovery {
		return domain.SessionInterfaceTransition{}, ErrInterfaceTransitionNoticeNotAcknowledgeable
	}
	acknowledged, changed, err := store.AcknowledgeSessionInterfaceTransitionNotice(
		ctx, id, transitionID, m.clock(),
	)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !changed {
		return domain.SessionInterfaceTransition{}, ErrInterfaceTransitionNoticeNotAcknowledgeable
	}
	return acknowledged, nil
}

func (m *Manager) runInterfaceTransition(
	ctx context.Context,
	transition domain.SessionInterfaceTransition,
	run *interfaceTransitionRun,
) {
	defer m.settleTransitionMessages(transition)
	defer close(run.done)
	defer func() {
		m.transitionMu.Lock()
		if current := m.transitions[transition.SessionID]; current == run {
			delete(m.transitions, transition.SessionID)
		}
		m.transitionMu.Unlock()
	}()

	sourcePrepared := transition.SourceMode == domain.SessionModeChat
	sourceStopped := false
	modeChanged := false
	accountStarting := false
	var sourceRuntimeHandle ports.RuntimeHandle
	fail := func(code string, cause error) {
		if sourcePrepared && !sourceStopped {
			m.abortSourceHandoff(transition)
		}
		if errors.Is(cause, context.Canceled) && !sourceStopped {
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionCancelled,
				"TRANSITION_CANCELLED", transitionDetail(transition, "TRANSITION_CANCELLED", "The interface switch was cancelled."))
			return
		}
		captureBackgroundFault(context.WithoutCancel(ctx), cause, "interface_transition", "")
		if sourceStopped {
			m.rollbackInterfaceTransition(transition, sourceRuntimeHandle, modeChanged, accountStarting, code, cause)
			return
		}
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionFailed, code,
			transitionDetail(transition, code, cause.Error()))
	}

	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionPreflighting, "", ""); err != nil {
		fail("TRANSITION_STATE_FAILED", err)
		return
	}
	rec, ok, err := m.store.GetSession(ctx, transition.SessionID)
	if err != nil || !ok {
		if err == nil {
			err = ErrNotFound
		}
		fail("SESSION_NOT_FOUND", err)
		return
	}
	if rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != transition.SourceMode {
		fail("SESSION_CHANGED", fmt.Errorf("session changed before the interface switch could start"))
		return
	}
	sourceRuntimeHandle = runtimeHandle(rec.Metadata)
	lastTerminalInputAt, releaseTerminalInput := m.beginTerminalInputDrain(rec)
	if releaseTerminalInput != nil {
		defer releaseTerminalInput()
	}
	preflight := m.preflightInterfaceTarget
	if transition.AccountMove() {
		preflight = m.preflightAccountTarget
	}
	if err := preflight(ctx, rec, transition); err != nil {
		fail(interfaceTransitionErrorCode(err), err)
		return
	}
	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionDraining, "", ""); err != nil {
		fail("TRANSITION_STATE_FAILED", err)
		return
	}
	if err := m.prepareSourceHandoff(ctx, rec, transition.Policy, lastTerminalInputAt); err != nil {
		code := "SOURCE_QUIESCE_FAILED"
		switch {
		case errors.Is(err, errDrainQuiescenceUnverified):
			code = "DRAIN_QUIESCENCE_UNVERIFIED"
		case errors.Is(err, errDrainDecisionPending):
			code = "DRAIN_DECISION_PENDING"
		}
		fail(code, err)
		return
	}
	sourcePrepared = true
	degradedIdentityReset := transition.DegradedKind == domain.SessionInterfaceTransitionDegradedIdentityReset
	if !degradedIdentityReset &&
		(transition.SourceMode == domain.SessionModeTUI || transition.NativeConversationID == "") {
		current, found, refreshErr := m.store.GetSession(ctx, rec.ID)
		if refreshErr != nil || !found {
			if refreshErr == nil {
				refreshErr = ErrNotFound
			}
			fail("SESSION_NOT_FOUND", refreshErr)
			return
		}
		if current.Metadata.RuntimeLaunchID != rec.Metadata.RuntimeLaunchID ||
			current.Metadata.ControllerGeneration != rec.Metadata.ControllerGeneration ||
			domain.NormalizeSessionMode(current.Mode) != transition.SourceMode {
			fail("SESSION_CHANGED", fmt.Errorf("session changed while the interface switch was draining"))
			return
		}
		resolvedID, handoff, refreshErr := m.nativeConversationID(ctx, current)
		if refreshErr == nil {
			resolvedID, refreshErr = m.persistedNativeConversationID(ctx, current, resolvedID, handoff)
		}
		if refreshErr != nil {
			fail(interfaceTransitionErrorCode(refreshErr), refreshErr)
			return
		}
		if transition.HistoryPolicy == domain.SessionInterfaceTransitionHistoryProvider &&
			resolvedID != transition.NativeConversationID {
			recoveryErr := fmt.Errorf("%w: native conversation changed after recovery admission",
				ErrInterfaceProviderHistoryRecoveryUnavailable)
			fail(interfaceTransitionErrorCode(recoveryErr), recoveryErr)
			return
		}
		transition.NativeConversationID = resolvedID
	}
	if err := m.moveInterfaceTransitionWithNativeID(
		transition.ID,
		domain.SessionInterfaceTransitionSourceStopping,
		transition.NativeConversationID,
		"",
		"",
	); err != nil {
		fail("TRANSITION_STATE_FAILED", err)
		return
	}
	if err := m.stopSourceControllerConclusive(rec); err != nil {
		detail := "Orchestrator could not prove that the old controller stopped. Restart Orchestrator to reconcile this session before sending more work: " + err.Error()
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
			"SOURCE_STOP_UNCERTAIN", detail)
		return
	}
	sourceStopped = true
	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionSourceStopped, "", ""); err != nil {
		fail("TRANSITION_STATE_FAILED", err)
		return
	}
	if bridge, ok := m.agents.Agent(rec.Harness); ok && transition.NativeConversationID != "" && !transition.AccountMove() {
		if storageBridge, supported := bridge.(ports.AgentInterfaceHandoffStorageBridge); supported {
			project, projectErr := m.loadProject(ctx, rec.ProjectID)
			if projectErr != nil {
				fail("HANDOFF_STORAGE_BRIDGE_FAILED", projectErr)
				return
			}
			env := m.runtimeEnvForHarness(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, rec.Harness)
			m.augmentAgentRuntimeEnv(bridge, env)
			if bridgeErr := storageBridge.PrepareInterfaceHandoffStorage(ctx, ports.SessionRef{
				ID: string(rec.ID), WorkspacePath: rec.Metadata.WorkspacePath,
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: transition.NativeConversationID},
			}, transition.SourceMode, transition.TargetMode, transition.NativeConversationID, env); bridgeErr != nil {
				fail("HANDOFF_STORAGE_BRIDGE_FAILED", bridgeErr)
				return
			}
		}
	}
	if transition.AccountMove() {
		m.setAccountOverride(rec.ID, transition.TargetAccountID)
		defer m.clearAccountOverride(rec.ID)
		accountStarting = true
	} else {
		if m.lcm == nil {
			fail("LIFECYCLE_UNAVAILABLE", fmt.Errorf("lifecycle manager is unavailable"))
			return
		}
		commitEpoch := m.lcm.CommitControllerEpoch
		if transition.DegradedKind != "" {
			commitEpoch = m.lcm.CommitControllerEpochArchivedChat
		}
		changed, err := commitEpoch(ctx, rec.ID, transition.SourceMode,
			transition.TargetMode, transition.NativeConversationID, transition.NativeConversationID == "")
		if err != nil || !changed {
			if err == nil {
				err = fmt.Errorf("session mode compare-and-swap failed")
			}
			fail("SESSION_CHANGED", err)
			return
		}
		modeChanged = true
	}
	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionTargetStarting, "", ""); err != nil {
		fail("TRANSITION_STATE_FAILED", err)
		return
	}
	replayHistory := !transition.AccountMove()
	fresh := transition.NativeConversationID == "" &&
		(modeChanged || transition.SourceMode != domain.SessionModeChat ||
			!m.chatHoldsProviderConversation(ctx, rec.ID))
	err = m.startTransitionTarget(ctx, rec.ID, fresh, replayHistory, transition.HistoryPolicy)
	if errors.Is(err, ports.ErrChatHistoryUnsettled) &&
		!errors.Is(err, ports.ErrChatRecoveryInconclusive) &&
		!errors.Is(err, ports.ErrChatHistoryLoadFailed) &&
		len(ports.ChatHistoryMismatchDimensions(err)) == 0 && transition.TargetMode == domain.SessionModeChat {
		if stopErr := m.stopTransitionTargetConclusive(ctx, transition); stopErr != nil {
			_ = m.retainUnconfirmedTransitionTarget(transition, errors.Join(err, stopErr))
			return
		}
		err = m.startTransitionTarget(ctx, rec.ID, fresh, replayHistory, transition.HistoryPolicy)
	}
	if err != nil {
		code := "TARGET_RESUME_FAILED"
		switch {
		case errors.Is(err, ports.ErrChatHistoryLoadFailed):
			code = "TARGET_HISTORY_LOAD_FAILED"
		case errors.Is(err, ports.ErrChatHistoryUnavailable):
			code = "TARGET_HISTORY_UNAVAILABLE"
		case ports.ChatHistoryMismatchOnlyUntrustedText(err):
			code = "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH"
		case errors.Is(err, ports.ErrChatHistoryUnsettled):
			code = "TARGET_HISTORY_UNSETTLED"
		}
		fail(code, err)
		return
	}
	if transition.AccountMove() {
		if err := m.agentAccounts.CommitSession(ctx, rec.ID, rec.Harness, transition.TargetAccountID); err != nil {
			fail("ACCOUNT_BIND_FAILED", err)
			return
		}
		m.clearAccountOverride(rec.ID)
		accountStarting = false
		m.markAccountSwitch(ctx, rec, transition)
	}
	if transition.SourceMode == domain.SessionModeTUI && transition.TargetMode == domain.SessionModeChat &&
		transition.NativeConversationID != "" {
		if cleaner, ok := m.store.(interface {
			DeleteTerminalTurnsForThread(context.Context, domain.SessionID, domain.AgentHarness, string) error
		}); ok {
			if err := cleaner.DeleteTerminalTurnsForThread(ctx, rec.ID, rec.Harness, transition.NativeConversationID); err != nil {
				m.logger.Warn("delete imported terminal turns", "sessionID", rec.ID, "error", err)
			}
		}
	}
	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionActivating, "", ""); err != nil {
		captureBackgroundFault(context.WithoutCancel(ctx), err, "interface_transition", "state_record")
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
			"TRANSITION_STATE_FAILED", err.Error())
		return
	}
	if err := m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionCompleted, "", ""); err != nil {
		m.logger.Error("interface transition completed but durable completion failed",
			"sessionID", rec.ID, "transitionID", transition.ID, "error", err)
		captureBackgroundFault(context.WithoutCancel(ctx), err, "interface_transition", "durable_completion")
		return
	}
	m.deliverQueuedAfterAccountMove(transition)
}

func hermesNativeConversationID(rec domain.SessionRecord) string {
	if id := rec.Metadata.ProviderConversationID; id != "" {
		return id
	}
	return rec.Metadata.AgentSessionID
}

func (m *Manager) handoffNativeConversationID(
	ctx context.Context,
	rec domain.SessionRecord,
) (string, error) {
	id, handoff, err := m.nativeConversationID(ctx, rec)
	if err != nil {
		return "", err
	}
	return m.persistedNativeConversationID(ctx, rec, id, handoff)
}

func (m *Manager) hermesIdentityDegraded(rec domain.SessionRecord, mode domain.SessionMode) bool {
	if rec.Harness != domain.HarnessHermes || mode != domain.SessionModeTUI {
		return false
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	_, declared := agent.(ports.AgentInterfaceHandoff)
	return !declared
}

const degradedHandoffMarkerText = "Chat started fresh — the terminal's native history stays in Terminal."

const (
	degradedTerminalResumeMarkerText = "Resumed your Terminal session."
	degradedTerminalFreshMarkerText  = "Terminal started fresh — the Chat conversation stays archived in Chat."
)

func degradedHandoffMarkerTextFor(transition domain.SessionInterfaceTransition) string {
	if transition.SourceMode == domain.SessionModeChat && transition.TargetMode == domain.SessionModeTUI {
		if transition.NativeConversationID != "" {
			return degradedTerminalResumeMarkerText
		}
		return degradedTerminalFreshMarkerText
	}
	return degradedHandoffMarkerText
}

func (m *Manager) hermesChatToTUIDegraded(rec domain.SessionRecord) bool {
	if rec.Harness != domain.HarnessHermes || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return false
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	_, declared := agent.(ports.AgentInterfaceHandoff)
	return !declared
}

type hermesNativeTerminalResume interface {
	PrepareNativeTerminalResume(ctx context.Context, dataDir, sessionID, nativeConversationID, workspacePath string) (found bool, err error)
}

func (m *Manager) hermesNativeTerminalResumeID(ctx context.Context, rec domain.SessionRecord) (string, bool) {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return "", false
	}
	resolver, ok := agent.(hermesNativeTerminalResume)
	if !ok {
		return "", false
	}
	nativeID := hermesNativeConversationID(rec)
	if nativeID == "" {
		return "", false
	}
	found, err := resolver.PrepareNativeTerminalResume(ctx, m.dataDir, string(rec.ID), nativeID, rec.Metadata.WorkspacePath)
	if err != nil {
		m.logger.Warn("interface transition: hermes native terminal lookup failed; degrading to a fresh TUI",
			"sessionID", rec.ID, "error", err)
		return "", false
	}
	if !found {
		return "", false
	}
	return nativeID, true
}

func (m *Manager) nativeConversationID(
	ctx context.Context,
	rec domain.SessionRecord,
) (string, ports.AgentInterfaceHandoff, error) {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return "", nil, fmt.Errorf("%w: no adapter for %s", ErrInterfaceHandoffUnsupported, rec.Harness)
	}
	handoff, ok := agent.(ports.AgentInterfaceHandoff)
	if !ok {
		return "", nil, fmt.Errorf("%w: %s has not declared compatible TUI and Chat identities",
			ErrInterfaceHandoffUnsupported, rec.Harness)
	}
	mode := domain.NormalizeSessionMode(rec.Mode)
	ref := ports.SessionRef{
		ID: string(rec.ID), WorkspacePath: rec.Metadata.WorkspacePath,
		Metadata: map[string]string{ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID},
	}
	id, ok, err := handoff.NativeConversationID(ctx, ref, mode, rec.Metadata.ProviderConversationID)
	if err != nil {
		return "", handoff, err
	}
	id = strings.TrimSpace(id)
	if !ok || id == "" {
		if m.nativeConversationNotStarted(ctx, rec, agent) {
			return "", handoff, nil
		}
		return "", handoff, fmt.Errorf("%w for %s", ErrNativeConversationMissing, rec.Harness)
	}
	if mode == domain.SessionModeTUI {
		launchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
		identityLaunchID := strings.TrimSpace(rec.Metadata.AgentSessionIDLaunchID)
		if launchID == "" || identityLaunchID != launchID {
			return "", handoff, fmt.Errorf("%w for %s", ErrNativeConversationUnverified, rec.Harness)
		}
	}
	return id, handoff, nil
}

func (m *Manager) nativeConversationNotStarted(
	ctx context.Context,
	rec domain.SessionRecord,
	agent ports.Agent,
) bool {
	if rec.Metadata.LatestUserPrompt != "" || rec.Metadata.LatestAssistantUpdate != "" ||
		!rec.Metadata.LatestUserPromptAt.IsZero() || rec.Metadata.ConversationCheckpointUnsettled {
		return false
	}
	switch rec.Metadata.ConversationCheckpointState {
	case "", domain.ConversationCheckpointLegacy, domain.ConversationCheckpointEmpty:
	default:
		return false
	}
	if rec.Metadata.ConversationCheckpointGeneration != "" || rec.Metadata.ConversationCheckpointNativeID != "" {
		return false
	}
	if path := rec.Metadata.NativeTranscriptPath; path != "" {
		if !filepath.IsAbs(path) {
			return false
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if rec.Metadata.Prompt != "" {
			return false
		}
		store, ok := m.store.(chatHandoffHistoryStore)
		if !ok {
			return false
		}
		conversation, err := store.ConversationForSession(ctx, rec.ID)
		switch {
		case errors.Is(err, domain.ErrNoConversation):
			return true
		case err != nil || conversation.SessionID != rec.ID || conversation.LatestSequence != 0:
			return false
		}
		branch, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
		if err != nil || branch.ParentBranchID != "" ||
			(branch.SessionID != rec.ID && conversation.Scope != domain.ConversationScopeProject) ||
			branch.ProviderConversationID != rec.Metadata.ProviderConversationID {
			return false
		}
		hasTurns, err := store.HasConversationTurns(ctx, conversation.ID)
		return err == nil && !hasTurns
	}
	if _, ok := agent.(ports.AgentInterfaceHandoffHistoryProbe); !ok {
		return false
	}
	return m.terminalProvesNativeConversationNotStarted(ctx, rec, agent)
}

func (m *Manager) terminalProvesNativeConversationNotStarted(
	ctx context.Context,
	rec domain.SessionRecord,
	agent ports.Agent,
) bool {
	inspector, ok := agent.(ports.TerminalSurfaceInspector)
	if !ok {
		return false
	}
	styled, ok := m.runtime.(ports.StyledTerminalOutputReader)
	if !ok || strings.TrimSpace(rec.Metadata.RuntimeHandleID) == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, interfaceInterruptSettle)
	defer cancel()
	output, err := styled.GetStyledOutput(
		probeCtx,
		ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID},
		interfaceTransitionOutputLines,
	)
	if err != nil {
		return false
	}
	return inspector.InspectTerminalSurface(output).NativeConversationNotStarted
}

func (m *Manager) persistedNativeConversationID(
	ctx context.Context,
	rec domain.SessionRecord,
	id string,
	handoff ports.AgentInterfaceHandoff,
) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", nil
	}
	probe, ok := handoff.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return id, nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return "", err
	}
	env := m.runtimeEnvForHarness(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, rec.Harness)
	exists, err := probe.NativeConversationExists(ctx, ports.SessionRef{
		ID:            string(rec.ID),
		WorkspacePath: rec.Metadata.WorkspacePath,
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID,
			ports.MetadataKeySessionMode:    string(rec.Mode),
		},
	}, id, env)
	if err != nil {
		return "", fmt.Errorf("inspect native conversation %s: %w", id, err)
	}
	if !exists {
		agent, registered := m.agents.Agent(rec.Harness)
		if registered && m.nativeConversationNotStarted(ctx, rec, agent) {
			return "", nil
		}
		return "", fmt.Errorf("%w for %s", ErrNativeConversationMissing, rec.Harness)
	}
	return id, nil
}

func (m *Manager) preflightInterfaceTarget(
	ctx context.Context,
	rec domain.SessionRecord,
	transition domain.SessionInterfaceTransition,
) error {
	if transition.TargetMode == domain.SessionModeChat {
		if m.chat == nil {
			return ports.ErrChatUnsupported
		}
		project, err := m.loadProject(ctx, rec.ProjectID)
		if err != nil {
			return err
		}
		permissions := effectiveAgentConfig(rec.Harness, rec.Kind, project.Config).Permissions
		return m.chat.PreflightChat(ctx, rec.Harness, permissions)
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return ErrUnknownHarness
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	config := restoredAgentConfig(rec, project.Config)
	env := m.runtimeEnvForHarness(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, rec.Harness)
	pinRuntimePermissionEnv(env, config.Permissions)
	m.augmentAgentRuntimeEnv(agent, env)
	if err := m.preflightLaunchAuth(ctx, agent, rec, env); err != nil {
		return err
	}
	return m.preflightTerminalLaunch(ctx, agent, rec, project, transition.NativeConversationID)
}

func (m *Manager) preflightLaunchAuth(ctx context.Context, agent ports.Agent, rec domain.SessionRecord, env map[string]string) error {
	validator, ok := agent.(ports.AgentLaunchAuthValidator)
	if !ok {
		return nil
	}
	status, authErr := validator.ValidateLaunchAuth(ctx, rec.Metadata.WorkspacePath, env)
	if authErr != nil {
		m.logger.Debug("interface transition: launch authentication probe inconclusive; continuing",
			"sessionID", rec.ID, "harness", rec.Harness, "error", authErr)
		return nil
	}
	if status == ports.AgentAuthStatusUnauthorized {
		return ports.ErrAgentAuthRequired
	}
	return nil
}

func (m *Manager) preflightTerminalLaunch(
	ctx context.Context,
	agent ports.Agent,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	nativeConversationID string,
) error {
	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, false)
	if err != nil {
		return err
	}
	config := restoredAgentConfig(rec, project.Config)
	config.Launch = m.sessionLaunchOptions(ctx, rec.ID)
	var cmd []string
	if nativeConversationID == "" {
		cmd, _, _, err = freshLaunchArgv(ctx, agent, rec.ID, rec.Metadata.WorkspacePath,
			rec.Metadata, systemPrompt, "", config, rec.Kind, m.dataDir, true, "", nil)
	} else {
		var resumable bool
		cmd, resumable, err = agent.GetRestoreCommand(ctx, ports.RestoreConfig{
			Session: ports.SessionRef{
				ID: string(rec.ID), WorkspacePath: rec.Metadata.WorkspacePath,
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeConversationID},
			},
			Kind: rec.Kind, DataDir: m.dataDir, SystemPrompt: systemPrompt,
			Config: config, Permissions: config.Permissions,
		})
		if err == nil && !resumable {
			return ErrNativeConversationMissing
		}
	}
	if err != nil {
		return err
	}
	return m.validateAgentBinary(cmd)
}

func (m *Manager) prepareSourceHandoff(
	ctx context.Context,
	rec domain.SessionRecord,
	policy domain.SessionInterfaceTransitionPolicy,
	lastTerminalInputAt time.Time,
) error {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		handoff, ok := m.chat.(chatHandoffLauncher)
		if !ok {
			return ErrInterfaceHandoffUnsupported
		}
		return handoff.PrepareChatHandoff(ctx, rec.ID, policy)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		if rec.Activity.State == domain.ActivityExited {
			return nil
		}
		return ErrIncompleteHandle
	}
	if policy == domain.SessionInterfaceTransitionInterrupt {
		if rec.Activity.State == domain.ActivityExited {
			return nil
		}
		interrupter, ok := m.runtime.(runtimeInterrupter)
		if !ok {
			return fmt.Errorf("runtime cannot interrupt a live terminal controller")
		}
		if err := interrupter.Interrupt(ctx, handle); err != nil {
			return err
		}
		initialSettle := time.NewTimer(m.interfaceTransition.pollInterval)
		select {
		case <-ctx.Done():
			initialSettle.Stop()
			return ctx.Err()
		case <-initialSettle.C:
		}
		deadline := time.NewTimer(interfaceInterruptSettle)
		defer deadline.Stop()
		ticker := time.NewTicker(interfaceTransitionPoll)
		defer ticker.Stop()
		for {
			current, found, readErr := m.store.GetSession(ctx, rec.ID)
			if readErr != nil {
				return readErr
			}
			if !found {
				return ErrNotFound
			}
			if current.Activity.State == domain.ActivityExited || tuiIdleAfterInput(current, lastTerminalInputAt) {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return nil
			case <-ticker.C:
			}
		}
	}

	var detector ports.TerminalActivityDetector
	var surfaceInspector ports.TerminalSurfaceInspector
	if agent, ok := m.agents.Agent(rec.Harness); ok {
		detector, _ = agent.(ports.TerminalActivityDetector)
		surfaceInspector, _ = agent.(ports.TerminalSurfaceInspector)
	}
	styledOutput, _ := m.runtime.(ports.StyledTerminalOutputReader)
	surfaceProofAvailable := surfaceInspector != nil && styledOutput != nil
	ticker := time.NewTicker(m.interfaceTransition.pollInterval)
	defer ticker.Stop()
	idleSince := time.Time{}
	idleSamples := 0
	unverifiedIdleSince := time.Time{}
	for {
		current, ok, err := m.store.GetSession(ctx, rec.ID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if current.Activity.State == domain.ActivityExited {
			return nil
		}
		switch current.Activity.State {
		case domain.ActivityWaitingInput, domain.ActivityBlocked:
			return errDrainDecisionPending
		}
		now := time.Now()
		durableIdleProven := tuiIdleAfterInput(current, lastTerminalInputAt)
		idleProven := durableIdleProven && !surfaceProofAvailable
		unverifiedIdle := current.Activity.State == domain.ActivityIdle && !idleProven
		probeCtx := ctx
		var cancelProbe context.CancelFunc
		if unverifiedIdle {
			if unverifiedIdleSince.IsZero() {
				unverifiedIdleSince = now
			}
			probeCtx, cancelProbe = context.WithDeadline(ctx, unverifiedIdleSince.Add(m.interfaceTransition.staleIdleLimit))
		}
		inputQuiet := lastTerminalInputAt.IsZero() ||
			!now.Before(lastTerminalInputAt.Add(m.interfaceTransition.idleSettle))
		surfaceKnownBusy := false
		if surfaceProofAvailable && inputQuiet && styledOutput != nil {
			output, outputErr := styledOutput.GetStyledOutput(probeCtx, handle, interfaceTransitionOutputLines)
			now = time.Now()
			if errors.Is(outputErr, ports.ErrStyledTerminalOutputUnavailable) {
				surfaceProofAvailable = false
				idleProven = durableIdleProven
				unverifiedIdle = current.Activity.State == domain.ActivityIdle && !idleProven
			} else if outputErr == nil {
				observation := surfaceInspector.InspectTerminalSurface(output)
				switch {
				case observation.Work == ports.TerminalSurfaceWorkWaitingInput,
					observation.Work == ports.TerminalSurfaceWorkBlocked:
					if cancelProbe != nil {
						cancelProbe()
					}
					return errDrainDecisionPending
				case current.Activity.State == domain.ActivityIdle &&
					observation.Work == ports.TerminalSurfaceWorkIdle:
					idleProven = true
				case observation.Work == ports.TerminalSurfaceWorkActive:
					surfaceKnownBusy = true
				}
			}
		}
		if !surfaceProofAvailable && unverifiedIdle && detector != nil && inputQuiet {
			if output, outputErr := m.runtime.GetOutput(probeCtx, handle, interfaceTransitionOutputLines); outputErr == nil {
				now = time.Now()
				state, authoritative := detector.DetectTerminalActivity(output)
				idleProven = authoritative && state == domain.ActivityIdle
			}
		}
		if current.Activity.State != domain.ActivityIdle || surfaceKnownBusy {
			unverifiedIdleSince = time.Time{}
			if surfaceKnownBusy && cancelProbe != nil {
				cancelProbe()
				cancelProbe = nil
				probeCtx = ctx
			}
		}
		if idleProven {
			idleSamples++
			if idleSince.IsZero() {
				idleSince = now
			} else if now.Sub(idleSince) >= m.interfaceTransition.idleSettle &&
				(!surfaceProofAvailable && durableIdleProven || idleSamples >= interfaceTransitionSurfaceIdleSamples) {
				if cancelProbe != nil {
					cancelProbe()
				}
				return nil
			}
		} else {
			idleSince = time.Time{}
			idleSamples = 0
		}
		alive, probeErr := m.runtime.IsAlive(probeCtx, handle)
		if cancelProbe != nil {
			cancelProbe()
		}
		if probeErr == nil && !alive {
			return nil
		}
		now = time.Now()
		if current.Activity.State == domain.ActivityIdle && !idleProven && !surfaceKnownBusy &&
			!unverifiedIdleSince.IsZero() && now.Sub(unverifiedIdleSince) >= m.interfaceTransition.staleIdleLimit {
			return errDrainQuiescenceUnverified
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func tuiIdleAfterInput(rec domain.SessionRecord, lastInputAt time.Time) bool {
	if rec.Activity.State != domain.ActivityIdle {
		return false
	}
	return lastInputAt.IsZero() || !rec.Activity.LastActivityAt.Before(lastInputAt)
}

func (m *Manager) abortSourceHandoff(transition domain.SessionInterfaceTransition) {
	if transition.SourceMode != domain.SessionModeChat {
		return
	}
	if handoff, ok := m.chat.(chatHandoffLauncher); ok {
		handoff.AbortChatHandoff(transition.SessionID)
	}
}

func (m *Manager) stopSourceController(ctx context.Context, rec domain.SessionRecord) error {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat == nil {
			return nil
		}
		return m.chat.StopChat(ctx, rec.ID)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return nil
	}
	return m.runtime.Destroy(ctx, handle)
}

func (m *Manager) stopSourceControllerConclusive(rec domain.SessionRecord) error {
	var failures []error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), interfaceTransitionStopLimit)
		err := m.stopSourceController(ctx, rec)
		cancel()
		if err == nil {
			return nil
		}
		failures = append(failures, err)
		if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
			probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			alive, probeErr := m.runtime.IsAlive(probeCtx, runtimeHandle(rec.Metadata))
			probeCancel()
			if probeErr == nil && !alive {
				return nil
			}
			if probeErr != nil {
				failures = append(failures, fmt.Errorf("verify terminal controller stopped: %w", probeErr))
			}
		}
	}
	return fmt.Errorf("could not prove the source controller stopped after retry: %w", errors.Join(failures...))
}

func (m *Manager) startTransitionTarget(
	ctx context.Context,
	id domain.SessionID,
	fresh, requireNativeHistory bool,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
) error {
	ctx, cancel := context.WithTimeout(ctx, interfaceTransitionStepLimit)
	defer cancel()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	ws := workspaceInfo(rec)
	var promptOverride *string
	if fresh && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI && m.chatAcceptedWork(ctx, id) {
		noPrompt := ""
		promptOverride = &noPrompt
	}
	_, err = m.relaunchSessionWithPolicyAndGeneration(
		ctx, "switch interface", rec, project, ws, nil,
		fresh, requireNativeHistory && !fresh, "", historyPolicy, promptOverride,
	)
	return err
}

func (m *Manager) chatAcceptedWork(ctx context.Context, id domain.SessionID) bool {
	store, ok := m.store.(interface {
		ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	})
	if !ok {
		return false
	}
	conversation, err := store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return false
	}
	return err != nil || conversation.LatestSequence != 0
}

func (m *Manager) rollbackInterfaceTransition(
	transition domain.SessionInterfaceTransition,
	sourceRuntimeHandle ports.RuntimeHandle,
	modeChanged, accountStarting bool,
	code string,
	cause error,
) {
	ctx, cancel := context.WithTimeout(context.Background(), interfaceTransitionStepLimit)
	defer cancel()
	if accountStarting {
		if stopErr := m.stopTransitionTargetConclusive(ctx, transition); stopErr != nil {
			_ = m.retainUnconfirmedTransitionTarget(transition, errors.Join(cause, stopErr))
			return
		}
		m.clearAccountOverride(transition.SessionID)
	}
	if modeChanged {
		if stopErr := m.stopTransitionTargetConclusive(ctx, transition); stopErr != nil {
			_ = m.retainUnconfirmedTransitionTarget(transition, errors.Join(cause, stopErr))
			return
		}
		if m.lcm == nil {
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
				"RECOVERY_REQUIRED", cause.Error()+"; rollback mode: lifecycle manager is unavailable")
			return
		}
		changed, err := m.lcm.RestoreControllerEpoch(ctx, transition.SessionID,
			transition.TargetMode, transition.SourceMode, transition.NativeConversationID,
			transition.NativeConversationID == "")
		if err != nil || !changed {
			detail := cause.Error()
			if err != nil {
				detail += "; rollback mode: " + err.Error()
			}
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
				"RECOVERY_REQUIRED", detail)
			return
		}
	}
	if transition.SourceMode != domain.SessionModeChat && sourceRuntimeHandle.ID != "" {
		if err := m.runtime.Destroy(ctx, sourceRuntimeHandle); err != nil {
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
				"RECOVERY_REQUIRED", cause.Error()+"; source runtime cleanup: "+err.Error())
			return
		}
	}
	fresh := transition.NativeConversationID == "" &&
		(modeChanged || transition.SourceMode != domain.SessionModeChat ||
			!m.chatHoldsProviderConversation(ctx, transition.SessionID))
	if err := m.startTransitionTarget(
		ctx,
		transition.SessionID,
		fresh,
		false,
		domain.SessionInterfaceTransitionHistoryStrict,
	); err != nil {
		captureBackgroundFault(context.Background(), err, "interface_transition", "relaunch")
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
			"RECOVERY_REQUIRED", cause.Error()+"; source restore: "+err.Error())
		return
	}
	_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionFailed, code, cause.Error())
	m.deliverQueuedAfterAccountMove(transition)
}

func (m *Manager) chatHoldsProviderConversation(ctx context.Context, id domain.SessionID) bool {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil || !found {
		return true
	}
	return strings.TrimSpace(rec.Metadata.ProviderConversationID) != ""
}

func (m *Manager) stopTransitionTargetConclusive(ctx context.Context, transition domain.SessionInterfaceTransition) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	current, found, err := m.store.GetSession(ctx, transition.SessionID)
	if err != nil {
		return fmt.Errorf("read failed target ownership: %w", err)
	}
	if !found || domain.NormalizeSessionMode(current.Mode) != transition.TargetMode {
		return errors.New("failed target ownership changed before shutdown")
	}
	if transition.TargetMode == domain.SessionModeChat && m.chat == nil {
		return errors.New("cannot confirm target shutdown without Chat service")
	}
	return m.stopSourceControllerConclusive(current)
}

func (m *Manager) retainUnconfirmedTransitionTarget(transition domain.SessionInterfaceTransition, cause error) error {
	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionTargetStarting,
		"TARGET_STOP_UNCONFIRMED", "Orchestrator could not confirm the target controller stopped. Restart Orchestrator to retry shutdown before restoring the original interface. "+cause.Error()); err != nil {
		m.logger.Error("interface transition: retain unconfirmed target shutdown", "transition", transition.ID, "error", err)
		captureBackgroundFault(context.Background(), err, "interface_transition", "state_record")
		return fmt.Errorf("persist unconfirmed target for transition %s: %w", transition.ID, err)
	}
	return nil
}

func (m *Manager) moveInterfaceTransition(
	id string,
	next domain.SessionInterfaceTransitionPhase,
	errorCode, errorDetail string,
) error {
	return m.moveInterfaceTransitionWithNativeID(id, next, "", errorCode, errorDetail)
}

func (m *Manager) moveInterfaceTransitionWithNativeID(
	id string,
	next domain.SessionInterfaceTransitionPhase,
	nativeConversationID string,
	errorCode, errorDetail string,
) error {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return ports.ErrChatUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		current, ok, err := store.GetSessionInterfaceTransition(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if current.Phase.Terminal() {
			return fmt.Errorf("transition %s is already %s", id, current.Phase)
		}
		if nativeConversationID == "" {
			nativeConversationID = current.NativeConversationID
		}
		var moved bool
		if next == domain.SessionInterfaceTransitionCompleted && current.DegradedKind != "" {
			moved, err = store.CompleteSessionInterfaceTransition(ctx, id, current.Phase,
				degradedHandoffMarkerTextFor(current), m.clock())
		} else {
			moved, err = store.AdvanceSessionInterfaceTransition(ctx, id, current.Phase, next,
				nativeConversationID, errorCode, errorDetail, m.clock())
		}
		if err != nil {
			return err
		}
		if moved {
			return nil
		}
	}
	return fmt.Errorf("transition %s changed concurrently", id)
}

func (m *Manager) finishInterfaceTransition(
	id string,
	phase domain.SessionInterfaceTransitionPhase,
	errorCode, errorDetail string,
) error {
	return m.moveInterfaceTransition(id, phase, errorCode, errorDetail)
}

func (m *Manager) deliverTransitionMessages(
	ctx context.Context,
	transition domain.SessionInterfaceTransition,
) error {
	m.transitionDeliveryAttemptMu.Lock()
	defer m.transitionDeliveryAttemptMu.Unlock()
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return ports.ErrChatUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, interfaceTransitionStepLimit)
	defer cancel()
	messages, err := store.ListPendingSessionInterfaceTransitionMessages(ctx, transition.ID)
	if err != nil {
		return fmt.Errorf("read transition %s messages: %w", transition.ID, err)
	}
	if len(messages) == 0 {
		return nil
	}
	rec, err := m.waitForTransitionDeliveryReady(ctx, transition.SessionID, time.Time{})
	if err != nil {
		return fmt.Errorf("wait to deliver transition %s messages: %w", transition.ID, err)
	}
	lastTerminalInputAt, releaseTerminalInput := m.beginTerminalInputDrain(rec)
	if releaseTerminalInput != nil {
		defer releaseTerminalInput()
		if _, err := m.waitForTransitionDeliveryReady(ctx, transition.SessionID, lastTerminalInputAt); err != nil {
			return fmt.Errorf("recheck transition %s delivery readiness: %w", transition.ID, err)
		}
	}
	for _, message := range messages {
		if err := m.send(ctx, transition.SessionID, message.Message, message.ClientMessageID, ports.MessageDeliveryOptions{AuthoredByUser: message.AuthoredByUser, SenderSessionID: message.SenderSessionID, InteractionAt: message.CreatedAt}); err != nil {
			return fmt.Errorf("deliver transition %s message %d: %w", transition.ID, message.ID, err)
		}
		if err := store.MarkSessionInterfaceTransitionMessageDelivered(ctx, message.ID, m.clock()); err != nil {
			return fmt.Errorf("acknowledge transition %s message %d: %w", transition.ID, message.ID, err)
		}
	}
	return nil
}

func (m *Manager) waitForTransitionDeliveryReady(
	ctx context.Context,
	id domain.SessionID,
	lastTerminalInputAt time.Time,
) (domain.SessionRecord, error) {
	ticker := time.NewTicker(interfaceTransitionPoll)
	defer ticker.Stop()
	idleSince := time.Time{}
	for {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return domain.SessionRecord{}, err
		}
		if !ok {
			return domain.SessionRecord{}, ErrNotFound
		}
		if rec.IsTerminated || rec.Activity.State == domain.ActivityExited {
			return domain.SessionRecord{}, ErrTerminated
		}
		ready := rec.Activity.State == domain.ActivityIdle
		if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI {
			ready = ready && !rec.FirstSignalAt.IsZero() && tuiIdleAfterInput(rec, lastTerminalInputAt)
		}
		if ready {
			if idleSince.IsZero() {
				idleSince = time.Now()
			} else if time.Since(idleSince) >= interfaceTransitionIdleSettle {
				return rec, nil
			}
		} else {
			idleSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return domain.SessionRecord{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) queueDuringInterfaceTransition(
	ctx context.Context,
	id domain.SessionID,
	message, clientMessageID string,
	options ports.MessageDeliveryOptions,
) (bool, error) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return false, nil
	}
	transition, active, err := store.GetActiveSessionInterfaceTransition(ctx, id)
	if err != nil || !active {
		return false, err
	}
	if strings.TrimSpace(clientMessageID) == "" {
		clientMessageID = "interface-transition:" + m.newLaunchID()
	}
	at := options.InteractionAt
	if at.IsZero() {
		at = m.clock()
	}
	if err := store.EnqueueSessionInterfaceTransitionMessage(
		ctx, transition.ID, clientMessageID, message, at, options,
	); err != nil {
		return true, err
	}
	return true, nil
}

func (m *Manager) settleTransitionMessages(transition domain.SessionInterfaceTransition) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		m.logger.Error("interface transition: store does not support queued messages", "transitionID", transition.ID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), interfaceTransitionStepLimit)
	current, ok, err := store.GetSessionInterfaceTransition(ctx, transition.ID)
	cancel()
	if err != nil {
		m.logger.Error("interface transition: read terminal state for queued messages",
			"transitionID", transition.ID, "error", err)
		m.wakeTransitionMessageDispatcher()
		return
	}
	if !ok || !current.Phase.Terminal() {
		m.wakeTransitionMessageDispatcher()
		return
	}
	if err := m.deliverTransitionMessages(context.Background(), current); err != nil {
		m.logger.Error("interface transition: queued-message delivery deferred",
			"transitionID", transition.ID, "error", err)
		captureBackgroundFault(context.Background(), err, "interface_transition", "queued_delivery")
	}
	m.wakeTransitionMessageDispatcher()
}

func (m *Manager) deliverAllTransitionMessages(ctx context.Context) error {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return nil
	}
	transitions, err := store.ListDeliverableSessionInterfaceTransitions(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, transition := range transitions {
		if err := m.deliverTransitionMessages(ctx, transition); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) wakeTransitionMessageDispatcher() {
	select {
	case m.transitionDeliveryWake <- struct{}{}:
	default:
	}
}

func (m *Manager) startTransitionMessageDispatcher(ctx context.Context) {
	if _, ok := m.store.(interfaceTransitionStore); !ok {
		return
	}
	m.transitionDeliveryMu.Lock()
	if m.transitionDeliveryRunning {
		m.transitionDeliveryMu.Unlock()
		return
	}
	m.transitionDeliveryRunning = true
	m.transitionDeliveryMu.Unlock()

	go func() {
		defer func() {
			m.transitionDeliveryMu.Lock()
			m.transitionDeliveryRunning = false
			m.transitionDeliveryMu.Unlock()
		}()
		delay := time.Duration(0)
		for {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-m.transitionDeliveryWake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
			}
			if err := m.deliverAllTransitionMessages(ctx); err != nil {
				m.logger.Error("interface transition: retry queued-message delivery", "error", err)
				captureBackgroundFault(ctx, err, "interface_transition", "queued_delivery")
				delay = interfaceDeliveryRetry
			} else {
				delay = interfaceDeliveryIdlePoll
			}
		}
	}()
}

func (m *Manager) hasActiveInterfaceTransition(ctx context.Context, id domain.SessionID) (bool, error) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return false, nil
	}
	_, active, err := store.GetActiveSessionInterfaceTransition(ctx, id)
	return active, err
}

func (m *Manager) recoverInterruptedInterfaceTransitions(
	ctx context.Context,
) ([]domain.SessionInterfaceTransition, error) {
	return m.recoverInterfaceTransitions(ctx, "")
}

func (m *Manager) recoverInterfaceTransitions(ctx context.Context, transitionID string) ([]domain.SessionInterfaceTransition, error) {
	m.interfaceRecoveryMu.Lock()
	defer m.interfaceRecoveryMu.Unlock()
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return nil, nil
	}
	var active []domain.SessionInterfaceTransition
	var err error
	if transitionID == "" {
		active, err = store.ListActiveSessionInterfaceTransitions(ctx)
	} else {
		var transition domain.SessionInterfaceTransition
		var found bool
		transition, found, err = store.GetSessionInterfaceTransition(ctx, transitionID)
		if found && transition.Active() {
			active = append(active, transition)
		}
	}
	if err != nil {
		return nil, err
	}
	for i := range active {
		transition := &active[i]
		m.agentOpMu.Lock()
		operation := m.agentOperations[transition.SessionID]
		if operation != "" && operation != agentOperationInterfaceRecovery {
			if m.deferredInterfaceRecovery == nil {
				m.deferredInterfaceRecovery = make(map[domain.SessionID]string)
			}
			m.deferredInterfaceRecovery[transition.SessionID] = transition.ID
			m.agentOpMu.Unlock()
			m.logger.Info("interface transition: recovery deferred behind exclusive operation", "sessionID", transition.SessionID, "operation", operation)
			continue
		}
		m.agentOperations[transition.SessionID] = agentOperationInterfaceRecovery
		delete(m.deferredInterfaceRecovery, transition.SessionID)
		drained := m.inputDrained[transition.SessionID]
		m.agentOpMu.Unlock()
		if drained != nil {
			select {
			case <-drained:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		detail := "The daemon restarted during the interface switch; Orchestrator recovered the session from its last committed mode."
		if transition.AccountMove() {
			detail = "The daemon restarted during the account switch; the session stays on its committed account."
			switch transition.Phase {
			case domain.SessionInterfaceTransitionSourceStopped,
				domain.SessionInterfaceTransitionTargetStarting,
				domain.SessionInterfaceTransitionActivating:
				rec, found, readErr := m.store.GetSession(ctx, transition.SessionID)
				if readErr != nil {
					return nil, readErr
				}
				committed := false
				if found && m.agentAccounts != nil {
					bound, bindErr := m.agentAccounts.SessionAccountID(ctx, transition.SessionID, rec.Harness)
					if bindErr != nil {
						return nil, bindErr
					}
					committed = bound == transition.TargetAccountID
				}
				if committed {
					detail = "The daemon restarted just after the account switch finished; the agent stays on its new account."
				} else if found && !rec.IsTerminated {
					if stopErr := m.stopTransitionTargetConclusive(ctx, *transition); stopErr != nil {
						if err := m.retainUnconfirmedTransitionTarget(*transition, stopErr); err != nil {
							return nil, err
						}
						m.logger.Error("account transition: session quarantined after unconfirmed target shutdown", "sessionID", transition.SessionID, "transition", transition.ID, "error", stopErr)
						captureBackgroundFault(ctx, stopErr, "session_quarantine", "")
						continue
					}
					detail = "The daemon restarted during the account switch; Orchestrator stopped the agent and kept the session on its committed account. Resume it to continue."
				}
			}
		} else if (transition.SourceMode == domain.SessionModeTUI && transition.TargetMode == domain.SessionModeChat) ||
			transition.ErrorCode == "TARGET_STOP_UNCONFIRMED" {
			rec, found, readErr := m.store.GetSession(ctx, transition.SessionID)
			if readErr != nil {
				return nil, readErr
			}
			if found && !rec.IsTerminated && domain.NormalizeSessionMode(rec.Mode) == transition.TargetMode {
				if stopErr := m.stopTransitionTargetConclusive(ctx, *transition); stopErr != nil {
					if err := m.retainUnconfirmedTransitionTarget(*transition, stopErr); err != nil {
						return nil, err
					}
					m.logger.Error("interface transition: session quarantined after unconfirmed target shutdown", "sessionID", transition.SessionID, "transition", transition.ID, "error", stopErr)
					captureBackgroundFault(ctx, stopErr, "session_quarantine", "")
					continue
				}
				if m.lcm == nil {
					return nil, fmt.Errorf("recover transition %s: lifecycle manager is unavailable", transition.ID)
				}
				changed, rollbackErr := m.lcm.RestoreControllerEpoch(
					ctx,
					transition.SessionID,
					transition.TargetMode,
					transition.SourceMode,
					transition.NativeConversationID,
					transition.NativeConversationID == "",
				)
				if rollbackErr != nil {
					return nil, fmt.Errorf("recover transition %s source mode: %w", transition.ID, rollbackErr)
				}
				if !changed {
					return nil, fmt.Errorf("recover transition %s source mode: session changed", transition.ID)
				}
				if transition.SourceMode == domain.SessionModeTUI {
					detail = "The daemon restarted during the interface switch; Orchestrator restored Terminal so native history can be replayed safely on retry."
				} else {
					detail = "The daemon restarted during the interface switch; Orchestrator confirmed the failed Terminal controller stopped and restored Chat ownership."
				}
			}
		}
		moved, err := store.AdvanceSessionInterfaceTransition(
			ctx,
			transition.ID,
			transition.Phase,
			domain.SessionInterfaceTransitionRecovery,
			transition.NativeConversationID,
			"DAEMON_RESTARTED",
			detail,
			m.clock(),
		)
		if err != nil {
			return nil, err
		}
		if !moved {
			current, found, readErr := store.GetSessionInterfaceTransition(ctx, transition.ID)
			if readErr != nil {
				return nil, readErr
			}
			if found && current.Phase == domain.SessionInterfaceTransitionCancelled &&
				(transition.Phase == domain.SessionInterfaceTransitionRequested ||
					transition.Phase == domain.SessionInterfaceTransitionPreflighting ||
					transition.Phase == domain.SessionInterfaceTransitionDraining) {
				*transition = current
				m.endAgentOperation(transition.SessionID, agentOperationInterfaceRecovery)
				continue
			}
			return nil, fmt.Errorf("transition %s changed while recovering", transition.ID)
		}
		transition.Phase = domain.SessionInterfaceTransitionRecovery
		transition.ErrorCode = "DAEMON_RESTARTED"
		transition.ErrorDetail = detail
		transition.UpdatedAt = m.clock()
		transition.CompletedAt = transition.UpdatedAt
		m.endAgentOperation(transition.SessionID, agentOperationInterfaceRecovery)
	}
	return active, nil
}

func oppositeSessionMode(mode domain.SessionMode) domain.SessionMode {
	if domain.NormalizeSessionMode(mode) == domain.SessionModeChat {
		return domain.SessionModeTUI
	}
	return domain.SessionModeChat
}

func interfaceTransitionErrorCode(err error) string {
	switch {
	case errors.Is(err, ports.ErrChatUnsupported), errors.Is(err, ErrInterfaceHandoffUnsupported):
		return "INTERFACE_HANDOFF_UNSUPPORTED"
	case errors.Is(err, ports.ErrChatDriverUnavailable), errors.Is(err, ports.ErrAgentBinaryNotFound):
		return "TARGET_UNAVAILABLE"
	case errors.Is(err, ports.ErrChatDriverIncompatible):
		return "TARGET_INCOMPATIBLE"
	case errors.Is(err, ports.ErrChatAuthRequired), errors.Is(err, ports.ErrAgentAuthRequired):
		return "TARGET_AUTH_REQUIRED"
	case errors.Is(err, ErrInterfaceProviderHistoryRecoveryUnavailable):
		return "PROVIDER_HISTORY_RECOVERY_UNAVAILABLE"
	case errors.Is(err, ErrNativeConversationMissing):
		return "NATIVE_SESSION_MISSING"
	case errors.Is(err, ErrNativeConversationUnverified):
		return "NATIVE_SESSION_UNVERIFIED"
	default:
		return "TARGET_PREFLIGHT_FAILED"
	}
}

func transitionDetail(transition domain.SessionInterfaceTransition, code, detail string) string {
	if !transition.AccountMove() {
		return detail
	}
	switch code {
	case "DRAIN_QUIESCENCE_UNVERIFIED":
		return "Orchestrator could not confirm that the terminal was idle, so the agent stays on its current account. Try again when it is idle."
	case "DRAIN_DECISION_PENDING":
		return "The agent is waiting for an answer in the terminal, so it stays on its current account. Answer it, then try again."
	case "TRANSITION_CANCELLED":
		return "The account switch was cancelled. The agent stays on its current account."
	case "SESSION_CHANGED":
		return "The session changed before the account switch could start. Try again."
	}
	return detail
}
