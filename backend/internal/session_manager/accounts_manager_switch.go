package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const agentOperationAccountSwitch agentOperationKind = "managed_account_switch"

// AccountsManagerSwitchConfig carries user intent and the observed binding revision.
type AccountsManagerSwitchConfig struct {
	OperationID      string
	ExpectedRevision int64
	Mode             domain.AccountsManagerConnectionMode
	AccountID        string
	Policy           domain.SessionInterfaceTransitionPolicy
	NewConversation  bool
}

type accountsManagerSwitchRun struct {
	id              string
	cancel          context.CancelFunc
	done            chan struct{}
	lastInput       time.Time
	releaseTerminal func()
	releaseOnce     sync.Once
	running         bool
	cancelled       bool
}

type accountsManagerChatHandoff interface {
	ArmAccountsManagerHandoff(context.Context, domain.SessionID, bool) error
	PrepareAccountsManagerHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	AbortAccountsManagerHandoff(domain.SessionID)
	AcknowledgeAccountsManagerSwitch(context.Context, domain.SessionID, string, func(context.Context) error) error
}

func sameAccountSwitchRequest(op domain.AccountsManagerSwitch, id domain.SessionID, cfg AccountsManagerSwitchConfig) bool {
	return op.SessionID == id && op.ID == cfg.OperationID && op.SourceRevision == cfg.ExpectedRevision &&
		op.TargetMode == cfg.Mode && op.TargetAccountID == cfg.AccountID && op.Policy == cfg.Policy && op.NewConversation == cfg.NewConversation
}

