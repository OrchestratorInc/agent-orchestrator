package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type ChatLauncher interface {
	SupportsChat(harness domain.AgentHarness) bool
	PreflightChat(ctx context.Context, harness domain.AgentHarness, permissions ports.PermissionMode) error
	StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error)
	StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error)
	RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error)
	HasLiveChatController(id domain.SessionID) bool
	StopChat(ctx context.Context, id domain.SessionID) error
	QueueChatPrompt(ctx context.Context, id domain.SessionID, text string) (string, error)
	DrainChatQueue(ctx context.Context, id domain.SessionID) error
}

type chatEnvironmentPreflighter interface {
	PreflightChatWithEnv(context.Context, domain.AgentHarness, ports.PermissionMode, map[string]string) error
}

func (m *Manager) preflightChat(ctx context.Context, harness domain.AgentHarness, permissions ports.PermissionMode, env map[string]string) error {
	if preflighter, ok := m.chat.(chatEnvironmentPreflighter); ok {
		return preflighter.PreflightChatWithEnv(ctx, harness, permissions, env)
	}
	return m.chat.PreflightChat(ctx, harness, permissions)
}

type chatBackgroundTaskRunner interface {
	RunBackgroundTask(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
}

type ChatStart = ports.ChatControllerStart

type ChatStarted = ports.ChatControllerStarted

func (m *Manager) RunBackgroundTask(
	ctx context.Context,
	id domain.SessionID,
	systemPrompt, prompt string,
) (string, error) {
	runner, ok := m.chat.(chatBackgroundTaskRunner)
	if !ok {
		return "", ports.ErrChatUnsupported
	}
	return m.runBackgroundTask(ctx, id, systemPrompt, prompt, runner.RunBackgroundTask)
}

func (m *Manager) RunPromptSuggestionTask(ctx context.Context, id domain.SessionID, turnID, systemPrompt, prompt string) (string, error) {
	runner, ok := m.chat.(interface {
		RunPromptSuggestionTask(context.Context, domain.SessionID, string, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
	})
	if !ok {
		return m.RunBackgroundTask(ctx, id, systemPrompt, prompt)
	}
	return m.runBackgroundTask(ctx, id, systemPrompt, prompt, func(ctx context.Context, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (string, error) {
		return runner.RunPromptSuggestionTask(ctx, id, turnID, harness, cfg, prompt)
	})
}

func (m *Manager) runBackgroundTask(ctx context.Context, id domain.SessionID, systemPrompt, prompt string, run func(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)) (string, error) {
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return "", err
	}
	if rec.IsTerminated {
		return "", fmt.Errorf("session %s is terminated", id)
	}
	rec.Metadata.Effort = ""
	return m.runRecordBackgroundTask(ctx, rec, systemPrompt, prompt, run)
}

func (m *Manager) runRecordBackgroundTask(ctx context.Context, rec domain.SessionRecord, systemPrompt, prompt string, run func(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)) (string, error) {
	releaseHarness, err := m.beginHarnessUse(rec.Harness)
	if err != nil {
		return "", err
	}
	defer releaseHarness()
	releaseCodex, err := m.acquireCodexControllerAdmission(ctx, rec.Harness)
	if err != nil {
		return "", err
	}
	defer releaseCodex()

	root := filepath.Join(m.dataDir, "background-tasks")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create background task root: %w", err)
	}
	workspace, err := os.MkdirTemp(root, "title-")
	if err != nil {
		return "", fmt.Errorf("create background task workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	config := ports.AgentConfig{
		Model: rec.Metadata.Model, Effort: rec.Metadata.Effort, Permissions: backgroundTaskPermissions(rec.Harness),
	}
	env := m.runtimeEnvForHarness(rec.ID, rec.ProjectID, rec.IssueID, nil, rec.Harness)
	if m.agents != nil {
		if agent, found := m.agents.Agent(rec.Harness); found {
			m.augmentAgentRuntimeEnv(agent, env)
		}
	}
	deleteProtectedEnv(env, EnvSessionID, envKeysCaseInsensitive)
	pinRuntimePermissionEnv(env, config.Permissions)

	out, err := run(ctx, rec.Harness, ports.ChatStartConfig{
		DataDir:       m.dataDir,
		WorkspacePath: workspace,
		Env:           env,
		Model:         config.Model,
		Effort:        config.Effort,
		Permissions:   config.Permissions,
		SystemPrompt:  systemPrompt,
	}, prompt)
	if m.backgroundTaskUsage != nil {
		if usageErr := m.backgroundTaskUsage.RecordBackgroundTask(
			context.WithoutCancel(ctx), rec.ID, rec.Harness, workspace,
		); usageErr != nil {
			m.logger.Warn("background task usage not recorded", "sessionID", rec.ID, "error", usageErr)
		}
	}
	return out, err
}

func backgroundTaskPermissions(harness domain.AgentHarness) ports.PermissionMode {
	if harness == domain.HarnessKimi || harness == domain.HarnessGrok {
		return ports.PermissionModeDefault
	}
	return ports.PermissionModeAcceptEdits
}

type ChatControllerCommit = ports.ChatControllerCommit

type chatProviderOwnershipStore interface {
	GetLatestSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	ConversationBranch(context.Context, string, string) (domain.ConversationBranch, error)
}

func interfaceTransitionProviderBoundaryID(transitionID string) string {
	return transitionID + ":provider"
}

type chatSpawn struct {
	cfg              ports.SpawnConfig
	project          domain.ProjectRecord
	projectKind      domain.ProjectKind
	record           domain.SessionRecord
	workspace        ports.WorkspaceInfo
	workspaceProject *ports.WorkspaceProjectInfo
	prompt           string
	systemPrompt     string
	systemPromptFile string
	promptQueued     bool
}

func (m *Manager) launchChatController(ctx context.Context, in chatSpawn) (domain.SessionRecord, error) {
	id := in.record.ID
	releaseCodexAdmission, err := m.acquireCodexControllerAdmission(ctx, in.cfg.Harness)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
		return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, err)
	}
	defer releaseCodexAdmission()
	agentConfig := in.cfg.AgentConfig
	if !in.cfg.AgentConfigResolved {
		agentConfig = applySpawnAgentConfig(effectiveAgentConfig(in.cfg.Harness, in.cfg.Kind, in.project.Config), in.cfg.AgentConfig)
	}

	var diffBaseSHA, diffBaseRef string
	if in.projectKind == domain.ProjectKindSingleRepo {
		diffBaseSHA, diffBaseRef = resolveSpawnDiffBase(
			ctx, in.workspace.Path, in.workspace.BaseRef)
	}
	env := m.runtimeEnvForHarness(id, in.record.ProjectID, in.record.IssueID, in.project.Config.Env, in.record.Harness)
	if agent, ok := m.agents.Agent(in.cfg.Harness); ok {
		m.augmentAgentRuntimeEnv(agent, env)
		if err := m.prepareWorkspace(ctx, agent, id, in.workspace.Path, in.systemPrompt, in.systemPromptFile, agentConfig, env); err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrSpawnPrepare, err)
		}
	}

	providerConversationID, forkHandoffNote := "", ""
	if fork := in.cfg.Fork; fork != nil && fork.Handoff != nil && fork.PrepareChat == nil {
		forkHandoffNote, err = fork.Handoff(ctx, ports.ForkTarget{SessionID: id, DataDir: m.dataDir, WorkspacePath: in.workspace.Path, Env: env})
		if err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, fmt.Errorf("fork: copy conversation: %w", err))
		}
	}
	if fork := in.cfg.Fork; fork != nil && fork.PrepareChat != nil {
		providerConversationID, err = fork.PrepareChat(ctx, ports.ForkTarget{
			SessionID: id, DataDir: m.dataDir, WorkspacePath: in.workspace.Path, Env: env,
		})
		if err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, fmt.Errorf("fork conversation: %w", err))
		}
	}

	var (
		controllerCommitted bool
		completionErr       error
	)
	_, err = m.chat.StartChat(ctx, ChatStart{
		SessionID:               id,
		ProviderConversationID:  providerConversationID,
		ProjectID:               in.cfg.ProjectID,
		Kind:                    in.cfg.Kind,
		Harness:                 in.cfg.Harness,
		DataDir:                 m.dataDir,
		WorkspacePath:           in.workspace.Path,
		Env:                     env,
		Model:                   agentConfig.Model,
		Effort:                  agentConfig.Effort,
		Permissions:             agentConfig.Permissions,
		SystemPrompt:            in.systemPrompt,
		ForkHandoffNote:         forkHandoffNote,
		ForkFirstStart:          in.cfg.Fork != nil && in.cfg.Fork.PrepareChat != nil,
		AdditionalDirectories:   workspaceProjectDirectories(in.workspace.Path, in.workspaceProject),
		MCPServers:              m.aoMCPServers(ctx, in.cfg.Harness, env),
		ExpectedControllerOwner: in.record.ControllerOwner(),
		PrepareControllerEnv: func(launchCtx context.Context, expected domain.SessionControllerOwner) (map[string]string, error) {
			prepared, launchEnv, prepareErr := m.prepareChatControllerEnv(
				launchCtx, in.record, in.project.Config.Env, expected,
			)
			if prepareErr != nil {
				return nil, fmt.Errorf("%w: %w", ErrSpawnBrowser, prepareErr)
			}
			if agent, ok := m.agents.Agent(in.cfg.Harness); ok {
				m.augmentAgentRuntimeEnv(agent, launchEnv)
			}
			in.record = prepared
			return launchEnv, nil
		},
		ControllerReady: func(started ChatStarted) (ChatControllerCommit, error) {
			metadata := domain.SessionMetadata{
				Permissions:               in.record.Metadata.Permissions,
				Branch:                    in.workspace.Branch,
				WorkspacePath:             in.workspace.Path,
				WorkspaceRepoPath:         in.workspace.RepoPath,
				Prompt:                    in.prompt,
				DiffBaseSHA:               diffBaseSHA,
				DiffBaseRef:               diffBaseRef,
				ArtifactDir:               in.record.Metadata.ArtifactDir,
				ProviderConversationID:    started.ProviderConversationID,
				ControllerGeneration:      started.ControllerGeneration,
				BrowserCapabilityVerifier: in.record.Metadata.BrowserCapabilityVerifier,
				Model:                     agentConfig.Model,
				Effort:                    agentConfig.Effort,
			}
			committedConversation, commitErr := m.markChatControllerSpawned(
				ctx, id, metadata, started.Conversation, started.ProviderBoundary,
				started.CommitProviderHistory, nil, started.LiveReconnect,
			)
			completionErr = commitErr
			controllerCommitted = completionErr == nil
			return ChatControllerCommit{
				Conversation: committedConversation,
				ControllerOwner: chatControllerOwner(
					in.record, in.cfg.Harness, started.ProviderConversationID, started.ControllerGeneration,
				),
			}, completionErr
		},
	})
	if err != nil {
		if completionErr != nil || controllerCommitted {
			m.stopChatAfterSpawnFailure(ctx, id)
			workspaceDestroyed := m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, in.record, in.workspace, in.workspaceProject, true)
			if in.promptQueued {
				if workspaceDestroyed {
					m.clearProvisionedWorkspace(ctx, id, in.workspace.Path)
				}
			} else {
				m.markSpawnFailedTerminatedAfterFailure(ctx, id, false)
			}
			if completionErr != nil {
				return domain.SessionRecord{}, wrapSpawnStage(id, ErrSpawnCommit, completionErr)
			}
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, agentlaunch.RedactError(err, in.project.Config.Env))
		}
		if providerConversationID != "" {
			m.discardForkConversation(ctx, in.cfg.Harness, providerConversationID, in.workspace.Path, env)
		}
		m.rollbackSeedSpawnWorkspace(ctx, in.record, in.workspace, in.workspaceProject, false, in.promptQueued)
		return domain.SessionRecord{}, wrapSpawnStage(id, ErrChatController, agentlaunch.RedactError(err, in.project.Config.Env))
	}

	if fork := in.cfg.Fork; fork != nil && providerConversationID != "" {
		m.pointForkLinksAtFork(ctx, in.cfg.ProjectID, fork.SourceSessionID, id)
	}

	if in.prompt != "" && !in.promptQueued {
		if _, err := m.chat.StartChatTurn(ctx, id, in.prompt); err != nil {
			m.stopChatAfterSpawnFailure(ctx, id)
			m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, in.record, in.workspace, in.workspaceProject, true)
			m.markSpawnFailedTerminatedAfterFailure(ctx, id, false)
			return domain.SessionRecord{}, wrapSpawnStage(id, ErrSpawnDeliverPrompt, agentlaunch.RedactError(err, in.project.Config.Env))
		}
	}

	return m.getRecord(ctx, id)
}

