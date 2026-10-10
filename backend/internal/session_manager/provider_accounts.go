package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetProviderAccounts connects managed account routing to session launches.
func (m *Manager) SetProviderAccounts(accounts ports.ProviderAccountRouting) {
	m.providerAccounts = accounts
}
func (m *Manager) applyAccountEnv(ctx context.Context, rec domain.SessionRecord, env map[string]string) error {
	if m.providerAccounts == nil {
		return nil
	}
	route, managed, err := m.providerAccounts.SessionAccount(ctx, rec.ID)
	if err != nil {
		return err
	}
	if !managed {
		return nil
	}
	expected := ""
	switch rec.Harness {
	case domain.HarnessCodex:
		expected = "codex"
	case domain.HarnessClaudeCode:
		expected = "claude"
	}
	if route.Provider != expected {
		return ports.ErrProviderAccountIncompatible
	}
	managedEnv, err := m.providerAccounts.LaunchAccountEnv(ctx, rec.ID)
	if err != nil {
		return err
	}
	for key, value := range managedEnv {
		env[key] = value
	}
	return nil
}

// AcquireAccountMutation fences worker input, related reviews, and native activity without interrupting work.
func (m *Manager) AcquireAccountMutation(ctx context.Context, ids []domain.SessionID) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	const operation agentOperationKind = "provider_account"
	m.reviewersMu.Lock()
	reviewers := m.reviewers
	m.reviewersMu.Unlock()
	var cleanup []func()
	release := func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	}
	for _, id := range ids {
		if err := m.beginAgentOperation(ctx, id, operation); err != nil {
			release()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ports.ErrProviderAccountBusy
		}
		sessionID := id
		cleanup = append(cleanup, func() { m.endAgentOperation(sessionID, operation) })
		rec, found, err := m.store.GetSession(ctx, id)
		if err != nil {
			release()
			return nil, err
		}
		if !found {
			continue
		}
		workerStopped := rec.IsTerminated || rec.Activity.State == domain.ActivityExited
		if !workerStopped && rec.Activity.State != domain.ActivityIdle && rec.Activity.State != domain.ActivityWaitingInput {
			release()
			return nil, ports.ErrProviderAccountBusy
		}
		if guard, ok := reviewers.(interface {
			AcquireAccountRoutingPause(context.Context, domain.SessionID) (func(), error)
		}); ok {
			done, err := guard.AcquireAccountRoutingPause(ctx, id)
			if err != nil {
				release()
				return nil, err
			}
			cleanup = append(cleanup, done)
		}
		if workerStopped {
			continue
		}
		if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
			guard, ok := m.chat.(interface {
				AcquireAccountRoutingPause(context.Context, domain.SessionID) (func(), error)
			})
			if !ok {
				release()
				return nil, ports.ErrProviderAccountBusy
			}
			done, err := guard.AcquireAccountRoutingPause(ctx, id)
			if err != nil {
				release()
				return nil, err
			}
			cleanup = append(cleanup, done)
		} else {
			lastInput, done := m.beginTerminalInputDrain(rec)
			if done != nil {
				cleanup = append(cleanup, done)
			}
			proofCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := m.prepareSourceHandoff(proofCtx, rec, domain.SessionInterfaceTransitionDrain, lastInput)
			cancel()
			if err != nil {
				release()
				if errors.Is(err, context.Canceled) && ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, ports.ErrProviderAccountBusy
			}
		}
	}
	var once sync.Once
	return func() { once.Do(release) }, nil
}

// RelatedAccountEnv gives a same-provider reviewer its owning worker's ticket.
func (m *Manager) RelatedAccountEnv(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (map[string]string, error) {
	if m.providerAccounts == nil {
		return nil, nil
	}
	route, managed, err := m.providerAccounts.SessionAccount(ctx, id)
	if err != nil || !managed {
		return nil, err
	}
	provider := ""
	switch harness {
	case domain.HarnessCodex:
		provider = "codex"
	case domain.HarnessClaudeCode:
		provider = "claude"
	}
	if provider != route.Provider {
		return nil, nil
	}
	if route.AccountID == "" {
		return nil, ports.ErrProviderLoginRequired
	}
	return m.providerAccounts.LaunchAccountEnv(ctx, id)
}

func (m *Manager) forgetAccount(ctx context.Context, id domain.SessionID) error {
	if accounts, ok := m.providerAccounts.(interface {
		ForgetAccount(context.Context, domain.SessionID) error
	}); ok {
		return accounts.ForgetAccount(ctx, id)
	}
	return nil
}