// StartAccountsManagerSwitch reserves a controller handoff before admitting new work.
func (m *Manager) StartAccountsManagerSwitch(ctx context.Context, id domain.SessionID, cfg AccountsManagerSwitchConfig) (domain.AccountsManagerSwitch, error) {
	store, supported := m.store.(ports.AccountsManagerSwitchStore)
	router, routed := m.accountsManager.(ports.AccountsManagerSwitchRouter)
	if !supported || !routed {
		return domain.AccountsManagerSwitch{}, ErrInterfaceHandoffUnsupported
	}
	previous, found, err := store.GetAccountsManagerSwitch(ctx, cfg.OperationID)
	if err != nil {
		return previous, err
	}
	if found {
		if !sameAccountSwitchRequest(previous, id, cfg) {
			return previous, domain.ErrAccountsManagerSwitchConflict
		}
		return previous, nil
	}
	if !cfg.Policy.Valid() || (cfg.Mode != domain.AccountsManagerNative && cfg.Mode != domain.AccountsManagerManaged) || cfg.ExpectedRevision <= 0 {
		return previous, domain.ErrAccountsManagerSwitchConflict
	}
	if err := m.beginAgentSwitchAttempt(); err != nil {
		return previous, err
	}
	workerOwns := false
	defer func() {
		if !workerOwns {
			m.agentSwitchWorkers.Done()
		}
	}()
	if err := m.beginAgentOperation(ctx, id, agentOperationAccountSwitch); err != nil {
		return previous, err
	}
	run := &accountsManagerSwitchRun{id: cfg.OperationID, done: make(chan struct{}), running: true}
	defer func() {
		if !workerOwns {
			m.releaseAccountSwitch(id, run)
		}
	}()
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return previous, err
	}
	provider, ok := accountsManagerProvider(rec.Harness)
	if !ok || rec.IsTerminated || rec.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return previous, domain.ErrAccountsManagerSwitchConflict
	}
	// Only an exact no-op skips handoff validation. Other requests read the
	// binding again at the original validation boundary, including lookup errors.
	if binding, found, err := store.GetAccountsManagerSessionRoute(ctx, id, provider); err == nil && found && !binding.Blocked && binding.Mode == cfg.Mode && binding.AccountID == cfg.AccountID {
		latest, found, err := store.GetLatestAccountsManagerSwitch(ctx, id)
		if err != nil {
			return previous, err
		}
		if found && !latest.Phase.Terminal() {
			return previous, domain.ErrAccountsManagerSwitchConflict
		}
		return domain.AccountsManagerSwitch{
			ID: cfg.OperationID, SessionID: id, Provider: provider,
			SourceMode: binding.Mode, SourceAccountID: binding.AccountID, SourceRevision: binding.Revision,
			TargetMode: binding.Mode, TargetAccountID: binding.AccountID, TargetRevision: binding.Revision,
			Policy: cfg.Policy, NewConversation: cfg.NewConversation, Phase: domain.AccountsManagerSwitchReady,
			CreatedAt: binding.UpdatedAt, UpdatedAt: binding.UpdatedAt,
		}, nil
	}
	chatHandoff, chatSupported := m.chat.(accountsManagerChatHandoff)
	if rec.Mode == domain.SessionModeChat {
		if !chatSupported {
			return previous, ErrInterfaceHandoffUnsupported
		}
		if provider == domain.AccountsManagerProviderCodex && cfg.Mode == domain.AccountsManagerManaged {
			if _, ok := m.chat.(managedChatPreflighter); !ok {
				return previous, ports.ErrChatUnsupported
			}
		}
	} else if rec.Metadata.RuntimeLaunchID == "" || rec.Metadata.RuntimeHandleID == "" {
		return previous, ErrIncompleteHandle
	} else if _, err := m.accountSwitchLaunchHandles(id); err != nil {
		return previous, err
	}
	binding, found, err := store.GetAccountsManagerSessionRoute(ctx, id, provider)
	if err != nil {
		return previous, err
	}
	if !found || binding.Revision != cfg.ExpectedRevision || binding.Blocked {
		return previous, domain.ErrAccountsManagerBindingConflict
	}
	if binding.Mode == cfg.Mode && binding.AccountID == cfg.AccountID {
		return previous, domain.ErrAccountsManagerSwitchConflict
	}
	op := domain.AccountsManagerSwitch{
		ID: cfg.OperationID, SessionID: id, Provider: provider,
		SourceMode: binding.Mode, SourceAccountID: binding.AccountID, SourceRevision: binding.Revision,
		SourceOwner: rec.ControllerOwner(), SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID,
		TargetMode: cfg.Mode, TargetAccountID: cfg.AccountID, TargetGeneration: m.newLaunchID(),
		Policy: cfg.Policy, NewConversation: cfg.NewConversation,
	}
	op, created, err := router.AdmitAgentAccountSwitch(ctx, op, rec.Metadata.Model)
	if err != nil || !created {
		return op, err
	}
	workerCtx, cancel := context.WithCancel(m.backgroundContext)
	run.cancel = cancel
	run.lastInput, run.releaseTerminal = m.beginTerminalInputDrain(rec)
	m.accountSwitchMu.Lock()
	if m.accountSwitches == nil {
		m.accountSwitches = make(map[domain.SessionID]*accountsManagerSwitchRun)
	}
	m.accountSwitches[id] = run
	m.accountSwitchMu.Unlock()
	workerOwns = true
	if rec.Mode == domain.SessionModeChat {
		if err := chatHandoff.ArmAccountsManagerHandoff(ctx, id, cfg.NewConversation); err != nil {
			m.finishAccountSwitchRun(store, op, run, "SOURCE_INTAKE_UNAVAILABLE")
			return op, err
		}
	}
	go func() {
		code := m.executeAccountSwitch(workerCtx, store, router, op, run)
		m.finishAccountSwitchRun(store, op, run, code)
	}()
	return op, nil
}

func (m *Manager) executeAccountSwitch(ctx context.Context, store ports.AccountsManagerSwitchStore, router ports.AccountsManagerSwitchRouter, op domain.AccountsManagerSwitch, run *accountsManagerSwitchRun) string {
	var err error
	op, err = store.AdvanceAccountsManagerSwitch(ctx, op.ID, domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, "")
	if err != nil {
		return "ADMISSION_CHANGED"
	}
	rec, err := m.accountSwitchSource(ctx, op)
	if err != nil {
		return "SOURCE_CHANGED"
	}
	if !op.NewConversation {
		if _, err := m.handoffNativeConversationID(ctx, rec); err != nil {
			return "NATIVE_HISTORY_UNAVAILABLE"
		}
	}
	if rec.Mode == domain.SessionModeChat {
		chatHandoff, supported := m.chat.(accountsManagerChatHandoff)
		if !supported {
			return "SOURCE_INTAKE_UNAVAILABLE"
		}
		err = chatHandoff.PrepareAccountsManagerHandoff(ctx, op.SessionID, op.Policy)
	} else {
		err = m.prepareAccountSwitchTerminalHandoff(ctx, rec, op.Policy, run.lastInput)
	}
	if err != nil {
		if errors.Is(err, ErrSwitchSourceStopUnconfirmed) {
			return "SOURCE_OWNERSHIP_UNCONFIRMED"
		}
		return "SOURCE_NOT_QUIESCENT"
	}
	rec, err = m.accountSwitchSource(ctx, op)
	if err != nil {
		return "SOURCE_CHANGED"
	}
	if err := router.ValidateAgentAccountTarget(ctx, op.TargetMode, op.Provider, op.TargetAccountID, rec.Metadata.Model); err != nil {
		return "TARGET_UNAVAILABLE"
	}
	op, err = m.prepareAccountSwitchStop(ctx, store, op, rec)
	if err != nil {
		return "ADMISSION_CHANGED"
	}
	return m.commitAndStartAccountSwitch(ctx, store, router, op)
}

