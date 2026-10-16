package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// applyAccountEnv adds the account of the session to a launch of its own agent,
// which runs harness: after a switch to another provider the session's account
// for that provider.
func (m *Manager) applyAccountEnv(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, env map[string]string) error {
	if m.accounts == nil {
		return nil
	}
	managed, err := m.accounts.LaunchAccountEnv(ctx, id, harness, false)
	maps.Copy(env, managed)
	return err
}

// plannedAccountEnv is what applyAccountEnv would add, with no route moved.
func (m *Manager) plannedAccountEnv(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (map[string]string, error) {
	if m.accounts == nil {
		return nil, nil
	}
	return m.accounts.PlannedAccountEnv(ctx, id, harness)
}

// sessionManaged reports a session whose sign-in Account Manager supplies.
func (m *Manager) sessionManaged(ctx context.Context, id domain.SessionID) (bool, error) {
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

// RelatedAccountEnv adds its owning session's account for harness to the
// environment of work it starts, a reviewer or a side chat: the session's own on
// the same provider, else its account for that provider, which starts as the
// provider's default. It never moves the session's own agent.
func (m *Manager) RelatedAccountEnv(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, env map[string]string) error {
	if m.accounts == nil {
		return nil
	}
	related, err := m.accounts.LaunchAccountEnv(ctx, id, harness, true)
	maps.Copy(env, related)
	return err
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
// without an account, and reports how many keep running without one. They are
// listed again whenever the providers with a default account change, so a
// sign-in moves the sessions of a provider that had none, however old they are.
// Only the account loop calls this. A chat that is asleep or stopped is adopted
// when it next starts and is not listed.
func (m *Manager) MigrateLegacySessions(ctx context.Context) (int, error) {
	if m.accounts == nil {
		return 0, nil
	}
	defaults, err := m.legacyDefaults(ctx)
	if err != nil {
		return len(m.legacyLeft), err
	}
	if m.legacyLeft == nil || defaults != m.legacyListedFor {
		sessions, err := m.store.ListAllSessions(ctx)
		if err != nil {
			return 0, err
		}
		m.legacyLeft, m.legacyListedFor = []domain.SessionID{}, defaults
		for _, rec := range sessions {
			if !legacyCandidate(rec) {
				continue
			}
			// One whose account cannot be read is listed too, to be looked at again.
			if managed, err := m.sessionManaged(ctx, rec.ID); err != nil || !managed {
				m.legacyLeft = append(m.legacyLeft, rec.ID)
			}
		}
	}
	left, failures := make([]bool, len(m.legacyLeft)), make([]error, len(m.legacyLeft))
	var wg sync.WaitGroup
	for i, id := range m.legacyLeft {
		wg.Go(func() {
			if left[i], failures[i] = m.moveLegacySession(ctx, id); failures[i] != nil {
				failures[i] = fmt.Errorf("move session %s onto its account: %w", id, failures[i])
			}
		})
	}
	wg.Wait()
	kept := m.legacyLeft[:0]
	for i, id := range m.legacyLeft {
		if left[i] {
			kept = append(kept, id)
		}
	}
	m.legacyLeft = kept
	return len(kept), errors.Join(failures...)
}

// legacyDefaults names the harnesses whose provider has a default account to move onto.
func (m *Manager) legacyDefaults(ctx context.Context) (string, error) {
	var with []string
	for _, harness := range domain.AccountHarnesses {
		ready, err := m.accounts.HasDefault(ctx, harness)
		if err != nil {
			return "", err
		}
		if ready {
			with = append(with, string(harness))
		}
	}
	return strings.Join(with, " "), nil
}

// SessionTurnEnded moves such a session as its turn ends, ahead of the next check.
func (m *Manager) SessionTurnEnded(rec domain.SessionRecord) {
	if m.accounts == nil || !legacyCandidate(rec) || m.beginAgentSwitchAttempt() != nil {
		return
	}
	ended := m.clock()
	go func() {
		defer m.agentSwitchWorkers.Done()
		ctx, cancel := context.WithTimeout(m.backgroundContext, 2*time.Minute)
		defer cancel()
		if managed, err := m.sessionManaged(ctx, rec.ID); err != nil || managed {
			return
		}
		m.legacyTurnEnded.Store(rec.ID, ended)
		if left, err := m.moveLegacySession(ctx, rec.ID); err != nil {
			m.logger.Warn("moving a session onto Account Manager", "session", rec.ID, "error", err)
		} else if left {
			m.logger.Info("session not moved onto its account at its turn end", "session", rec.ID)
		}
	}()
}

// moveLegacySession restarts one session onto an account if it runs without one and
// is idle now, a terminal also quiet. It reports whether it is still to be moved.
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
	if managed, err := m.sessionManaged(ctx, id); err != nil || managed {
		return err != nil, err
	}
	// With no default to move onto it keeps the computer's own setup; a restart would change nothing.
	if ready, err := m.accounts.HasDefault(ctx, rec.Harness); err != nil || !ready {
		return err != nil, err
	}
	if chatMode {
		chat, ok := m.chat.(interface {
			HibernateChatForRestart(context.Context, domain.SessionID) (bool, error)
			WakeChat(context.Context, domain.SessionID) error
		})
		if !ok || !m.chat.HasLiveChatController(id) {
			return true, nil
		}
		if stopped, err := m.stopLegacySession(ctx, id, chat.HibernateChatForRestart); err != nil || !stopped {
			return true, err
		}
		if err := chat.WakeChat(ctx, id); err != nil {
			m.legacyExited.Store(id, true) // its start is tried again at each check
			return true, err
		}
		managed, err := m.sessionManaged(ctx, id)
		return !managed, err
	}
	if rec.Activity.State != domain.ActivityIdle || rec.Metadata.RuntimeLaunchID == "" ||
		rec.Metadata.AgentSessionID == "" || !m.terminalQuiet(rec, m.terminalLastTyped(rec)) {
		return true, nil
	}
	if _, release := m.beginTerminalInputDrain(rec); release != nil {
		defer release() // keystrokes stay closed until the agent has resumed
	}
	if !m.terminalQuiet(rec, m.terminalLastTyped(rec)) { // text landed before input closed
		return true, nil
	}
	exit := func(ctx context.Context, _ domain.SessionID) (bool, error) { return m.exitLegacyTerminal(ctx, rec) }
	if exited, err := m.stopLegacySession(ctx, id, exit); err != nil || !exited {
		return true, err
	}
	if _, err = m.ResumeAgentWithMode(ctx, id); err != nil {
		return true, err
	}
	m.legacyTurnEnded.Delete(id)
	return false, m.reportResumedIdle(ctx, id)
}

// legacyQuietFor is how long a terminal goes without typed text before it is moved.
const legacyQuietFor = 30 * time.Second

// terminalQuiet reports a terminal nobody is typing in: no text typed for
// legacyQuietFor and none since its last turn ended, so no unsent text is lost.
// Text with no turn ended after it, in this daemon's life, counts as unsent.
func (m *Manager) terminalQuiet(rec domain.SessionRecord, lastTyped time.Time) bool {
	if lastTyped.IsZero() {
		return true
	}
	ended, _ := m.legacyTurnEnded.Load(rec.ID)
	endedAt, _ := ended.(time.Time)
	return m.clock().Sub(lastTyped) >= legacyQuietFor && lastTyped.Before(endedAt)
}

// reportResumedIdle reports the resumed agent idle, as it was before the move, so
// the terminal does not show "No signal" until its next turn. Its keystrokes are
// still closed, so no turn can have started.
func (m *Manager) reportResumedIdle(ctx context.Context, id domain.SessionID) error {
	rec, err := m.getRecord(ctx, id)
	if err != nil || rec.Activity.State != domain.ActivityIdle {
		return err
	}
	return m.lcm.ApplyActivitySignal(ctx, id, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Timestamp: m.clock(),
		Event: "account-move", LaunchID: strings.TrimSpace(rec.Metadata.RuntimeLaunchID),
	})
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
	adopted, err := m.accounts.AdoptSession(ctx, rec.ID, rec.Harness)
	if err != nil {
		return false, err
	}
	if !adopted { // no default after all: the agent keeps its own setup and keeps running
		if managed, err := m.sessionManaged(ctx, rec.ID); err != nil || !managed {
			return false, err
		}
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

// terminalLastTyped is when text was last typed in the terminal.
func (m *Manager) terminalLastTyped(rec domain.SessionRecord) time.Time {
	m.terminalInputGateMu.Lock()
	gate := m.terminalInputGate
	m.terminalInputGateMu.Unlock()
	if gate == nil {
		return time.Time{}
	}
	return gate.LastTypedAt(runtimeHandle(rec.Metadata).ID)
}

// stopLegacySession runs stop while nothing else may operate on the session.
func (m *Manager) stopLegacySession(ctx context.Context, id domain.SessionID, stop func(context.Context, domain.SessionID) (bool, error)) (bool, error) {
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
	return stop(ctx, id)
}
