package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const agentOperationAccountRemoval agentOperationKind = "managed_account_removal"

type accountsManagerRemovalRun struct {
	op          domain.AccountsManagerRemoval
	cancel      context.CancelFunc
	done        chan struct{}
	running     bool
	cancelled   bool
	gates       map[domain.SessionID]func()
	releaseOnce sync.Once
}

type accountsManagerRemovalChat interface {
	ArmAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
	StopAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
	AbortAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
}

// StartAccountsManagerRemoval requires impact confirmation before fencing session intake.
func (m *Manager) StartAccountsManagerRemoval(ctx context.Context, id, accountID string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, error) {
	store, ok := m.store.(ports.AccountsManagerRemovalStore)
	router, routed := m.accountsManager.(ports.AccountsManagerRemovalRouter)
	if !ok || !routed {
		return domain.AccountsManagerRemoval{}, ErrInterfaceHandoffUnsupported
	}
	op, created, err := router.PrepareAccountRemoval(ctx, id, accountID, revision, confirmed)
	if err != nil || !created {
		return op, err
	}
	return op, m.launchAccountsManagerRemoval(store, router, op)
}

// RetryAccountsManagerRemoval preserves captured owners instead of recapturing replacements.
func (m *Manager) RetryAccountsManagerRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, error) {
	store, ok := m.store.(ports.AccountsManagerRemovalStore)
	router, routed := m.accountsManager.(ports.AccountsManagerRemovalRouter)
	if !ok || !routed {
		return domain.AccountsManagerRemoval{}, ErrInterfaceHandoffUnsupported
	}
	op, found, err := store.GetAccountsManagerRemoval(ctx, id)
	if err != nil || !found {
		return op, errors.Join(err, domain.ErrAccountsManagerRemovalConflict)
	}
	if op.Phase.Terminal() {
		return op, domain.ErrAccountsManagerRemovalConflict
	}
	return op, m.launchAccountsManagerRemoval(store, router, op)
}

func (m *Manager) launchAccountsManagerRemoval(store ports.AccountsManagerRemovalStore, router ports.AccountsManagerRemovalRouter, op domain.AccountsManagerRemoval) error {
	if err := m.beginAgentSwitchAttempt(); err != nil {
		return err
	}
	m.accountRemovalMu.Lock()
	if m.accountRemovals == nil {
		m.accountRemovals = make(map[string]*accountsManagerRemovalRun)
	}
	run := m.accountRemovals[op.ID]
	if run != nil && (run.running || run.cancelled) {
		m.accountRemovalMu.Unlock()
		m.agentSwitchWorkers.Done()
		return domain.ErrAccountsManagerRemovalConflict
	}
	current, found, err := store.GetAccountsManagerRemoval(m.backgroundContext, op.ID)
	if err != nil || !found || current.Phase.Terminal() {
		m.accountRemovalMu.Unlock()
		m.agentSwitchWorkers.Done()
		return errors.Join(domain.ErrAccountsManagerRemovalConflict, err)
	}
	if run == nil {
		run = &accountsManagerRemovalRun{op: current, gates: make(map[domain.SessionID]func())}
		m.accountRemovals[op.ID] = run
	}
	ctx, cancel := context.WithCancel(m.backgroundContext)
	run.running, run.cancel, run.done = true, cancel, make(chan struct{})
	m.accountRemovalMu.Unlock()
	go func() {
		code := m.executeAccountsManagerRemoval(ctx, store, router, op.ID, run)
		m.finishAccountsManagerRemoval(store, op.ID, run, code)
	}()
	return nil
}

