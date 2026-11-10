package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// terminalSpawn is a Terminal session's agent launch into a worktree that
// already exists. A synchronous spawn runs it inside the request; an
// asynchronous one runs it in the background after the worktree is published.
type terminalSpawn struct {
	cfg              ports.SpawnConfig
	project          domain.ProjectRecord
	projectKind      domain.ProjectKind
	record           domain.SessionRecord
	workspace        ports.WorkspaceInfo
	prompt           string
	systemPrompt     string
	systemPromptFile string
	agentConfig      ports.AgentConfig
	explicitModel    bool
}

// terminalLaunchFailure is what a failed launchTerminalAgent left behind, so
// each caller can undo it its own way.
type terminalLaunchFailure struct {
	// handle is set once the runtime was created; the caller destroys it.
	handle *ports.RuntimeHandle
	// prepared reports agent files written into the workspace.
	prepared bool
	// terminateWithoutWorkspace lets a synchronous rollback that removed both
	// the runtime and the workspace record the session without one.
	terminateWithoutWorkspace bool
}

// launchTerminalAgent starts the agent in a runtime and delivers its opening
// prompt. It returns only once the prompt has been handed to the agent: that is
// the point a Terminal session is ready.
func (m *Manager) launchTerminalAgent(ctx context.Context, in terminalSpawn) (domain.SessionRecord, terminalLaunchFailure, error) {
	cfg, project, projectKind, rec, ws := in.cfg, in.project, in.projectKind, in.record, in.workspace
	prompt, systemPrompt, systemPromptFile := in.prompt, in.systemPrompt, in.systemPromptFile
	agentConfig, explicitModel := in.agentConfig, in.explicitModel
	// Adapters whose model picker is an agent-owned mode list (e.g. Amp) keep
	// their selectable values in AgentConfig.Mode. Normalize a copy for the
	// adapter so `ao spawn --agent amp --model high` launches Amp with `--mode
	// high`, while metadata keeps the original resolved Model for the API view.
	adapterConfig := normalizeAgentConfigForHarness(cfg.Harness, agentConfig)
	id := rec.ID
	var err error
	agent, ok := m.agents.Agent(cfg.Harness)
	if !ok {
		return rec, terminalLaunchFailure{}, fmt.Errorf("spawn %s: %w: no agent adapter for harness %q", id, ErrUnknownHarness, cfg.Harness)
	}
	var env map[string]string
	rec, env, err = m.prepareWorkerLaunchEnv(ctx, rec, project.Config.Env)
	if err != nil {
		return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnBrowser, err)
	}
	if cfg.Harness == domain.HarnessMemcode && agentConfig.Model != "" {
		// The workspace may introduce a native endpoint, so resolve this after
		// provisioning but before hooks or launch validation see the model.
		customEndpoint, modelErr := memcode.UsesCustomEndpoint(ctx, ws.Path, env)
		if modelErr == nil && customEndpoint {
			if explicitModel {
				modelErr = fmt.Errorf("%w: memcode custom endpoints require native model selection", ErrUnsupportedModel)
			} else {
				agentConfig.Model = ""
				adapterConfig.Model = ""
			}
		}
		if modelErr != nil {
			return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnPrepare, modelErr)
		}
	}
	m.augmentAgentRuntimeEnv(agent, env)
	pinRuntimePermissionEnv(env, adapterConfig.Permissions)
	if validator, ok := agent.(ports.AgentLaunchAuthValidator); ok {
		status, authErr := validator.ValidateLaunchAuth(ctx, ws.Path, env)
		if authErr != nil {
			m.logger.Debug("spawn: launch authentication probe inconclusive; continuing",
				"sessionID", id, "harness", cfg.Harness, "error", agentlaunch.RedactValues(authErr.Error(), project.Config.Env))
		} else if status == ports.AgentAuthStatusUnauthorized {
			return rec, terminalLaunchFailure{prepared: true}, fmt.Errorf("spawn %s: %w", id, ports.ErrAgentAuthRequired)
		}
	}
	if err := m.prepareWorkspace(ctx, agent, id, ws.Path, systemPrompt, systemPromptFile, adapterConfig, env); err != nil {
		return rec, terminalLaunchFailure{}, wrapSpawnStage(id, ErrSpawnPrepare, err)
	}
	launchCfg := ports.LaunchConfig{
		DataDir:          m.dataDir,
		SessionID:        string(id),
		WorkspacePath:    ws.Path,
		Kind:             cfg.Kind,
		Prompt:           prompt,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		IssueID:          string(cfg.IssueID),
		Config:           adapterConfig,
		Permissions:      adapterConfig.Permissions,
	}
	delivery, err := agent.GetPromptDeliveryStrategy(ctx, launchCfg)
	if err != nil {
		return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnPromptDelivery, err)
	}
	afterStartPrompt := prompt
	if delivery == ports.PromptDeliveryAfterStart {
		afterStartPrompt, err = buildAfterStartPrompt(ctx, agent, launchCfg)
		if err != nil {
			return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnPromptDelivery, err)
		}
		launchCfg.Prompt = ""
	}
	argv, err := agent.GetLaunchCommand(ctx, launchCfg)
	if err != nil {
		return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnLaunchCommand, err)
	}
	// Pre-flight: confirm argv[0] actually exists on PATH (or as an absolute
	// path the adapter returned) BEFORE handing the launch to the runtime.
	// tmux happily creates a session+pane around a missing command, so an
	// unresolved binary would leak through as a "live" session that never ran.
	if err := m.validateAgentBinary(argv); err != nil {
		return rec, terminalLaunchFailure{prepared: true}, fmt.Errorf("spawn %s: %w", id, err)
	}
	m.augmentRuntimePATHForLaunchBinary(ctx, env, argv)
	argv, launchID, err := m.superviseAgentProcess(agent, id, env, argv)
	if err != nil {
		return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnSupervisor, err)
	}
	if err := m.prepareRuntimeLaunch(ctx, agent, id, ws.Path, systemPrompt, systemPromptFile, adapterConfig, env, workspaceRestoreContext{}); err != nil {
		return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnPrepare, err)
	}
	if err := m.lcm.PrepareLaunch(id, launchID); err != nil {
		return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrSpawnPrepareLaunch, err)
	}
	defer m.lcm.CancelLaunch(id, launchID)
	releaseCodexAdmission, err := m.acquireCodexControllerAdmission(ctx, cfg.Harness)
	if err != nil {
		return rec, terminalLaunchFailure{prepared: true}, fmt.Errorf("spawn %s: %w", id, err)
	}
	defer releaseCodexAdmission()
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           env,
	})
	if err != nil {
		return rec, terminalLaunchFailure{prepared: true}, wrapSpawnStage(id, ErrRuntimeCreate, agentlaunch.RedactError(err, project.Config.Env))
	}

	metadata := domain.SessionMetadata{
		Permissions:               rec.Metadata.Permissions,
		Branch:                    ws.Branch,
		WorkspacePath:             ws.Path,
		WorkspaceRepoPath:         ws.RepoPath,
		RuntimeHandleID:           handle.ID,
		RuntimeLaunchID:           launchID,
		Prompt:                    prompt,
		LatestUserPrompt:          prompt,
		BrowserCapabilityVerifier: rec.Metadata.BrowserCapabilityVerifier,
		ArtifactDir:               rec.Metadata.ArtifactDir,
		// The user-visible resolved selection is Model for regular harnesses and
		// Mode for adapters whose catalog is a mode list (e.g. Amp). If an explicit
		// Model override exists it wins; otherwise fall back to the resolved Mode.
		Model:  resolvedModelForMetadata(cfg.Harness, agentConfig, adapterConfig),
		Effort: agentConfig.Effort,
	}
	if prompt != "" {
		metadata.LatestUserPromptAt = m.clock()
	}
	if projectKind == domain.ProjectKindSingleRepo {
		metadata.DiffBaseSHA, metadata.DiffBaseRef = resolveSpawnDiffBase(ctx, ws.Path, ws.BaseRef)
	}
	if cfg.AutomationRunID != nil {
		rec.Metadata = metadata
		rec.UpdatedAt = m.clock()
		if err := m.store.UpdateSession(ctx, rec); err != nil {
			return rec, terminalLaunchFailure{handle: &handle, terminateWithoutWorkspace: true}, wrapSpawnStage(id, ErrSpawnCommit, err)
		}
	}
	if err := m.lcm.MarkSpawned(ctx, id, metadata); err != nil {
		return rec, terminalLaunchFailure{handle: &handle}, wrapSpawnStage(id, ErrSpawnCommit, err)
	}
	// The agent owns the checkout now. Waiting for it to take its prompt must
	// not hold up other starts in this project; a rollback takes the gate again.
	if release, ok := ctx.Value(spawnWorkspaceGateKey{}).(func()); ok {
		release()
	}
	if delivery == ports.PromptDeliveryAfterStart {
		if err := m.deliverAfterStartPrompt(ctx, agent, launchCfg, handle, id, afterStartPrompt); err != nil {
			return rec, terminalLaunchFailure{handle: &handle, terminateWithoutWorkspace: true}, wrapSpawnStage(id, ErrSpawnDeliverPrompt, err)
		}
	}
	return rec, terminalLaunchFailure{}, nil
}

// rollbackTerminalLaunch undoes a failed synchronous launch: the session was
// never handed to a caller, so its runtime, workspace and row go.
func (m *Manager) rollbackTerminalLaunch(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo, failure terminalLaunchFailure) {
	if failure.handle == nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, failure.prepared, false)
		return
	}
	runtimeDestroyed := m.destroySpawnRuntimeAfterFailure(ctx, *failure.handle)
	workspaceDestroyed := m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, rec, ws, workspaceProject, runtimeDestroyed)
	m.markSpawnFailedTerminatedAfterFailure(ctx, rec.ID, failure.terminateWithoutWorkspace && runtimeDestroyed && workspaceDestroyed)
}
