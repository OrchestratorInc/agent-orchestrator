package sessionmanager

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RetryAccountsManagerSwitch recovers only the recorded session and operation.
func (m *Manager) RetryAccountsManagerSwitch(ctx context.Context, id domain.SessionID, operationID string) (domain.AccountsManagerSwitch, error) {
	store, ok := m.store.(ports.AccountsManagerSwitchStore)
	router, routed := m.accountsManager.(ports.AccountsManagerSwitchRouter)
	if !ok || !routed {
		return domain.AccountsManagerSwitch{}, ErrInterfaceHandoffUnsupported
	}
	op, found, err := store.GetAccountsManagerSwitch(ctx, operationID)
	if err != nil {
		return op, err
	}
	if !found || op.SessionID != id {
		return op, ErrNotFound
	}
	if op.Phase != domain.AccountsManagerSwitchRecoveryRequired && op.Phase != domain.AccountsManagerSwitchRequested && op.Phase != domain.AccountsManagerSwitchWaiting {
		return op, domain.ErrAccountsManagerSwitchConflict
	}
	if err := m.beginAgentSwitchAttempt(); err != nil {
		return op, err
	}
	m.accountSwitchMu.Lock()
	run := m.accountSwitches[id]
	if run == nil || run.id != operationID || run.running || run.cancelled {
		m.accountSwitchMu.Unlock()
		m.agentSwitchWorkers.Done()
		return op, domain.ErrAccountsManagerSwitchConflict
	}
	workerCtx, cancel := context.WithCancel(m.backgroundContext)
	run.running, run.cancel, run.done = true, cancel, make(chan struct{})
	m.accountSwitchMu.Unlock()
	go func() {
		code := m.recoverAccountSwitch(workerCtx, store, router, op, run)
		m.finishAccountSwitchRun(store, op, run, code)
	}()
	return op, nil
}

func (m *Manager) recoverAccountSwitch(ctx context.Context, store ports.AccountsManagerSwitchStore, router ports.AccountsManagerSwitchRouter, op domain.AccountsManagerSwitch, run *accountsManagerSwitchRun) string {
	rec, err := m.getRecord(ctx, op.SessionID)
	if err != nil {
		return "SESSION_UNAVAILABLE"
	}
	if err := router.ValidateAgentAccountTarget(ctx, op.TargetMode, op.Provider, op.TargetAccountID, rec.Metadata.Model); err != nil {
		return "TARGET_REVALIDATION_UNAVAILABLE"
	}
	if op.TargetRevision == 0 {
		if op.Phase == domain.AccountsManagerSwitchRequested {
			op, err = store.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, domain.AccountsManagerSwitchWaiting, "")
			if err != nil {
				return "ADMISSION_CHANGED"
			}
		}
		rec, err = m.accountSwitchSource(ctx, op)
		if err != nil {
			return "SOURCE_CHANGED"
		}
		if rec.Mode == domain.SessionModeChat {
			chat, ok := m.chat.(accountsManagerChatHandoff)
			if !ok {
				return "SOURCE_INTAKE_UNAVAILABLE"
			}
			if err := chat.ArmAccountsManagerHandoff(ctx, rec.ID, op.NewConversation); err != nil {
				return "SOURCE_INTAKE_UNAVAILABLE"
			}
			err = chat.PrepareAccountsManagerHandoff(ctx, rec.ID, op.Policy)
		} else if op.Phase == domain.AccountsManagerSwitchWaiting {
			// Later phases already crossed handoff; stale activity cannot justify replaying input.
			err = m.prepareAccountSwitchTerminalHandoff(ctx, rec, op.Policy, run.lastInput)
		}
		if err != nil {
			if errors.Is(err, ErrSwitchSourceStopUnconfirmed) {
				return "SOURCE_OWNERSHIP_UNCONFIRMED"
			}
			return "SOURCE_NOT_QUIESCENT"
		}
		op, err = m.prepareAccountSwitchStop(ctx, store, op, rec)
		if err != nil {
			return "ADMISSION_CHANGED"
		}
		return m.commitAndStartAccountSwitch(ctx, store, router, op)
	}
	stepCtx, cancel := context.WithTimeout(ctx, interfaceTransitionStepLimit)
	defer cancel()
	if err := router.SynchronizeAgentBindings(stepCtx); err != nil {
		return "REVOCATION_UNCONFIRMED"
	}
	owner := rec.ControllerOwner()
	if rec.IsTerminated || owner.Harness != op.SourceOwner.Harness || owner.Mode != op.SourceOwner.Mode {
		return "CONTROLLER_CHANGED"
	}
	generation, source := owner.RuntimeLaunchID, op.SourceOwner.RuntimeLaunchID
	if owner.Mode == domain.SessionModeChat {
		generation, source = owner.ControllerGeneration, op.SourceOwner.ControllerGeneration
	}
	retired := generation != "" && generation == op.RetiredTargetGeneration && rec.Metadata.RuntimeHandleID == op.RetiredTargetHandleID
	if generation != source && generation != op.TargetGeneration && !retired {
		return "CONTROLLER_CHANGED"
	}
	if owner.Mode == domain.SessionModeChat {
		if m.chat == nil {
			return "TARGET_STOP_UNCONFIRMED"
		}
		err = m.chat.StopChat(stepCtx, rec.ID)
	} else {
		err = m.retireAccountSwitchTargets(stepCtx, op, rec, generation)
	}
	if err != nil {
		return "TARGET_STOP_UNCONFIRMED"
	}
	op, err = store.RetryAccountsManagerSwitch(stepCtx, op.ID, m.newLaunchID(), owner)
	if err != nil {
		return "RETRY_COMMIT_UNCONFIRMED"
	}
	return m.startAccountSwitchTarget(stepCtx, store, router, op)
}