func (m *Manager) prepareAccountSwitchTerminalHandoff(ctx context.Context, rec domain.SessionRecord, policy domain.SessionInterfaceTransitionPolicy, lastInput time.Time) error {
	if policy == domain.SessionInterfaceTransitionInterrupt && rec.Activity.State != domain.ActivityExited {
		ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(rec.Metadata), Generation: rec.Metadata.RuntimeLaunchID}
		if ref.Handle.ID == "" || ref.Generation == "" {
			return ErrSwitchSourceStopUnconfirmed
		}
		switch m.runtime.ProbeFencedRuntime(ctx, ref).Liveness {
		case ports.FencedDead:
			return nil
		case ports.FencedAlive:
		default:
			return ErrSwitchSourceStopUnconfirmed
		}
	}
	return m.prepareSourceHandoff(ctx, rec, policy, lastInput)
}

func (m *Manager) prepareAccountSwitchStop(ctx context.Context, store ports.AccountsManagerSwitchStore, op domain.AccountsManagerSwitch, rec domain.SessionRecord) (domain.AccountsManagerSwitch, error) {
	nativeID, empty := op.SourceNativeConversationID, op.EmptySource
	if !op.NewConversation && !empty && nativeID == "" {
		var err error
		nativeID, err = m.handoffNativeConversationID(ctx, rec)
		if err != nil {
			return op, err
		}
		empty = nativeID == ""
	}
	return store.PrepareAccountsManagerSwitchStop(ctx, op.ID, empty, nativeID, rec.ControllerOwner())
}

func (m *Manager) commitAndStartAccountSwitch(ctx context.Context, store ports.AccountsManagerSwitchStore, router ports.AccountsManagerSwitchRouter, op domain.AccountsManagerSwitch) string {
	stepCtx, cancel := context.WithTimeout(ctx, interfaceTransitionStepLimit)
	defer cancel()
	rec, err := m.accountSwitchSource(stepCtx, op)
	if err != nil {
		return "SOURCE_CHANGED"
	}
	if err := router.SynchronizeAgentBindings(stepCtx); err != nil {
		return "REVOCATION_UNCONFIRMED"
	}
	if err := m.stopAccountSwitchSource(stepCtx, op); err != nil {
		return "SOURCE_STOP_UNCONFIRMED"
	}
	op, err = store.AdvanceAccountsManagerSwitch(stepCtx, op.ID, op.Phase, domain.AccountsManagerSwitchStopped, "")
	if err != nil {
		return "STOP_RECORD_UNCONFIRMED"
	}
	op, err = router.CommitAgentAccountSwitch(stepCtx, op.ID, rec.Metadata.Model)
	if err != nil {
		return "BINDING_COMMIT_UNCONFIRMED"
	}
	return m.startAccountSwitchTarget(stepCtx, store, router, op)
}