func (m *Manager) aoMCPServers(ctx context.Context, harness domain.AgentHarness, env map[string]string) []ports.ChatMCPServerConfig {
	if harness == domain.HarnessUnreal {
		return nil
	}
	executable, err := m.executable()
	if err != nil || !filepath.IsAbs(executable) {
		m.logger.Warn("chat session gets no ao tool server; the agent can still run ao render",
			"executable", executable, "error", err)
		return nil
	}
	serverEnv := make(map[string]string, 4)
	for _, key := range []string{EnvSessionID, EnvRunFile, EnvDataDir} {
		if value := env[key]; value != "" {
			serverEnv[key] = value
		}
	}
	if m.computerUseEnabled(ctx) {
		serverEnv[envComputerUse] = "1"
	}
	return []ports.ChatMCPServerConfig{{
		Name: "ao", Type: "stdio", Command: executable, Args: []string{"mcp"}, Env: serverEnv,
	}}
}

func (m *Manager) stopChatAfterSpawnFailure(ctx context.Context, id domain.SessionID) {
	cleanupCtx, cancel := spawnRollbackContext(ctx)
	defer cancel()
	m.stopChatBestEffort(cleanupCtx, id)
}

func (m *Manager) stopChatBestEffort(ctx context.Context, id domain.SessionID) {
	if m.chat == nil {
		return
	}
	if err := m.chat.StopChat(ctx, id); err != nil {
		m.logger.Warn("spawn rollback: close chat controller", "sessionID", id, "error", err)
	}
}

