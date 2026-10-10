package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// chatRestarter stops a quiet Chat provider and starts it again.
type chatRestarter interface {
	HibernateChatForRestart(context.Context, domain.SessionID) (bool, error)
	WakeChat(context.Context, domain.SessionID) error
}

func (m *Manager) applyAccountEnv(ctx context.Context, id domain.SessionID, env map[string]string) error {
	if m.accounts == nil {
		return nil
	}
	managed, err := m.accounts.LaunchAccountEnv(ctx, id)
	for key, value := range managed {
		env[key] = value
	}
	return err
}

// accountManaged reports a session whose sign-in Account Manager supplies.
func (m *Manager) accountManaged(ctx context.Context, id domain.SessionID) (bool, error) {
	if m.accounts == nil {
		return false, nil
	}
	_, managed, err := m.accounts.SessionAccount(ctx, id)
	return managed, err
}

func (m *Manager) forgetAccount(ctx context.Context, id domain.SessionID) error {
	if m.accounts == nil {
		return nil
	}
	return m.accounts.ForgetAccount(ctx, id)
}

// RelatedAccountEnv gives a same-provider reviewer its owning worker's ticket.
func (m *Manager) RelatedAccountEnv(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (map[string]string, error) {
	if m.accounts == nil {
		return nil, nil
	}
	route, managed, err := m.accounts.SessionAccount(ctx, id)
	if err != nil || !managed || route.Provider != domain.AccountProvider(harness) {
		return nil, err
	}
	if route.AccountID == "" {
		return nil, ports.ErrProviderLoginRequired
	}
	return m.accounts.LaunchAccountEnv(ctx, id)
}

// agentSwitching reports a session changing agent: it has no settled provider.
func (m *Manager) agentSwitching(ctx context.Context, id domain.SessionID) (bool, error) {
	store, ok := m.store.(ports.AgentSwitchStore)
	if !ok {
		return false, nil
	}
	_, active, err := store.GetActiveAgentSwitch(ctx, id)
	return active, err
}

// adoptLegacyChat gives a chat that predates Account Manager its provider's
// default account. It runs only as the chat's provider process is launched.
func (m *Manager) adoptLegacyChat(ctx context.Context, rec domain.SessionRecord) {
	if m.accounts == nil || domain.AccountProvider(rec.Harness) == "" {
		return
	}
	if switching, err := m.agentSwitching(ctx, rec.ID); err != nil || switching {
		return
	}
	if _, err := m.accounts.AdoptSession(ctx, rec.ID, rec.Harness); err != nil {
		m.logger.Warn("chat left on this computer's own sign-in", "session", rec.ID, "error", err)
	}
}

// legacyCandidate reports a session whose process may still need an account.
func legacyCandidate(rec domain.SessionRecord) bool {
	return !rec.IsTerminated && rec.HibernatedAt == nil && domain.AccountProvider(rec.Harness) != ""
}

// MigrateLegacySessions restarts, four at a time, the idle sessions that still run
// without an account, and reports how many keep running without one. A chat
// that is asleep or stopped is adopted when it next starts and is not counted.
func (m *Manager) MigrateLegacySessions(ctx context.Context) (int, error) {
	if m.accounts == nil {
		return 0, nil
	}
	sessions, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return 0, err
	}
	remaining := 0
	var failures []error
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, rec := range sessions {
		if !legacyCandidate(rec) {
			continue
		}
		wg.Go(func() {
			left, err := m.moveLegacySession(ctx, rec.ID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Errorf("move session %s onto its account: %w", rec.ID, err))
			}
			if left {
				remaining++
			}
		})
	}
	wg.Wait()
	return remaining, errors.Join(failures...)
}

// SessionTurnEnded moves such a session as its turn ends, ahead of the next check.
func (m *Manager) SessionTurnEnded(rec domain.SessionRecord) {
	if m.accounts == nil || !legacyCandidate(rec) || m.beginAgentSwitchAttempt() != nil {
		return
	}
	go func() {
		defer m.agentSwitchWorkers.Done()
		ctx, cancel := context.WithTimeout(m.backgroundContext, 2*time.Minute)
		defer cancel()
		if managed, err := m.accountManaged(ctx, rec.ID); err != nil || managed {
			return
		}
		if _, err := m.moveLegacySession(ctx, rec.ID); err != nil {
			m.logger.Warn("moving a session onto Account Manager", "session", rec.ID, "error", err)
		}
	}()
}