func (m *Manager) accountSwitchLaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	resolver, ok := m.runtime.(ports.RuntimeLaunchHandleResolver)
	if !ok {
		return nil, ErrInterfaceHandoffUnsupported
	}
	handles, err := resolver.LaunchHandles(id)
	if err != nil || len(handles) == 0 {
		return nil, ErrIncompleteHandle
	}
	seen := make(map[ports.RuntimeHandle]bool, len(handles))
	for _, handle := range handles {
		if handle.ID == "" || seen[handle] {
			return nil, ErrIncompleteHandle
		}
		seen[handle] = true
	}
	return handles, nil
}

func (m *Manager) retireAccountSwitchTargets(ctx context.Context, op domain.AccountsManagerSwitch, rec domain.SessionRecord, generation string) error {
	handles, err := m.accountSwitchLaunchHandles(rec.ID)
	if err != nil {
		return err
	}
	retired := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: runtimeHandle(rec.Metadata), Generation: generation}
	refs := make([]ports.FencedRuntimeRef, 0, len(handles)+1)
	retiredSlot := false
	for _, handle := range handles {
		ref := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: handle, Generation: op.TargetGeneration}
		retiredSlot = retiredSlot || handle == retired.Handle
		probe := m.runtime.ProbeFencedRuntime(ctx, ref)
		if probe.Liveness == ports.FencedUnknown && probe.Reason == ports.FencedReasonGenerationMismatch && ref.Handle == retired.Handle && ref.Generation != retired.Generation {
			ref = retired
			probe = m.runtime.ProbeFencedRuntime(ctx, ref)
		}
		if probe.Liveness != ports.FencedAlive && probe.Liveness != ports.FencedDead {
			return ErrSwitchSourceStopUnconfirmed
		}
		refs = append(refs, ref)
	}
	if !retiredSlot {
		probe := m.runtime.ProbeFencedRuntime(ctx, retired)
		if probe.Liveness != ports.FencedAlive && probe.Liveness != ports.FencedDead {
			return ErrSwitchSourceStopUnconfirmed
		}
		refs = append(refs, retired)
	}
	// Validate every possible slot before teardown. Re-probe each exact owner
	// during stop; uncertainty never permits rotating the durable reservation.
	for _, ref := range refs {
		if err := m.stopAccountsManagerRuntime(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) accountSwitchTargetUnambiguous(ctx context.Context, op domain.AccountsManagerSwitch, target ports.RuntimeHandle) bool {
	handles, err := m.accountSwitchLaunchHandles(op.SessionID)
	if err != nil {
		return false
	}
	found := false
	for _, handle := range handles {
		if handle == target {
			found = true
			continue
		}
		ref := ports.FencedRuntimeRef{SessionID: op.SessionID, Handle: handle, Generation: op.TargetGeneration}
		if m.runtime.ProbeFencedRuntime(ctx, ref).Liveness != ports.FencedDead {
			return false
		}
	}
	return found
}