func (m *Manager) startAccountSwitchTarget(stepCtx context.Context, store ports.AccountsManagerSwitchStore, router ports.AccountsManagerSwitchRouter, op domain.AccountsManagerSwitch) string {
	if err := router.SynchronizeAgentBindings(stepCtx); err != nil {
		return "BINDING_SYNC_UNCONFIRMED"
	}
	var err error
	op, err = store.AdvanceAccountsManagerSwitch(stepCtx, op.ID, op.Phase, domain.AccountsManagerSwitchStarting, "")
	if err != nil {
		return "START_RECORD_UNCONFIRMED"
	}
	rec, err := m.getRecord(stepCtx, op.SessionID)
	if err != nil {
		return "SESSION_UNAVAILABLE"
	}
	project, err := m.loadProject(stepCtx, rec.ProjectID)
	if err != nil {
		return "PROJECT_UNAVAILABLE"
	}
	fresh := op.NewConversation || op.EmptySource
	if !fresh && op.SourceNativeConversationID != "" {
		if rec.Mode == domain.SessionModeChat {
			rec.Metadata.ProviderConversationID = op.SourceNativeConversationID
		} else {
			rec.Metadata.AgentSessionID = op.SourceNativeConversationID
		}
	}
	result, err := m.relaunchSessionWithOptions(stepCtx, "switch managed account", rec, project, workspaceInfo(rec), nil,
		fresh, !fresh, op.TargetGeneration, domain.SessionInterfaceTransitionHistoryStrict, true)
	if err != nil {
		return "TARGET_START_UNCONFIRMED"
	}
	if err := m.acknowledgeAccountSwitch(stepCtx, store, op, result.Session); err != nil {
		return "TARGET_NOT_READY"
	}
	return ""
}

func (m *Manager) accountSwitchSource(ctx context.Context, op domain.AccountsManagerSwitch) (domain.SessionRecord, error) {
	rec, err := m.getRecord(ctx, op.SessionID)
	if err != nil {
		return rec, err
	}
	owner := rec.ControllerOwner()
	if rec.IsTerminated || owner.Harness != op.SourceOwner.Harness || owner.Mode != op.SourceOwner.Mode ||
		owner.RuntimeLaunchID != op.SourceOwner.RuntimeLaunchID || owner.ControllerGeneration != op.SourceOwner.ControllerGeneration ||
		rec.Metadata.RuntimeHandleID != op.SourceRuntimeHandleID {
		return rec, domain.ErrAccountsManagerSwitchConflict
	}
	return rec, nil
}

func (m *Manager) stopAccountSwitchSource(ctx context.Context, op domain.AccountsManagerSwitch) error {
	rec, err := m.accountSwitchSource(ctx, op)
	if err != nil {
		return err
	}
	if rec.Mode == domain.SessionModeChat {
		return m.chat.StopChat(ctx, rec.ID)
	}
	ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(rec.Metadata), Generation: rec.Metadata.RuntimeLaunchID}
	return m.stopAccountsManagerRuntime(ctx, ref)
}

func (m *Manager) stopAccountsManagerRuntime(ctx context.Context, ref ports.FencedRuntimeRef) error {
	probe := m.runtime.ProbeFencedRuntime(ctx, ref)
	if probe.Liveness != ports.FencedAlive && probe.Liveness != ports.FencedDead {
		return ErrSwitchSourceStopUnconfirmed
	}
	if err := m.stopSourceRuntime(ctx, ref); err != nil {
		return err
	}
	if m.runtime.ProbeFencedRuntime(ctx, ref).Liveness != ports.FencedDead {
		return ErrSwitchSourceStopUnconfirmed
	}
	return nil
}