// moveLegacySession restarts one session onto an account if it runs without one and
// is idle now, a terminal also off screen. It reports whether it is still to be moved.
func (m *Manager) moveLegacySession(ctx context.Context, id domain.SessionID) (bool, error) {
	if _, moving := m.legacyMoving.LoadOrStore(id, struct{}{}); moving {
		return true, nil
	}
	defer m.legacyMoving.Delete(id)
	if err := m.legacySlots.Acquire(ctx, 1); err != nil {
		return true, err
	}
	defer m.legacySlots.Release(1)
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !legacyCandidate(rec) {
		return err != nil, err
	}
	if rec.Activity.State == domain.ActivityExited {
		// Only a terminal this move exited and could not resume is started again.
		if launch, _ := m.legacyExited.Load(id); launch != any(rec.Metadata.RuntimeLaunchID) {
			return false, nil
		}
		_, err = m.ResumeAgentWithMode(ctx, id)
		return err != nil, err
	}
	chatMode := domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
	if _, down := m.legacyExited.Load(id); down && chatMode {
		// A chat this move stopped and could not start is started again at each check.
		if !m.chat.HasLiveChatController(id) {
			if _, err = m.ResumeAgentWithMode(ctx, id); err != nil {
				return true, err
			}
		}
		m.legacyExited.Delete(id)
		return false, nil
	}
	if managed, err := m.accountManaged(ctx, id); err != nil || managed {
		return err != nil, err
	}
	if chatMode {
		chat, ok := m.chat.(chatRestarter)
		if !ok || !m.chat.HasLiveChatController(id) {
			return true, nil
		}
		moved, err := m.restartLegacyChat(ctx, chat, id)
		return !moved, err
	}
	if rec.Activity.State != domain.ActivityIdle || rec.Metadata.RuntimeLaunchID == "" ||
		rec.Metadata.AgentSessionID == "" || m.terminalOnScreen(rec) {
		return true, nil
	}
	if _, release := m.beginTerminalInputDrain(rec); release != nil {
		defer release() // keystrokes stay closed until the agent has resumed
	}
	exit := func(ctx context.Context) (bool, error) { return m.exitLegacyTerminal(ctx, rec) }
	if exited, err := m.stopLegacySession(ctx, id, exit); err != nil || !exited {
		return true, err
	}
	_, err = m.ResumeAgentWithMode(ctx, id)
	return err != nil, err
}

// restartLegacyChat restarts one chat if it is doing nothing, and reports
// whether it now has an account. A failed start is tried again at each check.
func (m *Manager) restartLegacyChat(ctx context.Context, chat chatRestarter, id domain.SessionID) (bool, error) {
	stop := func(ctx context.Context) (bool, error) { return chat.HibernateChatForRestart(ctx, id) }
	if stopped, err := m.stopLegacySession(ctx, id, stop); err != nil || !stopped {
		return false, err
	}
	if err := chat.WakeChat(ctx, id); err != nil {
		m.legacyExited.Store(id, true)
		return false, err
	}
	return m.accountManaged(ctx, id)
}

// exitLegacyTerminal gives a terminal its account and exits its agent, unless a
// second reading, taken once input is closed, finds that a turn has started.
func (m *Manager) exitLegacyTerminal(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	if err := sleepContext(ctx, m.interfaceTransition.idleSettle); err != nil {
		return false, err
	}
	current, err := m.getRecord(ctx, rec.ID)
	if err != nil || current.IsTerminated || current.Activity.State != domain.ActivityIdle ||
		current.Metadata.RuntimeLaunchID != rec.Metadata.RuntimeLaunchID {
		return false, err
	}
	if _, err = m.accounts.AdoptSession(ctx, rec.ID, rec.Harness); err != nil {
		return false, err
	}
	if err = m.stopAgentController(ctx, current); err == nil {
		err = m.recordAgentExited(ctx, current)
	}
	if err != nil {
		_ = m.forgetAccount(context.WithoutCancel(ctx), rec.ID) // its agent keeps its own sign-in
		return false, err
	}
	m.legacyExited.Store(rec.ID, current.Metadata.RuntimeLaunchID)
	return true, nil
}

// terminalOnScreen reports a terminal that some client is showing.
func (m *Manager) terminalOnScreen(rec domain.SessionRecord) bool {
	m.terminalInputGateMu.Lock()
	defer m.terminalInputGateMu.Unlock()
	return m.terminalInputGate != nil && m.terminalInputGate.TerminalOnScreen(rec.Metadata.RuntimeHandleID)
}

// stopLegacySession runs stop while nothing else may operate on the session.
func (m *Manager) stopLegacySession(ctx context.Context, id domain.SessionID, stop func(context.Context) (bool, error)) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := m.beginAgentOperation(ctx, id, agentOperationHibernate); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return false, nil
		}
		return false, err
	}
	defer m.endAgentOperation(id, agentOperationHibernate)
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil || active {
		return false, err
	}
	if switching, err := m.agentSwitching(ctx, id); err != nil || switching {
		return false, err
	}
	return stop(ctx)
}
