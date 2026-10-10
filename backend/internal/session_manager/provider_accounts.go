package sessionmanager

import (
	"context"
	"errors"
	"fmt"
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

// MigrateLegacyChats restarts, one after another, the idle chats that still run
// without an account, and reports how many keep running without one. A chat
// that is asleep or stopped is adopted when it next starts and is not counted.
func (m *Manager) MigrateLegacyChats(ctx context.Context) (int, error) {
	chat, ok := m.chat.(chatRestarter)
	if m.accounts == nil || !ok {
		return 0, nil
	}
	sessions, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return 0, err
	}
	remaining := 0
	var failures []error
	for _, rec := range sessions {
		if rec.IsTerminated || rec.HibernatedAt != nil || rec.Activity.State == domain.ActivityExited ||
			domain.AccountProvider(rec.Harness) == "" || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
			continue
		}
		moved, err := m.accountManaged(ctx, rec.ID)
		if err == nil && !moved && m.chat.HasLiveChatController(rec.ID) {
			moved, err = m.restartLegacyChat(ctx, chat, rec.ID)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("move chat %s onto its account: %w", rec.ID, err))
		}
		if !moved {
			remaining++
		}
	}
	return remaining, errors.Join(failures...)
}

// restartLegacyChat restarts one chat if it is doing nothing, and reports
// whether it now has an account. A failed start leaves it asleep.
func (m *Manager) restartLegacyChat(ctx context.Context, chat chatRestarter, id domain.SessionID) (bool, error) {
	if stopped, err := m.stopLegacyChat(ctx, chat, id); err != nil || !stopped {
		return false, err
	}
	if err := chat.WakeChat(ctx, id); err != nil {
		return false, err
	}
	return m.accountManaged(ctx, id)
}

func (m *Manager) stopLegacyChat(ctx context.Context, chat chatRestarter, id domain.SessionID) (bool, error) {
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
	return chat.HibernateChatForRestart(ctx, id)
}