func (m *Manager) sendChat(ctx context.Context, id domain.SessionID, message, clientMessageID string, options ports.MessageDeliveryOptions) (bool, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return false, fmt.Errorf("send %s: session: %w", id, err)
	}
	if !ok || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return false, nil
	}
	if m.chat == nil {
		return true, fmt.Errorf("send %s: %w: chat mode is not available in this build",
			id, ports.ErrChatUnsupported)
	}
	if rec.IsTerminated {
		return true, fmt.Errorf("send %s: %w", id, ErrTerminated)
	}
	_, relayErr := m.chat.RelaySessionChatTurn(ctx, id, message, clientMessageID, options)
	if relayErr != nil {
		return true, fmt.Errorf("send %s: %w", id, relayErr)
	}
	return true, nil
}

type SessionModeDefaults interface {
	DefaultSessionMode(ctx context.Context) domain.SessionMode
}

func (m *Manager) resolveSessionMode(ctx context.Context, kind domain.SessionKind, requested domain.SessionMode) domain.SessionMode {
	var defaultMode domain.SessionMode
	if m.defaults != nil {
		defaultMode = m.defaults.DefaultSessionMode(ctx)
	}
	return startSessionMode(kind, requested, defaultMode)
}

func startSessionMode(kind domain.SessionKind, requested, defaultMode domain.SessionMode) domain.SessionMode {
	if requested.Valid() {
		return requested
	}
	if kind == domain.KindOrchestrator {
		return domain.SessionModeChat
	}
	return domain.NormalizeSessionMode(defaultMode)
}