func (m *Manager) acknowledgeAccountSwitch(ctx context.Context, store ports.AccountsManagerSwitchStore, op domain.AccountsManagerSwitch, target domain.SessionRecord) error {
	chatHandoff, chatSupported := m.chat.(accountsManagerChatHandoff)
	if target.Mode == domain.SessionModeChat && !chatSupported {
		return ErrInterfaceHandoffUnsupported
	}
	readyCtx, cancel := context.WithTimeout(ctx, m.switchTargetStartWait)
	defer cancel()
	commit := func(commitCtx context.Context) error {
		_, err := store.AcknowledgeAccountsManagerSwitch(commitCtx, op.ID, op.TargetGeneration)
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	idleSamples := 0
	for {
		if target.Mode == domain.SessionModeChat {
			if err := chatHandoff.AcknowledgeAccountsManagerSwitch(readyCtx, op.SessionID, op.TargetGeneration, commit); err == nil {
				return nil
			}
		} else {
			alive, err := m.exactTargetGenerationAlive(readyCtx, runtimeHandle(target.Metadata), op.SessionID, domain.AgentGenerationID(op.TargetGeneration))
			if err == nil && alive && m.accountSwitchTargetUnambiguous(readyCtx, op, runtimeHandle(target.Metadata)) && m.accountTargetComposerReady(readyCtx, target) {
				idleSamples++
				if idleSamples >= interfaceTransitionSurfaceIdleSamples {
					return commit(readyCtx)
				}
			} else {
				idleSamples = 0
			}
		}
		select {
		case <-readyCtx.Done():
			return readyCtx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) accountTargetComposerReady(ctx context.Context, rec domain.SessionRecord) bool {
	agent, found := m.agents.Agent(rec.Harness)
	if !found {
		return false
	}
	inspector, supported := agent.(ports.TerminalSurfaceInspector)
	reader, styled := m.runtime.(ports.StyledTerminalOutputReader)
	if !supported || !styled {
		return false
	}
	output, err := reader.GetStyledOutput(ctx, runtimeHandle(rec.Metadata), interfaceTransitionOutputLines)
	if err != nil {
		return false
	}
	observation := inspector.InspectTerminalSurface(output)
	return observation.Work == ports.TerminalSurfaceWorkIdle && observation.Composer == ports.TerminalComposerEmpty
}

func (m *Manager) finishAccountSwitchRun(store ports.AccountsManagerSwitchStore, initial domain.AccountsManagerSwitch, run *accountsManagerSwitchRun, code string) {
	defer m.agentSwitchWorkers.Done()
	defer func() {
		m.accountSwitchMu.Lock()
		done := run.done
		cancelled := run.cancelled && m.accountSwitches[initial.SessionID] == run
		if cancelled {
			// Keep the cancelled run unavailable while fence release can still block.
			m.accountSwitchMu.Unlock()
			if chat, ok := m.chat.(accountsManagerChatHandoff); ok {
				chat.AbortAccountsManagerHandoff(initial.SessionID)
			}
			m.releaseAccountSwitch(initial.SessionID, run)
			m.accountSwitchMu.Lock()
		}
		run.running = false
		close(done)
		m.accountSwitchMu.Unlock()
	}()
	run.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	op, found, err := store.GetAccountsManagerSwitch(ctx, initial.ID)
	// Shutdown does not invalidate durable proof that stopping has not begun.
	shutdownBeforeStop := m.backgroundContext.Err() != nil &&
		(op.Phase == domain.AccountsManagerSwitchRequested || op.Phase == domain.AccountsManagerSwitchWaiting)
	if err == nil && found && !op.Phase.Terminal() && !shutdownBeforeStop {
		phase := domain.AccountsManagerSwitchRecoveryRequired
		if op.Phase == domain.AccountsManagerSwitchRequested || op.Phase == domain.AccountsManagerSwitchWaiting {
			phase = domain.AccountsManagerSwitchFailed
			if code == "TARGET_REVALIDATION_UNAVAILABLE" {
				// A retry outage retains pre-stop proof and the user's recovery choices.
				phase = domain.AccountsManagerSwitchWaiting
			}
		}
		if op.Phase == domain.AccountsManagerSwitchWaiting && code == "SOURCE_OWNERSHIP_UNCONFIRMED" {
			// Preserve both intake fences and the durable pre-stop cancellation proof.
			phase = op.Phase
		}
		if code == "" {
			code = "OUTCOME_UNCONFIRMED"
		}
		op, err = store.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, phase, code)
	}
	if err == nil && found && op.Phase.Terminal() {
		if op.Phase != domain.AccountsManagerSwitchReady {
			if chat, ok := m.chat.(accountsManagerChatHandoff); ok {
				chat.AbortAccountsManagerHandoff(initial.SessionID)
			}
		}
		m.releaseAccountSwitch(initial.SessionID, run)
	}
	if router, ok := m.accountsManager.(ports.AccountsManagerSwitchRouter); ok {
		if err := router.SynchronizeAgentBindings(ctx); err != nil {
			m.logger.Warn("account switch reconciliation remains pending", "sessionID", initial.SessionID)
		}
	}
}

func (m *Manager) releaseAccountSwitch(id domain.SessionID, run *accountsManagerSwitchRun) {
	run.releaseOnce.Do(func() {
		if run.releaseTerminal != nil {
			run.releaseTerminal()
		}
		m.accountSwitchMu.Lock()
		if m.accountSwitches[id] == run {
			delete(m.accountSwitches, id)
		}
		m.accountSwitchMu.Unlock()
		m.endAgentOperation(id, agentOperationAccountSwitch)
	})
}

// CancelAccountsManagerSwitch requires durable proof that source stopping has not begun.
func (m *Manager) CancelAccountsManagerSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	store, ok := m.store.(ports.AccountsManagerSwitchStore)
	if !ok {
		return domain.AccountsManagerSwitch{}, ErrInterfaceHandoffUnsupported
	}
	op, found, err := store.GetAccountsManagerSwitch(ctx, operationID)
	if err != nil {
		return op, err
	}
	if !found || op.SessionID != id {
		return op, ErrNotFound
	}
	for op.Phase != domain.AccountsManagerSwitchCancelled {
		if op.Phase != domain.AccountsManagerSwitchRequested && op.Phase != domain.AccountsManagerSwitchWaiting {
			return op, domain.ErrAccountsManagerSwitchConflict
		}
		cancelled, advanceErr := store.AdvanceAccountsManagerSwitch(ctx, operationID, op.Phase, domain.AccountsManagerSwitchCancelled, "")
		if advanceErr == nil {
			op = cancelled
			break
		}
		current, found, readErr := store.GetAccountsManagerSwitch(ctx, operationID)
		if readErr != nil {
			return op, errors.Join(advanceErr, readErr)
		}
		if !found || current.SessionID != id {
			return op, ErrNotFound
		}
		if current.Phase == domain.AccountsManagerSwitchCancelled {
			op = current
			break
		}
		// Retry only the single forward transition that retains pre-stop proof.
		if op.Phase == domain.AccountsManagerSwitchRequested && current.Phase == domain.AccountsManagerSwitchWaiting && errors.Is(advanceErr, domain.ErrAccountsManagerSwitchConflict) {
			op = current
			continue
		}
		return current, advanceErr
	}
	m.accountSwitchMu.Lock()
	run := m.accountSwitches[id]
	settled := run != nil && run.id == operationID && !run.running
	if run != nil && run.id == operationID {
		run.cancelled = true
		run.cancel()
	}
	if settled {
		delete(m.accountSwitches, id)
	}
	m.accountSwitchMu.Unlock()
	if settled {
		if chat, ok := m.chat.(accountsManagerChatHandoff); ok {
			chat.AbortAccountsManagerHandoff(id)
		}
		m.releaseAccountSwitch(id, run)
		if router, ok := m.accountsManager.(ports.AccountsManagerSwitchRouter); ok {
			if err := router.SynchronizeAgentBindings(ctx); err != nil {
				m.logger.Warn("cancelled account switch bindings await reconciliation", "sessionID", id)
			}
		}
	}
	return op, nil
}

func (m *Manager) reconcileAccountsManagerSwitches(ctx context.Context) error {
	store, ok := m.store.(ports.AccountsManagerSwitchStore)
	if !ok {
		return nil
	}
	ops, err := store.ListActiveAccountsManagerSwitches(ctx)
	if err != nil {
		return err
	}
	for _, op := range ops {
		m.accountSwitchMu.Lock()
		existing := m.accountSwitches[op.SessionID]
		m.accountSwitchMu.Unlock()
		if existing != nil && existing.id == op.ID {
			continue
		}
		if err := m.beginAgentOperation(ctx, op.SessionID, agentOperationAccountSwitch); err != nil {
			return err
		}
		// Requested/waiting are durable proof that stopping never began.
		if op.Phase != domain.AccountsManagerSwitchRecoveryRequired && op.Phase != domain.AccountsManagerSwitchRequested && op.Phase != domain.AccountsManagerSwitchWaiting {
			if _, err := store.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, domain.AccountsManagerSwitchRecoveryRequired, "DAEMON_RESTARTED"); err != nil {
				return err
			}
		}
		run := &accountsManagerSwitchRun{id: op.ID, done: make(chan struct{}), cancel: func() {}}
		rec, err := m.getRecord(ctx, op.SessionID)
		if err != nil {
			m.endAgentOperation(op.SessionID, agentOperationAccountSwitch)
			return err
		}
		run.lastInput, run.releaseTerminal = m.beginTerminalInputDrain(rec)
		close(run.done)
		m.accountSwitchMu.Lock()
		if m.accountSwitches == nil {
			m.accountSwitches = make(map[domain.SessionID]*accountsManagerSwitchRun)
		}
		m.accountSwitches[op.SessionID] = run
		m.accountSwitchMu.Unlock()
	}
	if len(ops) > 0 {
		if router, ok := m.accountsManager.(ports.AccountsManagerSwitchRouter); ok {
			if err := router.SynchronizeAgentBindings(ctx); err != nil {
				// The runner's lease also fails closed while it cannot reconcile.
				m.logger.Warn("managed account recovery bindings await runner")
			}
		}
	}
	return nil
}
