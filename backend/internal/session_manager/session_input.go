package sessionmanager

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type agentOperationKind string

const (
	agentOperationExit              agentOperationKind = "exit"
	agentOperationResume            agentOperationKind = "resume"
	agentOperationHibernate         agentOperationKind = "hibernate"
	agentOperationKill              agentOperationKind = "kill"
	agentOperationRestore           agentOperationKind = "restore"
	agentOperationRetire            agentOperationKind = "retire"
	agentOperationReconcile         agentOperationKind = "reconcile"
	agentOperationInterfaceRecovery agentOperationKind = "interface_recovery"
	agentOperationProviderSwitch    agentOperationKind = "provider_switch"
)

var errAgentOperationInProgress = errors.New("session: another exclusive operation is in progress")

var _ sessionguard.InputLease = (*Manager)(nil)

// AcquireSessionInput atomically admits one pane write unless an exclusive
// operation already owns the AO session. The returned release is idempotent;
// callers hold it through the underlying pane write so a later mutation can
// close admission and wait for every already-admitted write to finish.
func (m *Manager) AcquireSessionInput(id domain.SessionID) (release func(), ok bool) {
	id = domain.SessionID(strings.TrimSpace(string(id)))
	m.agentOpMu.Lock()
	if m.agentOperationActiveLocked(id) {
		m.agentOpMu.Unlock()
		return nil, false
	}
	if m.inputLeases[id] == 0 {
		m.inputDrained[id] = make(chan struct{})
		if m.inputLeaseSince == nil {
			m.inputLeaseSince = make(map[domain.SessionID]time.Time)
		}
		m.inputLeaseSince[id] = time.Now()
	}
	m.inputLeases[id]++
	m.agentOpMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { m.releaseSessionInput(id) })
	}, true
}

func (m *Manager) releaseSessionInput(id domain.SessionID) {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	count := m.inputLeases[id]
	if count <= 1 {
		delete(m.inputLeases, id)
		delete(m.inputLeaseSince, id)
		if drained := m.inputDrained[id]; drained != nil {
			close(drained)
			delete(m.inputDrained, id)
		}
		return
	}
	m.inputLeases[id] = count - 1
}

