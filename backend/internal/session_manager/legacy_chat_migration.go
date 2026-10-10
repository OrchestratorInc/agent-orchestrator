package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A Codex or Claude chat started before Account Manager has no account: its
// provider process was launched on this computer's own sign-in and keeps
// running on it. Such a chat is moved onto Account Manager by one rule, applied
// in one place: it is given the provider's default account as its provider
// process is launched. Everything else here only causes that launch for the
// chats whose old process is still running, by restarting them while they are
// doing nothing.
//
// A running process cannot be given an account, so a chat is never adopted
// while its old process lives: it would read as managed and not be.

// legacyChatRestartsAtOnce bounds how many chats are restarted together. Each
// restart starts a provider process, which costs seconds of CPU.
const legacyChatRestartsAtOnce = 3

// sessionAdopter is the part of Account Manager that takes in a session it did
// not create.
type sessionAdopter interface {
	AdoptSession(context.Context, domain.SessionID, domain.AgentHarness) (bool, error)
}

// chatRestarter stops a quiet Chat provider and starts it again.
type chatRestarter interface {
	HibernateChatForRestart(context.Context, domain.SessionID) (bool, error)
	WakeChat(context.Context, domain.SessionID) error
}

// SetLegacyChatMigration turns the move on or off. Off, a chat without an
// account keeps running on this computer's own sign-in, as it did before
// Account Manager.
func (m *Manager) SetLegacyChatMigration(enabled bool) {
	m.legacyChatMigration.Store(enabled)
}

func accountManagedHarness(harness domain.AgentHarness) bool {
	return harness == domain.HarnessCodex || harness == domain.HarnessClaudeCode
}

// adoptLegacyChat gives a chat without an account the provider's default one.
// The caller is about to launch the chat's provider process and has none
// running. An error means the launch must not go ahead.
func (m *Manager) adoptLegacyChat(ctx context.Context, rec domain.SessionRecord) error {
	adopter, ok := m.providerAccounts.(sessionAdopter)
	if !ok || !m.legacyChatMigration.Load() || !accountManagedHarness(rec.Harness) {
		return nil
	}
	// A session changing agent has no settled provider. An account for the one
	// it is leaving would stop the one it is going to from starting.
	if switching, err := m.agentSwitchInProgress(ctx, rec.ID); err != nil || switching {
		return nil
	}
	adopted, err := adopter.AdoptSession(ctx, rec.ID, rec.Harness)
	if err == nil {
		if adopted && m.logger != nil {
			m.logger.Info("chat moved onto Account Manager", "session", rec.ID, "harness", rec.Harness)
		}
		return nil
	}
	// An adoption that was written down but could not be sent to the helper
	// still takes effect once the helper is reachable. Launching on this
	// computer's own sign-in now would leave a chat that reads as managed and
	// is not, so it waits; the next start finds the adoption finished.
	if recovery, ok := m.providerAccounts.(interface {
		RecoveryRequired(context.Context) (bool, error)
	}); ok {
		if unfinished, recoveryErr := recovery.RecoveryRequired(ctx); recoveryErr != nil || unfinished {
			return fmt.Errorf("give chat %s its account: %w", rec.ID, err)
		}
	}
	// Nothing was changed: the chat starts as it always has.
	if m.logger != nil {
		m.logger.Warn("chat left on this computer's own sign-in", "session", rec.ID, "error", err)
	}
	return nil
}

func (m *Manager) agentSwitchInProgress(ctx context.Context, id domain.SessionID) (bool, error) {
	store, ok := m.store.(interface {
		GetActiveAgentSwitch(context.Context, domain.SessionID) (domain.AgentSwitch, bool, error)
	})
	if !ok {
		return false, nil
	}
	_, active, err := store.GetActiveAgentSwitch(ctx, id)
	return active, err
}

// MigrateLegacyChats restarts the idle chats that still run without an account,
// a few at a time, and reports how many are still running without one. A chat
// that is busy is left alone and counted, so the caller can try again later.
// One that is asleep or has stopped has no process to replace and nothing to
// wait for: it is adopted whenever it next starts, and is not counted.
func (m *Manager) MigrateLegacyChats(ctx context.Context) (int, error) {
	if _, ok := m.providerAccounts.(sessionAdopter); !ok || m.chat == nil || !m.legacyChatMigration.Load() {
		return 0, nil
	}
	sessions, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return 0, fmt.Errorf("list chats without an account: %w", err)
	}
	remaining := 0
	var running []domain.SessionID
	for _, rec := range sessions {
		if rec.IsTerminated || !accountManagedHarness(rec.Harness) || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
			continue
		}
		_, managed, err := m.providerAccounts.SessionAccount(ctx, rec.ID)
		if err != nil {
			return remaining, err
		}
		if managed {
			continue
		}
		if rec.HibernatedAt != nil || rec.Activity.State == domain.ActivityExited {
			continue
		}
		// Counted even before AO has found its process again after a restart
		// of its own: the process is there, and a later pass will see it.
		remaining++
		if m.chat.HasLiveChatController(rec.ID) {
			running = append(running, rec.ID)
		}
	}
	var (
		wait     sync.WaitGroup
		mu       sync.Mutex
		failures []error
		slots    = make(chan struct{}, legacyChatRestartsAtOnce)
	)
	for _, id := range running {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { <-slots }()
			moved, err := m.restartLegacyChat(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if moved {
				remaining--
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("restart chat %s: %w", id, err))
			}
		}()
	}
	wait.Wait()
	return remaining, errors.Join(failures...)
}

// restartLegacyChat stops one chat's provider process if the chat is doing
// nothing, starts it again, and reports whether the chat now has an account.
func (m *Manager) restartLegacyChat(ctx context.Context, id domain.SessionID) (bool, error) {
	restarter, ok := m.chat.(chatRestarter)
	if !ok {
		return false, nil
	}
	stopped, err := func() (bool, error) {
		operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// The same fence idle hibernation takes: a resume, an account change or
		// another restart of this session is already in hand.
		if err := m.beginAgentOperation(operationCtx, id, agentOperationHibernate); err != nil {
			if errors.Is(err, errAgentOperationInProgress) {
				return false, nil
			}
			return false, err
		}
		defer m.endAgentOperation(id, agentOperationHibernate)
		if active, err := m.hasActiveInterfaceTransition(operationCtx, id); err != nil || active {
			return false, err
		}
		if switching, err := m.agentSwitchInProgress(operationCtx, id); err != nil || switching {
			return false, err
		}
		return restarter.HibernateChatForRestart(operationCtx, id)
	}()
	if err != nil || !stopped {
		return false, err
	}
	// Starting the process is what gives the chat its account. If the start
	// fails the chat stays asleep and wakes the usual way, when it is opened or
	// written to, and is adopted then.
	if err := restarter.WakeChat(ctx, id); err != nil {
		return false, fmt.Errorf("start again: %w", err)
	}
	_, managed, err := m.providerAccounts.SessionAccount(ctx, id)
	return managed, err
}
