package sessionmanager

import (
	"context"
	"errors"
	"fmt"
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

// agentOperationHolder records which exclusive operation owns a session and
// when it started, so a refusal can name the holder and a wedged one can be
// recognised as stale instead of refusing every later operation forever.
type agentOperationHolder struct {
	kind      agentOperationKind
	startedAt time.Time
}

// defaultAgentOperationStaleAfter bounds how long an exclusive operation may
// hold a session before later callers treat it as stale. Every operation kind
// is bounded well inside it (wake 60s, interface-transition steps 45s, a whole
// terminal switch 135s), so a holder this old has lost its owner — a process
// that died mid-operation, or a lease its release never reached.
const defaultAgentOperationStaleAfter = 10 * time.Minute

var errAgentOperationInProgress = errors.New("session: another exclusive operation is in progress")

// AgentOperationBusyError is a refusal that names the operation holding the
// session, so a client can offer cancel/wait instead of a bare busy. It
// unwraps to errAgentOperationInProgress, keeping every existing errors.Is
// refusal check working.
type AgentOperationBusyError struct {
	Kind      agentOperationKind
	StartedAt time.Time
}

func (e *AgentOperationBusyError) Error() string {
	if e == nil || e.Kind == "" {
		if e != nil && !e.StartedAt.IsZero() {
			return errAgentOperationInProgress.Error() + " (held since " + e.StartedAt.Format(time.RFC3339) + ")"
		}
		return errAgentOperationInProgress.Error()
	}
	message := errAgentOperationInProgress.Error() + " (" + string(e.Kind)
	if !e.StartedAt.IsZero() {
		message += " held since " + e.StartedAt.Format(time.RFC3339)
	}
	return message + ")"
}

func (e *AgentOperationBusyError) Unwrap() error { return errAgentOperationInProgress }

// agentOperationBusyDetail renders a busy refusal for a provider switch: it
// names the holder and when it started so the UI can offer cancel/wait.
func agentOperationBusyDetail(err error) string {
	var busy *AgentOperationBusyError
	if !errors.As(err, &busy) || busy == nil || busy.Kind == "" {
		return "another session operation is in progress"
	}
	detail := "another session operation is in progress: " + string(busy.Kind)
	if !busy.StartedAt.IsZero() {
		detail += fmt.Sprintf(" held since %s (running for %s)", busy.StartedAt.Format("15:04:05"), time.Since(busy.StartedAt).Round(time.Second))
	}
	return detail
}

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

// operationClock tolerates a bare test Manager: the clock is always wired in
// production, but the lease helpers must not segfault without it.
func (m *Manager) operationClock() time.Time {
	if m.clock == nil {
		return time.Now()
	}
	return m.clock()
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
			holder := m.agentOperations[id]
			m.agentOpMu.Unlock()
			if m.agentOperationStale(holder) && m.reapStaleAgentOperation(id, holder) {
				continue
			}
			if holder.kind != agentOperationHibernate || kind == agentOperationHibernate {
				return &AgentOperationBusyError{Kind: holder.kind, StartedAt: holder.startedAt}
			}
			// Background cleanup yields to every explicit operation once its
			// bounded shutdown completes; it must not make Kill/Switch fail.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		m.agentOperations[id] = agentOperationHolder{kind: kind, startedAt: m.operationClock()}
		drained = m.inputDrained[id]
		m.agentOpMu.Unlock()
		break
	}
	if drained == nil {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		m.endAgentOperation(id, kind)
		return ctx.Err()
	}
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
		m.agentOperations[id] = agentOperationHolder{kind: kind, startedAt: m.operationClock()}
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
	if current, ok := m.agentOperations[id]; ok && current.kind == kind {
		delete(m.agentOperations, id)
		m.resumeDeferredInterfaceRecoveryLocked(id)
	}
}

// agentOperationStale reports whether an exclusive-operation holder has run
// past every budget its kind is given. Only an owner that can no longer be
// alive — a process that died mid-operation, or a release that never ran —
// gets there; a live operation is named in the refusal instead.
func (m *Manager) agentOperationStale(holder agentOperationHolder) bool {
	if holder.kind == "" || holder.startedAt.IsZero() {
		return false
	}
	limit := m.operationStaleAfter
	if limit <= 0 {
		limit = defaultAgentOperationStaleAfter
	}
	return m.operationClock().Sub(holder.startedAt) > limit
}

// reapStaleAgentOperation releases a stale holder so the session stops
// refusing every exclusive operation forever. The compare under the lock keeps
// a finished operation's own release, or a newer operation that reused the
// slot, from being reaped in the stale holder's place.
func (m *Manager) reapStaleAgentOperation(id domain.SessionID, holder agentOperationHolder) bool {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	if current, ok := m.agentOperations[id]; !ok || current != holder {
		return false
	}
	delete(m.agentOperations, id)
	m.logger.Warn("session: reaped stale exclusive operation",
		"sessionID", id, "operation", holder.kind,
		"startedAt", holder.startedAt.Format(time.RFC3339),
		"age", m.operationClock().Sub(holder.startedAt).Round(time.Second))
	m.resumeDeferredInterfaceRecoveryLocked(id)
	return true
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
