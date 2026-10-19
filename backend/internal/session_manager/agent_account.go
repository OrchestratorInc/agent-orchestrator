package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// StartAccountTransition moves the session's agent to accountID through the
// same durable handoff as an interface switch, so a move either completes or
// leaves the session on its old account:
//
//  1. While the current agent keeps running, everything that can fail without
//     touching the conversation is checked: the account's login, launch auth
//     in its environment, and that its folder can see the conversation.
//  2. The current turn drains (or, with the interrupt policy, is cancelled) and
//     the agent stops; a provider allows one live writer per conversation.
//  3. The same conversation resumes under the new account.
//  4. Only then does the session's binding change. A failure before that stops
//     whatever started and restarts the agent on the old account.
//
// The caller gets the transition at once; the handoff runs in the background.
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
	// Refuse an unusable account before claiming the session, so a wrong pick
	// never interrupts the running agent.
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

// preflightAccountTarget checks, while the source still runs, that the target
// account can resume this conversation.
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
	// Account folders link their history to the computer's own, so the target
	// sees the conversation. Prove it rather than find out after the stop.
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

// markAccountSwitch tells the conversation that a move which stopped the turn
// in progress is why that turn ended, so the chat shows the switch instead of
// a failure. Best-effort: the move has already committed.
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

// runtimeEnvOnAccount is runtimeEnv as the session's agent would get it on
// accountID, for checks made before the move. It leaves the account override
// alone: the source agent still runs, and a restart of it must keep its own
// account. An account env that cannot be resolved fails the check: the env
// would name another login, and every probe below would pass against it.
func (m *Manager) runtimeEnvOnAccount(rec domain.SessionRecord, projectEnv map[string]string, accountID string) (map[string]string, error) {
	return m.runtimeEnvChecked(rec.ID, rec.ProjectID, rec.IssueID, projectEnv, accountID)
}

// ExitAgentsOnAccount stops every running agent that uses accountID, before the
// account is removed. Each session stays, its agent exited; opening it resumes
// it on the default account, and a move takes it to another one.
func (m *Manager) ExitAgentsOnAccount(ctx context.Context, accountID string) error {
	if m.agentAccounts == nil {
		return nil
	}
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		bound, err := m.agentAccounts.SessionAccountID(ctx, rec.ID, rec.Harness)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if bound != accountID {
			continue
		}
		// A launch already underway (a resume, a spawn, a move) may have read the
		// account's folder before the removal began; the session still reads as
		// exited until its agent reports. Refuse rather than delete the folder
		// under it: a retry after it settles stops it like any running agent.
		if m.launchInFlight(rec.ID) {
			errs = append(errs, fmt.Errorf("session %s is starting its agent on the account", rec.ID))
			continue
		}
		if rec.Activity.State == domain.ActivityExited {
			continue
		}
		if _, err := m.ExitAgent(ctx, rec.ID); err != nil && !errors.Is(err, ErrAgentExited) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// launchInFlight reports an agent operation, a transition or an asynchronous
// chat spawn that may still be starting an agent for the session.
func (m *Manager) launchInFlight(id domain.SessionID) bool {
	if m.SessionMutationInProgress(id) {
		return true
	}
	m.asyncChatSpawnsMu.Lock()
	defer m.asyncChatSpawnsMu.Unlock()
	_, starting := m.asyncChatSpawns[id]
	return starting
}

// The account override makes the session's launches use accountID instead of
// its binding while an account transition starts the agent there, before the
// binding commits. Transitions own the session, so only their launch reads it.
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