func (m *Manager) resumeChatController(
	ctx context.Context,
	operation string,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	ws ports.WorkspaceInfo,
	requireNativeHistory bool,
	reconnectOnly bool,
	controllerGeneration string,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
	awakeTargets ...domain.AwakeAgentTarget,
) (RestoreResult, error) {
	if m.chat == nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w: chat mode is not available in this build",
			operation, rec.ID, ports.ErrChatUnsupported)
	}
	releaseCodexAdmission, err := m.acquireCodexControllerAdmission(ctx, rec.Harness)
	if err != nil {
		return RestoreResult{}, relaunchFailed("controller admission", fmt.Errorf("%s %s: %w", operation, rec.ID, err))
	}
	defer releaseCodexAdmission()

	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, true)
	if err != nil {
		return RestoreResult{}, relaunchFailed("system prompt", fmt.Errorf("%s %s: system prompt: %w", operation, rec.ID, err))
	}
	agentConfig := restoredAgentConfig(rec, project.Config)
	agentConfig.Permissions = m.importedChatPermissions(ctx, rec, agentConfig.Permissions)
	additionalDirectories, err := m.restoredWorkspaceProjectDirectories(ctx, rec, project, ws.Path)
	if err != nil {
		return RestoreResult{}, relaunchFailed("workspace roots", fmt.Errorf("%s %s: workspace roots: %w", operation, rec.ID, err))
	}
	var env map[string]string
	preparedImportEnv := requireNativeHistory && rec.NeedsImportResume()
	if preparedImportEnv {
		rec, env, err = m.prepareWorkerLaunchEnv(ctx, rec, project.Config.Env)
		if err != nil {
			return RestoreResult{}, relaunchFailed("browser capability", fmt.Errorf("%s %s: browser capability: %w", operation, rec.ID, err))
		}
	} else {
		env = m.runtimeEnvForHarness(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, rec.Harness)
	}
	if agent, ok := m.agents.Agent(rec.Harness); ok {
		m.augmentAgentRuntimeEnv(agent, env)
	}
	m.applyImportedConfigEnv(ctx, rec, env)

	historyMode := ports.ChatHistoryImport
	var providerHandoff *domain.ChatProviderHandoff
	if requireNativeHistory {
		historyMode = ports.ChatHistoryRequired
		providerHandoff, err = m.prepareLiveChatProviderHandoff(ctx, rec)
	} else {
		providerHandoff, err = m.prepareRecoveredChatProviderHandoff(ctx, rec)
	}
	if err != nil {
		return RestoreResult{}, relaunchFailed("provider ownership", fmt.Errorf("%s %s: recover provider ownership: %w", operation, rec.ID, err))
	}
	rec, launchPath, err := m.importLaunch(ctx, operation, rec, ws.Path, env, preparedImportEnv)
	if err != nil {
		return RestoreResult{}, err
	}
	if rec.NeedsImportResume() && !reconnectOnly {
		if rec.Metadata.Model == "" && roleOverride(rec.Kind, project.Config).Harness == rec.Harness {
			rec.Metadata.Model = effectiveAgentConfig(rec.Harness, rec.Kind, project.Config).Model
			agentConfig.Model = rec.Metadata.Model
		}
		m.applyImportedConfigEnv(ctx, rec, env)
		if agent, ok := m.agents.Agent(rec.Harness); ok {
			if err := m.prepareWorkspace(ctx, agent, rec.ID, launchPath, systemPrompt, "", agentConfig, env); err != nil {
				return RestoreResult{}, relaunchFailed("import workspace preparation", fmt.Errorf("%s %s: %w", operation, rec.ID, err))
			}
		}
	}
	freshIfMissing := !requireNativeHistory && !reconnectOnly && providerHandoff == nil && m.providerNeverPersisted(ctx, rec)
	var awakeTarget *domain.AwakeAgentTarget
	if len(awakeTargets) > 0 {
		if len(awakeTargets) != 1 || !reconnectOnly || awakeTargets[0].SessionID != rec.ID || awakeTargets[0].Owner != rec.ControllerOwner() {
			return RestoreResult{}, fmt.Errorf("awake reconnect ownership changed")
		}
		target := awakeTargets[0]
		awakeTarget = &target
	}
	var completionErr error
	_, err = m.chat.StartChat(ctx, ChatStart{
		AwakeReconnectTarget:    awakeTarget,
		ReconnectOnly:           reconnectOnly,
		SessionID:               rec.ID,
		ProjectID:               rec.ProjectID,
		Kind:                    rec.Kind,
		Harness:                 rec.Harness,
		DataDir:                 m.dataDir,
		WorkspacePath:           launchPath,
		Env:                     env,
		Model:                   agentConfig.Model,
		Effort:                  agentConfig.Effort,
		Permissions:             agentConfig.Permissions,
		SystemPrompt:            systemPrompt,
		AdditionalDirectories:   additionalDirectories,
		MCPServers:              m.aoMCPServers(ctx, rec.Harness, env),
		ExpectedControllerOwner: rec.ControllerOwner(),
		PrepareControllerEnv: func(launchCtx context.Context, expected domain.SessionControllerOwner) (map[string]string, error) {
			if preparedImportEnv {
				prepared, prepareErr := m.persistBrowserCapabilityVerifier(launchCtx, rec, expected, rec.Metadata.BrowserCapabilityVerifier)
				if prepareErr != nil {
					return nil, prepareErr
				}
				rec = prepared
				return maps.Clone(env), nil
			}
			prepared, launchEnv, prepareErr := m.prepareChatControllerEnv(
				launchCtx, rec, project.Config.Env, expected,
			)
			if prepareErr != nil {
				return nil, prepareErr
			}
			if agent, ok := m.agents.Agent(rec.Harness); ok {
				m.augmentAgentRuntimeEnv(agent, launchEnv)
			}
			m.applyImportedConfigEnv(launchCtx, rec, launchEnv)

			rec = prepared
			return launchEnv, nil
		},
		ProviderConversationID:             rec.Metadata.ProviderConversationID,
		FreshIfProviderConversationMissing: freshIfMissing,
		ProviderHandoff:                    providerHandoff,
		ControllerGeneration:               controllerGeneration,
		HistoryMode:                        historyMode,
		HistoryPolicy:                      historyPolicy,
		ControllerReady: func(started ChatStarted) (ChatControllerCommit, error) {
			if rec.NeedsImportResume() && started.ProviderConversationID != importedNativeRef(rec.Metadata.ImportSource).NativeSessionID {
				return ChatControllerCommit{}, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, importNotice("the provider did not resume the original conversation"))
			}

			metadata := rec.Metadata
			if metadata.ImportSource != nil {
				adopted := *metadata.ImportSource
				if !adopted.Adopted {
					through, err := m.importReplayedThrough(ctx, rec.ID)
					if err != nil {
						return ChatControllerCommit{}, err
					}
					adopted.HistoryThrough = through
				}
				adopted.Adopted = true
				metadata.ImportSource = &adopted
			}

			metadata.WorkspacePath = ws.Path
			metadata.WorkspaceRepoPath = ws.RepoPath
			if ws.Branch != "" {
				metadata.Branch = ws.Branch
			}
			if freshIfMissing && started.ProviderConversationID != rec.Metadata.ProviderConversationID {
				if err := m.replaceUnpersistedChatProvider(ctx, rec, started.ProviderConversationID); err != nil {
					completionErr = err
					return ChatControllerCommit{}, err
				}
				rec.Metadata.ProviderConversationID = started.ProviderConversationID
				freshIfMissing = false
			}
			metadata.ProviderConversationID = started.ProviderConversationID
			metadata.ControllerGeneration = started.ControllerGeneration

			committedConversation, commitErr := m.markChatControllerSpawned(
				ctx, rec.ID, metadata, started.Conversation, started.ProviderBoundary,
				started.CommitProviderHistory, providerHandoff, started.LiveReconnect,
			)
			if commitErr == nil {
				providerHandoff = nil
				rec.Metadata.ImportSource = metadata.ImportSource
			}
			completionErr = commitErr
			return ChatControllerCommit{
				Conversation: committedConversation,
				ControllerOwner: chatControllerOwner(
					rec, rec.Harness, started.ProviderConversationID, started.ControllerGeneration,
				),
			}, completionErr
		},
	})
	if err != nil {
		if completionErr != nil {
			m.stopChatBestEffort(ctx, rec.ID)
			return RestoreResult{}, relaunchFailed("record launch", fmt.Errorf("%s %s: completed: %w", operation, rec.ID, completionErr))
		}
		if rec.Metadata.ImportSource != nil && providerRefusedImportOwnership(err) {
			return RestoreResult{}, ErrSessionOpenElsewhere
		}
		m.requireChatLogin(ctx, rec.ID, err)
		return RestoreResult{}, relaunchFailed("chat start", fmt.Errorf("%s %s: resume chat: %w", operation, rec.ID, agentlaunch.RedactError(err, project.Config.Env)))
	}

	restored, err := m.getRecord(ctx, rec.ID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%w: load resumed chat after native start: %w", ports.ErrChatRecoveryInconclusive, err)
	}
	return RestoreResult{Session: restored, Mode: RestoreModeNative}, nil
}

