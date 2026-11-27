package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LaunchColdChatSwitch builds the launch for a Chat provider switch whose source
// agent has exited and cannot start, then runs start with it. The launch is the
// one a resume would build, for the target harness: its runtime environment, AO
// MCP servers and system prompt. There is no provider conversation to resume;
// start supplies the switch and its handoff note.
//
// The session's agent operation is held until start returns, so a concurrent
// Resume cannot relaunch the old agent while the target takes over, and a switch
// cannot begin while a resume is still running.
func (m *Manager) LaunchColdChatSwitch(
	ctx context.Context,
	id domain.SessionID,
	target domain.AgentHarness,
	start func(context.Context, ChatStart) error,
) error {
	if m.chat == nil {
		return errors.New("chat mode is not available in this build")
	}
	if err := m.beginAgentOperation(ctx, id, agentOperationProviderSwitch); err != nil {
		if errors.Is(err, errAgentOperationInProgress) {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "another session operation is in progress", Cause: err}
		}
		return err
	}
	defer m.endAgentOperation(id, agentOperationProviderSwitch)
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return fmt.Errorf("switch %s: interface transition: %w", id, err)
	} else if active {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "an interface switch is already in progress", Cause: ErrInterfaceTransitionInProgress}
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return err
	}
	if rec.IsTerminated {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "terminated sessions must be restored before switching", Cause: ErrTerminated}
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: "only a Chat session can switch without its agent"}
	}
	if rec.Metadata.WorkspacePath == "" {
		return fmt.Errorf("switch %s: %w", id, ErrIncompleteHandle)
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return fmt.Errorf("switch %s: %w", id, err)
	}
	releaseHarness, err := m.beginHarnessUse(target)
	if err != nil {
		return fmt.Errorf("switch %s: %w", id, err)
	}
	defer releaseHarness()
	releaseCodexAdmission, err := m.acquireCodexControllerAdmission(ctx, target)
	if err != nil {
		return fmt.Errorf("switch %s: controller admission: %w", id, err)
	}
	defer releaseCodexAdmission()

	// The launch describes the target, never the exited source.
	targetRec := rec
	targetRec.Harness = target
	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, true)
	if err != nil {
		return fmt.Errorf("switch %s: system prompt: %w", id, err)
	}
	additionalDirectories, err := m.restoredWorkspaceProjectDirectories(ctx, rec, project, rec.Metadata.WorkspacePath)
	if err != nil {
		return fmt.Errorf("switch %s: workspace roots: %w", id, err)
	}
	env := m.runtimeEnvForHarness(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, target)
	agent, hasAgent := m.agents.Agent(target)
	if hasAgent {
		m.augmentAgentRuntimeEnv(agent, env)
	}
	return start(ctx, ChatStart{
		SessionID:               rec.ID,
		ProjectID:               rec.ProjectID,
		Kind:                    rec.Kind,
		Harness:                 target,
		DataDir:                 m.dataDir,
		WorkspacePath:           rec.Metadata.WorkspacePath,
		Env:                     maps.Clone(env),
		Permissions:             restoredAgentConfig(targetRec, project.Config).Permissions,
		SystemPrompt:            systemPrompt,
		AdditionalDirectories:   additionalDirectories,
		MCPServers:              m.aoMCPServers(ctx, target, env),
		ExpectedControllerOwner: rec.ControllerOwner(),
		PrepareControllerEnv: func(launchCtx context.Context, expected domain.SessionControllerOwner) (map[string]string, error) {
			_, launchEnv, prepareErr := m.prepareChatControllerEnv(launchCtx, targetRec, project.Config.Env, expected)
			if prepareErr != nil {
				return nil, prepareErr
			}
			if hasAgent {
				m.augmentAgentRuntimeEnv(agent, launchEnv)
			}
			return launchEnv, nil
		},
	})
}