// SessionMutationInProgress is consumed by observation-driven lifecycle paths
// that must not independently terminate a session while AO is replacing or
// relaunching its provider process. A live interface or account transition is
// one: between its source stop and the target's spawn the session's runtime is
// dead by design, and a same-mode move stays probeable all through it.
func (m *Manager) SessionMutationInProgress(id domain.SessionID) bool {
	id = domain.SessionID(strings.TrimSpace(string(id)))
	m.agentOpMu.Lock()
	active := m.agentOperationActiveLocked(id)
	m.agentOpMu.Unlock()
	if active {
		return true
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	_, live := m.transitions[id]
	return live
}

func (m *Manager) agentOperationActiveLocked(id domain.SessionID) bool {
	_, ok := m.agentOperations[id]
	_, deferred := m.deferredInterfaceRecovery[id]
	return ok || deferred
}

// beginAgentOperation closes input admission before waiting for already-issued
// leases. Because both actions share agentOpMu, a pane write is either fully
// admitted before the operation (and drained) or rejected after it; there is
// no interval in which it can pass a boolean check and write into the new
// provider generation.
func (m *Manager) beginAgentOperation(ctx context.Context, id domain.SessionID, kind agentOperationKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var drained <-chan struct{}
	for {
		m.agentOpMu.Lock()
		if m.agentOperationActiveLocked(id) {
			current := m.agentOperations[id]
			m.agentOpMu.Unlock()
			if current != agentOperationHibernate || kind == agentOperationHibernate {
				return errAgentOperationInProgress
			}
			// Background cleanup yields to every explicit operation once its
			// bounded shutdown completes; it must not make Kill/Switch fail.
			select {
			case <-ctx.Done():
				m.agentWaitLogger().Warn("agent operation gave up waiting for hibernation to finish",
					"sessionID", id, "operation", kind, "error", ctx.Err())
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		m.agentOperations[id] = kind
		drained = m.inputDrained[id]
		m.agentOpMu.Unlock()
		break
	}
	if drained == nil {
		return nil
	}
	waitStarted := time.Now()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		// A lease that outlives the whole wait usually belongs to a write that
		// is itself waiting on this operation, such as a relay into a sleeping
		// Chat session that has to wake it first.
		m.agentOpMu.Lock()
		leases := m.inputLeases[id]
		var heldFor time.Duration
		if since, ok := m.inputLeaseSince[id]; ok {
			heldFor = time.Since(since).Round(time.Millisecond)
		}
		m.agentOpMu.Unlock()
		m.endAgentOperation(id, kind)
		m.agentWaitLogger().Warn("agent operation gave up waiting for session input to drain",
			"sessionID", id, "operation", kind, "waited", time.Since(waitStarted).Round(time.Millisecond),
			"inputLeases", leases, "oldestLeaseHeldFor", heldFor, "error", ctx.Err())
		return ctx.Err()
	}
}

// Some tests build a Manager without New, so the logger may be unset.
func (m *Manager) agentWaitLogger() *slog.Logger {
	if m.logger == nil {
		return slog.Default()
	}
	return m.logger
}

// beginAgentOperations reserves every currently-unowned session before waiting
// for any admitted input to drain. Startup reconciliation uses this batch form
// so candidates queued behind its worker limit are fenced just as early as the
// candidates already being probed.
func (m *Manager) beginAgentOperations(ctx context.Context, ids []domain.SessionID, kind agentOperationKind) ([]domain.SessionID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type reservation struct {
		id      domain.SessionID
		drained <-chan struct{}
	}
	reservations := make([]reservation, 0, len(ids))
	m.agentOpMu.Lock()
	for _, id := range ids {
		id = domain.SessionID(strings.TrimSpace(string(id)))
		if id == "" || m.agentOperationActiveLocked(id) {
			continue
		}
		m.agentOperations[id] = kind
		reservations = append(reservations, reservation{id: id, drained: m.inputDrained[id]})
	}
	m.agentOpMu.Unlock()

	for _, reservation := range reservations {
		if reservation.drained == nil {
			continue
		}
		select {
		case <-reservation.drained:
		case <-ctx.Done():
			for _, reserved := range reservations {
				m.endAgentOperation(reserved.id, kind)
			}
			return nil, ctx.Err()
		}
	}
	acquired := make([]domain.SessionID, 0, len(reservations))
	for _, reservation := range reservations {
		acquired = append(acquired, reservation.id)
	}
	return acquired, nil
}

func (m *Manager) endAgentOperation(id domain.SessionID, kind agentOperationKind) {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	if current, ok := m.agentOperations[id]; ok && current == kind {
		delete(m.agentOperations, id)
		m.resumeDeferredInterfaceRecoveryLocked(id)
	}
}

// The deferred fence survives the foreign operation's release. Recovery, not
// that operation, is responsible for reopening input after its durable commit.
func (m *Manager) resumeDeferredInterfaceRecoveryLocked(id domain.SessionID) {
	transitionID, deferred := m.deferredInterfaceRecovery[id]
	if !deferred {
		return
	}
	if err := m.beginAgentSwitchAttempt(); err != nil {
		return // Preserve the fence for the next boot after shutdown admission closes.
	}
	go func() {
		defer m.agentSwitchWorkers.Done()
		recovered, err := m.recoverInterfaceTransitions(m.backgroundContext, transitionID)
		if err != nil {
			m.logger.Error("interface transition: deferred recovery failed", "sessionID", id, "error", err)
		} else if len(recovered) == 0 {
			// The exact obligation was cancelled/settled before the worker read it.
			m.agentOpMu.Lock()
			if m.deferredInterfaceRecovery[id] == transitionID {
				delete(m.deferredInterfaceRecovery, id)
			}
			m.agentOpMu.Unlock()
		}
	}()
}

func (m *Manager) beginAgentResume(ctx context.Context, id domain.SessionID) error {
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err := m.beginAgentOperation(waitCtx, id, agentOperationResume)
	if errors.Is(err, errAgentOperationInProgress) {
		return ErrResumeInProgress
	}
	return err
}

func (m *Manager) endAgentResume(id domain.SessionID) {
	m.endAgentOperation(id, agentOperationResume)
}