func (m *Manager) requireChatLogin(ctx context.Context, id domain.SessionID, err error) {
	reason := ""
	for _, sentinel := range []error{ports.ErrChatAuthRequired, ports.ErrAgentAuthRequired} {
		if errors.Is(err, sentinel) {
			reason = sentinel.Error()
			break
		}
	}
	if reason == "" || m.accountOverride(id) != "" {
		return
	}
	store, ok := m.store.(interface {
		RequireSessionLogin(context.Context, domain.SessionID, string, time.Time) error
	})
	if !ok {
		return
	}
	if err := store.RequireSessionLogin(ctx, id, reason, m.clock()); err != nil {
		m.logger.Warn("record chat sign-in demand", "sessionID", id, "error", err)
	}
}

type unpersistedChatProviderStore interface {
	ReplaceUnpersistedChatProvider(ctx context.Context, id domain.SessionID, expectedProviderConversationID, providerConversationID string) error
}

func (m *Manager) providerNeverPersisted(ctx context.Context, rec domain.SessionRecord) bool {
	id := rec.Metadata.ProviderConversationID
	if id == "" {
		return false
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	handoff, ok := agent.(ports.AgentInterfaceHandoff)
	if !ok {
		return false
	}
	if _, ok := m.store.(unpersistedChatProviderStore); !ok {
		return false
	}
	persisted, err := m.persistedNativeConversationID(ctx, rec, id, handoff)
	return err == nil && persisted == ""
}

func (m *Manager) replaceUnpersistedChatProvider(ctx context.Context, rec domain.SessionRecord, providerConversationID string) error {
	store, ok := m.store.(unpersistedChatProviderStore)
	if !ok {
		return errors.New("replace unpersisted Chat provider: storage is unavailable")
	}
	return store.ReplaceUnpersistedChatProvider(ctx, rec.ID, rec.Metadata.ProviderConversationID, providerConversationID)
}

func (m *Manager) markChatControllerSpawned(
	ctx context.Context,
	id domain.SessionID,
	metadata domain.SessionMetadata,
	conversation domain.ConversationRecord,
	providerBoundary *domain.ConversationBranch,
	commitProviderHistory func(context.Context) error,
	handoff *domain.ChatProviderHandoff,
	liveReconnect bool,
) (domain.ConversationRecord, error) {
	if liveReconnect {
		if providerBoundary != nil {
			return domain.ConversationRecord{}, errors.New("live Chat reconnect cannot replace the provider boundary")
		}
		return conversation, m.lcm.MarkChatReconnected(ctx, id, metadata)
	}
	if handoff != nil && (providerBoundary == nil || commitProviderHistory == nil) {
		return domain.ConversationRecord{}, errors.New("native Chat handoff requires atomic history publication")
	}
	if providerBoundary == nil {
		return conversation, m.lcm.MarkSpawned(ctx, id, metadata)
	}
	var err error
	if commitProviderHistory == nil {
		err = m.lcm.MarkChatSpawned(ctx, id, metadata, *providerBoundary)
	} else {
		prepared, ok := m.lcm.(interface {
			MarkChatSpawnedPrepared(
				context.Context,
				domain.SessionID,
				domain.SessionMetadata,
				domain.ConversationBranch,
				*domain.ChatProviderHandoff,
				func(context.Context) error,
			) error
		})
		if !ok {
			return domain.ConversationRecord{}, errors.New(
				"atomic Chat provider-history persistence is unavailable",
			)
		}
		err = prepared.MarkChatSpawnedPrepared(
			ctx, id, metadata, *providerBoundary, handoff, commitProviderHistory,
		)
	}
	if err != nil {
		return domain.ConversationRecord{}, err
	}
	conversation.ActiveBranchID = providerBoundary.ID
	conversation.SessionID = id
	conversation.UpdatedAt = providerBoundary.CreatedAt
	return conversation, nil
}

func workspaceProjectDirectories(root string, project *ports.WorkspaceProjectInfo) []string {
	if project == nil {
		return nil
	}
	root = filepath.Clean(root)
	directories := make([]string, 0, len(project.Worktrees))
	for _, worktree := range project.Worktrees {
		if worktree.Path == "" || filepath.Clean(worktree.Path) == root {
			continue
		}
		directories = append(directories, worktree.Path)
	}
	return directories
}

func (m *Manager) restoredWorkspaceProjectDirectories(
	ctx context.Context,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	root string,
) ([]string, error) {
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return nil, nil
	}
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	directories := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.WorktreePath == "" || filepath.Clean(row.WorktreePath) == root {
			continue
		}
		directories = append(directories, row.WorktreePath)
	}
	return directories, nil
}
