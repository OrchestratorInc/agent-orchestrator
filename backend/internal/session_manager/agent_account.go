package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (m *Manager) StartAccountTransition(
	ctx context.Context,
	id domain.SessionID,
	accountID string,
	policy domain.SessionInterfaceTransitionPolicy,
) (domain.SessionInterfaceTransition, error) {
	if !policy.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("transition policy %q is invalid", policy)
	}
	if m.agentAccounts == nil {
		return domain.SessionInterfaceTransition{}, ports.ErrAgentAccountUnknown
	}
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return domain.SessionInterfaceTransition{}, ErrInterfaceHandoffUnsupported
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !found {
		return domain.SessionInterfaceTransition{}, ErrNotFound
	}
	if rec.IsTerminated {
		return domain.SessionInterfaceTransition{}, ErrTerminated
	}
	if interfaceWorkspaceUnavailable(rec) {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: session %s", ErrInterfaceWorkspaceUnavailable, id)
	}
	current, err := m.agentAccounts.SessionAccountID(ctx, id, rec.Harness)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if current == "" {
		return domain.SessionInterfaceTransition{}, ports.ErrAgentAccountProviderMismatch
	}
	if current == accountID {
		return domain.SessionInterfaceTransition{}, ports.ErrAgentAccountUnchanged
	}
	if err := m.agentAccounts.ValidateAccount(ctx, rec.Harness, accountID); err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	nativeID, err := m.handoffNativeConversationID(ctx, rec)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	mode := domain.NormalizeSessionMode(rec.Mode)
	now := m.clock()
	transition, created, err := store.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
		ID: m.newLaunchID(), SessionID: id, SourceMode: mode, TargetMode: mode,
		Policy: policy, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase: domain.SessionInterfaceTransitionRequested, NativeConversationID: nativeID,
		SourceAccountID: current, TargetAccountID: accountID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !created {
		return transition, ErrInterfaceTransitionInProgress
	}
	if err := m.launchInterfaceTransition(ctx, transition); err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	return transition, nil
}

func (m *Manager) preflightAccountTarget(
	ctx context.Context,
	rec domain.SessionRecord,
	transition domain.SessionInterfaceTransition,
) error {
	if err := m.agentAccounts.ValidateAccount(ctx, rec.Harness, transition.TargetAccountID); err != nil {
		return err
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return ErrUnknownHarness
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	env, err := m.runtimeEnvOnAccount(rec, project.Config.Env, transition.TargetAccountID)
	if err != nil {
		return err
	}
	config := restoredAgentConfig(rec, project.Config)
	pinRuntimePermissionEnv(env, config.Permissions)
	m.augmentAgentRuntimeEnv(agent, env)
	if err := m.preflightLaunchAuth(ctx, agent, rec, env); err != nil {
		return err
	}
	if transition.NativeConversationID != "" {
		if probe, ok := agent.(ports.AgentInterfaceHandoffHistoryProbe); ok {
			exists, probeErr := probe.NativeConversationExists(ctx, ports.SessionRef{
				ID: string(rec.ID), WorkspacePath: rec.Metadata.WorkspacePath,
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: transition.NativeConversationID},
			}, transition.NativeConversationID, env)
			if probeErr != nil {
				return fmt.Errorf("%w: %w", ErrNativeConversationUnverified, probeErr)
			}
			if !exists {
				return fmt.Errorf("%w: the account's folder cannot see this conversation", ErrNativeConversationMissing)
			}
		}
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat == nil {
			return ports.ErrChatUnsupported
		}
		return m.chat.PreflightChat(ctx, rec.Harness, config.Permissions)
	}
	return m.preflightTerminalLaunch(ctx, agent, rec, project, transition.NativeConversationID)
}

func (m *Manager) markAccountSwitch(ctx context.Context, rec domain.SessionRecord, transition domain.SessionInterfaceTransition) {
	if transition.Policy != domain.SessionInterfaceTransitionInterrupt || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return
	}
	marker, ok := m.chat.(interface {
		MarkAccountSwitch(context.Context, domain.SessionID, string) error
	})
	if !ok {
		return
	}
	if err := marker.MarkAccountSwitch(ctx, rec.ID, transition.TargetAccountID); err != nil {
		m.logger.Warn("account transition: stopped turn not marked as switched", "sessionID", rec.ID, "transition", transition.ID, "error", err)
	}
}

func (m *Manager) deliverQueuedAfterAccountMove(transition domain.SessionInterfaceTransition) {
	if !transition.AccountMove() || transition.SourceMode != domain.SessionModeChat || m.chat == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), interfaceTransitionStepLimit)
	defer cancel()
	if err := m.chat.DrainChatQueue(ctx, transition.SessionID); err != nil {
		m.logger.Warn("account transition: messages sent during the move not delivered",
			"sessionID", transition.SessionID, "transition", transition.ID, "error", err)
	}
}

func (m *Manager) runtimeEnvOnAccount(rec domain.SessionRecord, projectEnv map[string]string, accountID string) (map[string]string, error) {
	return m.runtimeEnvChecked(rec.ID, rec.ProjectID, rec.IssueID, projectEnv, accountID, rec.Harness)
}

func (m *Manager) ExitAgentsOnAccount(ctx context.Context, accountID string) error {
	if m.agentAccounts == nil {
		return nil
	}
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	var (
		errs  []error
		errMu sync.Mutex
		wg    sync.WaitGroup
	)
	fail := func(err error) {
		errMu.Lock()
		defer errMu.Unlock()
		errs = append(errs, err)
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		bound, err := m.agentAccounts.SessionAccountID(ctx, rec.ID, rec.Harness)
		if err != nil {
			fail(err)
			continue
		}
		if bound != accountID {
			continue
		}
		if m.launchInFlight(rec.ID) {
			fail(fmt.Errorf("session %s is starting its agent on the account", rec.ID))
			continue
		}
		if rec.Activity.State == domain.ActivityExited {
			continue
		}
		wg.Go(func() {
			if _, err := m.ExitAgent(ctx, rec.ID); err != nil && !errors.Is(err, ErrAgentExited) {
				fail(err)
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (m *Manager) launchInFlight(id domain.SessionID) bool {
	if m.SessionMutationInProgress(id) {
		return true
	}
	m.asyncChatSpawnsMu.Lock()
	defer m.asyncChatSpawnsMu.Unlock()
	_, starting := m.asyncChatSpawns[id]
	return starting
}

func (m *Manager) setAccountOverride(id domain.SessionID, accountID string) {
	m.accountOverrideMu.Lock()
	defer m.accountOverrideMu.Unlock()
	if m.accountOverrides == nil {
		m.accountOverrides = map[domain.SessionID]string{}
	}
	m.accountOverrides[id] = accountID
}

func (m *Manager) clearAccountOverride(id domain.SessionID) {
	m.accountOverrideMu.Lock()
	defer m.accountOverrideMu.Unlock()
	delete(m.accountOverrides, id)
}

func (m *Manager) accountOverride(id domain.SessionID) string {
	m.accountOverrideMu.Lock()
	defer m.accountOverrideMu.Unlock()
	return m.accountOverrides[id]
}