func (m *Manager) reserveAccountRemoval(ctx context.Context, op domain.AccountsManagerRemoval, run *accountsManagerRemovalRun) error {
	for _, entry := range op.Impact.Sessions {
		if !entry.OwnsController() {
			continue
		}
		if _, found := run.gates[entry.SessionID]; found {
			continue
		}
		if err := m.beginAgentOperation(ctx, entry.SessionID, agentOperationAccountRemoval); err != nil {
			return err
		}
		rec, err := m.getRecord(ctx, entry.SessionID)
		if err != nil {
			m.endAgentOperation(entry.SessionID, agentOperationAccountRemoval)
			return err
		}
		_, release := m.beginTerminalInputDrain(rec)
		run.gates[entry.SessionID] = release
	}
	for _, entry := range op.Impact.Sessions {
		if !entry.OwnsController() || entry.Owner.Mode != domain.SessionModeChat {
			continue
		}
		chat, ok := m.chat.(accountsManagerRemovalChat)
		if !ok {
			return ErrInterfaceHandoffUnsupported
		}
		if err := chat.ArmAccountsManagerRemoval(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) executeAccountsManagerRemoval(ctx context.Context, store ports.AccountsManagerRemovalStore, router ports.AccountsManagerRemovalRouter, id string, run *accountsManagerRemovalRun) string {
	op, found, err := store.GetAccountsManagerRemoval(ctx, id)
	if err != nil || !found || op.Phase.Terminal() {
		return "ADMISSION_CHANGED"
	}
	if err := m.reserveAccountRemoval(ctx, op, run); err != nil {
		return "SESSION_INTAKE_UNAVAILABLE"
	}
	if err := store.BeginAccountsManagerRemovalStop(ctx, id); err != nil {
		return "STOP_ADMISSION_CHANGED"
	}
	step, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := router.SynchronizeAgentBindings(step); err != nil {
		return "REVOCATION_UNCONFIRMED"
	}
	if err := store.RecordAccountsManagerRemovalBindingsRevoked(step, id); err != nil {
		return "REVOCATION_RECORD_UNCONFIRMED"
	}
	stopped := make(map[domain.SessionID]bool)
	for _, entry := range op.Impact.Sessions {
		if stopped[entry.SessionID] {
			continue
		}
		if err := store.BeginAccountsManagerRemovalStop(step, id); err != nil {
			return "STOP_ADMISSION_CHANGED"
		}
		for _, binding := range op.Impact.Sessions {
			if binding.SessionID == entry.SessionID {
				if err := m.stopAccountRemovalOwner(step, binding); err != nil {
					return "SOURCE_STOP_UNCONFIRMED"
				}
			}
		}
		if err := store.RecordAccountsManagerRemovalStopped(step, id, entry.SessionID); err != nil {
			return "STOP_RECORD_UNCONFIRMED"
		}
		stopped[entry.SessionID] = true
	}
	if err := store.BeginAccountsManagerRemovalStop(step, id); err != nil {
		return "FINAL_ADMISSION_CHANGED"
	}
	if err := router.FinalizeAccountRemoval(step, id); err != nil {
		return "CREDENTIAL_REMOVAL_UNCONFIRMED"
	}
	return ""
}

func (m *Manager) stopAccountRemovalOwner(ctx context.Context, entry domain.AccountsManagerRemovalSession) error {
	if !entry.OwnsController() {
		return nil
	}
	rec, err := m.getRecord(ctx, entry.SessionID)
	if err != nil {
		return err
	}
	if rec.ControllerOwner() != entry.Owner || rec.Metadata.RuntimeHandleID != entry.RuntimeHandleID {
		return domain.ErrAccountsManagerRemovalConflict
	}
	if entry.Owner.Mode == domain.SessionModeChat {
		chat, ok := m.chat.(accountsManagerRemovalChat)
		if !ok {
			return ErrInterfaceHandoffUnsupported
		}
		return chat.StopAccountsManagerRemoval(ctx, entry)
	}
	handles, err := m.accountSwitchLaunchHandles(entry.SessionID)
	if err != nil {
		return err
	}
	refs := make([]ports.FencedRuntimeRef, 0, len(handles)+1)
	seen := make(map[string]bool)
	if entry.RuntimeHandleID != "" {
		handles = append(handles, ports.RuntimeHandle{ID: entry.RuntimeHandleID})
	}
	for _, handle := range handles {
		if seen[handle.ID] {
			continue
		}
		seen[handle.ID] = true
		generation := entry.Owner.RuntimeLaunchID
		if generation == "" {
			// This value may prove absence only; it never authorizes a stop.
			generation = "unrecorded-removal-owner"
		}
		ref := ports.FencedRuntimeRef{SessionID: entry.SessionID, Handle: handle, Generation: generation}
		probe := m.runtime.ProbeFencedRuntime(ctx, ref)
		replaced := entry.Owner.RuntimeLaunchID != "" && accountRemovalOwnerReplaced(probe)
		if !replaced && probe.Liveness != ports.FencedDead && (probe.Liveness != ports.FencedAlive || entry.Owner.RuntimeLaunchID == "") {
			return ErrSwitchSourceStopUnconfirmed
		}
		refs = append(refs, ref)
	}
	for _, ref := range refs {
		if entry.Owner.RuntimeLaunchID == "" {
			if m.runtime.ProbeFencedRuntime(ctx, ref).Liveness != ports.FencedDead {
				return ErrSwitchSourceStopUnconfirmed
			}
			continue
		}
		if err := m.stopAccountRemovalRuntime(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

func accountRemovalOwnerReplaced(probe ports.FencedProbeResult) bool {
	return probe.Liveness == ports.FencedUnknown && probe.Reason == ports.FencedReasonGenerationMismatch
}

func (m *Manager) stopAccountRemovalRuntime(ctx context.Context, ref ports.FencedRuntimeRef) error {
	var stopErr error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(ErrSwitchSourceStopUnconfirmed, stopErr, err)
		}
		probe := m.runtime.ProbeFencedRuntime(ctx, ref)
		// A replacement proves only that the recorded owner left this slot.
		// It never grants permission to signal or destroy the new owner.
		if accountRemovalOwnerReplaced(probe) {
			return nil
		}
		if probe.Liveness == ports.FencedDead {
			present, err := m.runtime.IsAlive(ctx, ref.Handle)
			if errors.Is(err, ports.ErrRuntimeUnavailable) || (err == nil && !present) {
				return nil
			}
			if err != nil {
				return errors.Join(ErrSwitchSourceStopUnconfirmed, stopErr, err)
			}
			// Exited supervised panes can retain a terminal host. Reprove the
			// generation after the slot-existence check before removing it.
			probe = m.runtime.ProbeFencedRuntime(ctx, ref)
			if accountRemovalOwnerReplaced(probe) {
				return nil
			}
		}
		if (probe.Liveness != ports.FencedAlive && probe.Liveness != ports.FencedDead) || attempt == 2 {
			return errors.Join(ErrSwitchSourceStopUnconfirmed, stopErr)
		}
		stopErr = m.runtime.Destroy(ctx, ref.Handle)
	}
}

func (m *Manager) finishAccountsManagerRemoval(store ports.AccountsManagerRemovalStore, id string, run *accountsManagerRemovalRun, code string) {
	defer m.agentSwitchWorkers.Done()
	run.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code != "" {
		if err := store.RecordAccountsManagerRemovalFailure(ctx, id, code); err != nil {
			m.logger.Warn("account removal result remains pending", "operationID", id)
		}
	}
	op, found, err := store.GetAccountsManagerRemoval(ctx, id)
	m.accountRemovalMu.Lock()
	done := run.done
	if run.cancelled {
		op, found, err = run.op, true, nil
		op.Phase = domain.AccountsManagerRemovalCancelled
	}
	if err == nil && found && op.Phase.Terminal() {
		// Keep retry admission closed until both queue and input fences release.
		m.accountRemovalMu.Unlock()
		m.releaseAccountsManagerRemoval(op, run)
		m.accountRemovalMu.Lock()
	}
	run.running = false
	close(done)
	m.accountRemovalMu.Unlock()
}

func (m *Manager) releaseAccountsManagerRemoval(op domain.AccountsManagerRemoval, run *accountsManagerRemovalRun) {
	run.releaseOnce.Do(func() {
		if op.Phase == domain.AccountsManagerRemovalCancelled {
			for _, entry := range op.Impact.Sessions {
				if !entry.OwnsController() || entry.Owner.Mode != domain.SessionModeChat {
					continue
				}
				if chat, ok := m.chat.(accountsManagerRemovalChat); ok {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err := chat.AbortAccountsManagerRemoval(ctx, entry)
					cancel()
					if err != nil {
						m.logger.Warn("cancelled removal queue ownership changed", "sessionID", entry.SessionID)
					}
				}
			}
		}
		for id, release := range run.gates {
			if release != nil {
				release()
			}
			m.endAgentOperation(id, agentOperationAccountRemoval)
		}
		m.accountRemovalMu.Lock()
		if m.accountRemovals[op.ID] == run {
			delete(m.accountRemovals, op.ID)
		}
		m.accountRemovalMu.Unlock()
	})
}

// CancelAccountsManagerRemoval releases only a durably cancelled pre-stop operation.
func (m *Manager) CancelAccountsManagerRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, error) {
	store, ok := m.store.(ports.AccountsManagerRemovalStore)
	if !ok {
		return domain.AccountsManagerRemoval{}, ErrInterfaceHandoffUnsupported
	}
	m.accountRemovalMu.Lock()
	if err := store.CancelAccountsManagerRemoval(ctx, id); err != nil {
		m.accountRemovalMu.Unlock()
		return domain.AccountsManagerRemoval{}, err
	}
	run := m.accountRemovals[id]
	settled := run != nil && !run.running
	if run != nil {
		run.cancelled = true
		if run.cancel != nil {
			run.cancel()
		}
	}
	m.accountRemovalMu.Unlock()
	if settled {
		op := run.op
		op.Phase = domain.AccountsManagerRemovalCancelled
		m.releaseAccountsManagerRemoval(op, run)
	}
	op, found, err := store.GetAccountsManagerRemoval(ctx, id)
	if err != nil || !found {
		return op, errors.Join(err, domain.ErrAccountsManagerRemovalConflict)
	}
	return op, nil
}

func (m *Manager) reconcileAccountsManagerRemovals(ctx context.Context) error {
	store, ok := m.store.(ports.AccountsManagerRemovalStore)
	if !ok {
		return nil
	}
	ops, err := store.ListActiveAccountsManagerRemovals(ctx)
	if err != nil {
		return err
	}
	for _, op := range ops {
		m.accountRemovalMu.Lock()
		if m.accountRemovals == nil {
			m.accountRemovals = make(map[string]*accountsManagerRemovalRun)
		}
		run := m.accountRemovals[op.ID]
		if run == nil {
			run = &accountsManagerRemovalRun{op: op, gates: make(map[domain.SessionID]func()), done: make(chan struct{}), cancel: func() {}}
			close(run.done)
			m.accountRemovals[op.ID] = run
		}
		m.accountRemovalMu.Unlock()
		if err := m.reserveAccountRemoval(ctx, op, run); err != nil {
			return err
		}
	}
	return nil
}
