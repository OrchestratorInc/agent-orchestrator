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

// chatStandbyTTL bounds how long a picked-but-unsent agent keeps its process.
const chatStandbyTTL = 2 * time.Minute

// chatStandbyTakeWait bounds how long a switch waits for a prewarm still
// starting its agent. Past it the switch starts the agent itself.
var chatStandbyTakeWait = 10 * time.Second

// chatStandbyLauncher starts a provider standby for a session's next agent
// switch. The Chat service implements it for drivers that support standbys.
type chatStandbyLauncher interface {
	SupportsChatStandby(harness domain.AgentHarness) bool
	PrewarmChatStandby(ctx context.Context, cfg ChatStart) (ports.ChatStandby, error)
}

// chatSwitchStandby is the target agent of a switch the user has picked but not
// sent, already started and initialized. It holds no provider session, and its
// browser bearer is inert until the switch persists verifier, at the same point
// a cold switch rotates the credential.
type chatSwitchStandby struct {
	session     domain.SessionID
	target      domain.AgentHarness
	model       string
	effort      string
	permissions ports.PermissionMode
	env         map[string]string
	verifier    string
	handle      ports.ChatStandby
	targetAgent ports.Agent
	setupEnv    map[string]string
	workspace   string
	// keepWorkspace leaves the agent files in place when the standby expires:
	// a new chat's worktree may by then belong to a session using them.
	keepWorkspace bool
	timer         *time.Timer
}

// chatStandbyLock serializes prewarm and takeover for one session, so a slow
// prewarm never rewrites the workspace files of a switch already running. It
// is a one-slot channel so a switch can stop waiting.
func (m *Manager) chatStandbyLock(id domain.SessionID) chan struct{} {
	m.chatStandbyMu.Lock()
	defer m.chatStandbyMu.Unlock()
	if m.chatStandbyLocks == nil {
		m.chatStandbyLocks = make(map[domain.SessionID]chan struct{})
	}
	lock := m.chatStandbyLocks[id]
	if lock == nil {
		lock = make(chan struct{}, 1)
		m.chatStandbyLocks[id] = lock
	}
	return lock
}

func (m *Manager) chatStandbyEpoch(id domain.SessionID) uint64 {
	m.chatStandbyMu.Lock()
	defer m.chatStandbyMu.Unlock()
	return m.chatStandbyEpochs[id]
}

// supersedeChatStandby records that a launch went ahead without waiting for a
// prewarm still in flight.
func (m *Manager) supersedeChatStandby(id domain.SessionID) {
	m.chatStandbyMu.Lock()
	defer m.chatStandbyMu.Unlock()
	if m.chatStandbyEpochs == nil {
		m.chatStandbyEpochs = make(map[domain.SessionID]uint64)
	}
	m.chatStandbyEpochs[id]++
}

// PrewarmChatSwitch starts the agent the user picked for a chat's next switch,
// so the send that carries the switch skips process launch and initialization.
// Picking the session's current agent drops any standby. Unsupported targets
// are a no-op: the switch then starts its agent as usual.
func (m *Manager) PrewarmChatSwitch(ctx context.Context, id domain.SessionID, cfg SwitchAgentConfig) error {
	lock := m.chatStandbyLock(id)
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-lock }()
	m.dropChatStandby(id, true)
	epoch := m.chatStandbyEpoch(id)

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("prewarm switch %s: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("prewarm switch %s: %w", id, ErrNotFound)
	}
	target := cfg.TargetHarness
	if rec.IsTerminated || rec.Kind != domain.KindWorker ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat ||
		rec.Harness == target || strings.TrimSpace(rec.Metadata.WorkspacePath) == "" {
		return nil
	}
	if m.agentOperationActive(id) {
		return nil
	}
	if !m.supportsChatStandby(target) {
		return nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return fmt.Errorf("prewarm switch %s: project: %w", id, err)
	}
	agentConfig, systemPrompt, err := m.chatSwitchTargetConfig(ctx, rec, project, cfg)
	if err != nil {
		return fmt.Errorf("prewarm switch %s: %w", id, err)
	}
	workspace := rec.Metadata.WorkspacePath
	additionalDirectories, err := m.restoredWorkspaceProjectDirectories(ctx, rec, project, workspace)
	if err != nil {
		return fmt.Errorf("prewarm switch %s: workspace roots: %w", id, err)
	}
	err = m.startChatStandby(ctx, chatStandbyStart{
		record: rec, project: project, target: target, agentConfig: agentConfig,
		systemPrompt: systemPrompt, workspace: workspace, additionalDirectories: additionalDirectories,
		liveHarness: rec.Harness,
		// A switch that stops waiting for this prewarm moves the epoch on; one
		// still waiting takes the standby when this returns.
		ttl: chatStandbyTTL, epoch: epoch,
	})
	if err != nil {
		return fmt.Errorf("prewarm switch %s: %w", id, err)
	}
	return nil
}

// workspaceFilesShared reports agents whose workspace setup writes the same
// files, so preparing one ahead of a switch would change the other's while it
// still runs. opencode v2 replaces v1's activity plugin.
func workspaceFilesShared(live, target domain.AgentHarness) bool {
	opencode := func(h domain.AgentHarness) bool {
		return h == domain.HarnessOpenCode || h == domain.HarnessOpenCodeV2
	}
	return live != "" && opencode(live) && opencode(target)
}

func (m *Manager) supportsChatStandby(harness domain.AgentHarness) bool {
	launcher, ok := m.chat.(chatStandbyLauncher)
	return ok && launcher.SupportsChatStandby(harness)
}

// chatStandbyStart is everything a later Chat launch of record will use.
type chatStandbyStart struct {
	record                domain.SessionRecord
	project               domain.ProjectRecord
	target                domain.AgentHarness
	agentConfig           ports.AgentConfig
	systemPrompt          string
	workspace             string
	additionalDirectories []string
	ttl                   time.Duration
	keepWorkspaceOnExpiry bool
	// liveHarness is the agent still running in workspace, for a switch. Its
	// session prompt file is left alone, and so are its files when the target
	// writes into the same place.
	liveHarness domain.AgentHarness
	// epoch is the session's standby epoch when the prewarm took its lock.
	epoch uint64
	// abandoned reports, once the provider is up, that the launch it was for
	// will no longer look for it.
	abandoned func() bool
}

// startChatStandby prepares the target's workspace files, builds its launch
// environment, and starts its provider without a session. The caller holds the
// session's standby lock and has dropped any earlier standby.
func (m *Manager) startChatStandby(ctx context.Context, in chatStandbyStart) error {
	launcher, ok := m.chat.(chatStandbyLauncher)
	if !ok {
		return nil
	}
	rec := in.record
	id := rec.ID
	// A launch that stopped waiting owns the workspace files now.
	superseded := func() bool { return m.chatStandbyEpoch(id) != in.epoch }
	if superseded() {
		return nil
	}
	targetAgent, ok := m.agents.Agent(in.target)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownHarness, in.target)
	}
	setupEnv := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, in.project.Config.Env)
	m.augmentAgentRuntimeEnv(targetAgent, setupEnv)
	// Agents read their hook and trust files at startup, so they go in before
	// the process does, and the launch rewrites them with its final
	// instructions. A running chat's prompt file stays the live agent's until
	// the switch commits.
	systemPromptFile := ""
	if in.liveHarness == "" {
		var err error
		systemPromptFile, err = m.prepareSystemPromptFile(rec.ID, in.target, in.systemPrompt)
		if err != nil {
			return fmt.Errorf("system prompt file: %w", err)
		}
	}
	if !workspaceFilesShared(in.liveHarness, in.target) {
		if err := m.prepareWorkspace(ctx, targetAgent, id, in.workspace,
			in.systemPrompt, systemPromptFile, in.agentConfig, setupEnv); err != nil {
			return fmt.Errorf("prepare workspace: %w", err)
		}
	}
	// After a launch stopped waiting, the agent files are the launch's.
	cleanupFiles := func() {
		if !superseded() {
			m.cleanupPreparedAgentWorkspace(ctx, targetAgent, id, in.workspace, setupEnv)
		}
	}
	env, verifier, err := m.launchRuntimeEnv(rec.ID, rec.ProjectID, rec.IssueID, in.project.Config.Env)
	if err != nil {
		cleanupFiles()
		return fmt.Errorf("runtime env: %w", err)
	}
	m.augmentAgentRuntimeEnv(targetAgent, env)
	started := time.Now()
	handle, err := launcher.PrewarmChatStandby(ctx, ChatStart{
		SessionID:             id,
		ProjectID:             rec.ProjectID,
		Kind:                  rec.Kind,
		Harness:               in.target,
		DataDir:               m.dataDir,
		WorkspacePath:         in.workspace,
		Env:                   env,
		Model:                 in.agentConfig.Model,
		Effort:                in.agentConfig.Effort,
		Permissions:           in.agentConfig.Permissions,
		SystemPrompt:          in.systemPrompt,
		AdditionalDirectories: in.additionalDirectories,
	})
	if err != nil {
		cleanupFiles()
		if errors.Is(err, ports.ErrChatUnsupported) {
			return nil
		}
		return err
	}
	if superseded() || in.abandoned != nil && in.abandoned() {
		handle.Discard()
		return nil
	}
	standby := &chatSwitchStandby{
		session: id, target: in.target, model: in.agentConfig.Model, effort: in.agentConfig.Effort,
		permissions: in.agentConfig.Permissions, env: env, verifier: verifier, handle: handle,
		targetAgent: targetAgent, setupEnv: setupEnv, workspace: in.workspace,
		keepWorkspace: in.keepWorkspaceOnExpiry,
	}
	standby.timer = time.AfterFunc(in.ttl, func() { m.expireChatStandby(id, standby) })
	m.chatStandbyMu.Lock()
	if m.chatStandbys == nil {
		m.chatStandbys = make(map[domain.SessionID]*chatSwitchStandby)
	}
	m.chatStandbys[id] = standby
	m.chatStandbyMu.Unlock()
	m.logger.Info("agent standby ready", "sessionID", id, "target", in.target,
		"duration", time.Since(started).Round(time.Millisecond))
	return nil
}

// chatSwitchTargetConfig resolves the target's agent config and standing
// instructions exactly as the switch saga does.
func (m *Manager) chatSwitchTargetConfig(
	ctx context.Context,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	cfg SwitchAgentConfig,
) (ports.AgentConfig, string, error) {
	base := effectiveAgentConfig(cfg.TargetHarness, rec.Kind, project.Config)
	base.Permissions = m.chatSwitchPermissions(ctx, rec, base.Permissions)
	agentConfig, err := m.resolveAgentConfig(ctx, ports.SpawnConfig{
		ProjectID: rec.ProjectID,
		Kind:      rec.Kind,
		Harness:   cfg.TargetHarness,
		AgentConfig: ports.AgentConfig{
			Model:  strings.TrimSpace(cfg.Model),
			Effort: strings.TrimSpace(cfg.Effort),
		},
		EffortOverride: strings.TrimSpace(cfg.Effort) != "",
	}, domain.ProjectConfig{AgentConfig: base})
	if err != nil {
		return ports.AgentConfig{}, "", fmt.Errorf("target config: %w", err)
	}
	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, true)
	if err != nil {
		return ports.AgentConfig{}, "", fmt.Errorf("system prompt: %w", err)
	}
	return agentConfig, appendAgentContinuationProtocol(systemPrompt), nil
}

// takeChatStandby hands a switch the standby for its target, waiting for a
// prewarm still in flight. A standby for another target or other settings is
// dropped, with its workspace files when they belong to a different agent.
func (m *Manager) takeChatStandby(id domain.SessionID, target domain.AgentHarness, agentConfig ports.AgentConfig) *chatSwitchStandby {
	m.chatStandbyMu.Lock()
	_, held := m.chatStandbyLocks[id]
	m.chatStandbyMu.Unlock()
	if !held {
		// No prewarm ever ran for this session.
		return nil
	}
	lock := m.chatStandbyLock(id)
	wait := time.NewTimer(chatStandbyTakeWait)
	defer wait.Stop()
	select {
	case lock <- struct{}{}:
	case <-wait.C:
		m.supersedeChatStandby(id)
		return nil
	}
	defer func() { <-lock }()
	m.chatStandbyMu.Lock()
	standby := m.chatStandbys[id]
	delete(m.chatStandbys, id)
	m.chatStandbyMu.Unlock()
	if standby == nil {
		return nil
	}
	standby.timer.Stop()
	if standby.target != target {
		m.releaseChatStandby(standby, true)
		return nil
	}
	if standby.model != agentConfig.Model || standby.effort != agentConfig.Effort ||
		ports.NormalizePermissionMode(standby.permissions) != ports.NormalizePermissionMode(agentConfig.Permissions) {
		// Same agent: the switch prepares and owns its workspace files.
		m.releaseChatStandby(standby, false)
		return nil
	}
	return standby
}

// controllerEnv returns the standby's launch environment and persists its
// browser verifier once, replacing the cold switch's credential rotation.
func (s *chatSwitchStandby) controllerEnv(
	ctx context.Context,
	m *Manager,
	rec *domain.SessionRecord,
	expected domain.SessionControllerOwner,
) func() (map[string]string, error) {
	var once sync.Once
	var err error
	return func() (map[string]string, error) {
		once.Do(func() {
			var prepared domain.SessionRecord
			prepared, err = m.persistBrowserCapabilityVerifier(ctx, *rec, expected, s.verifier)
			if err == nil {
				*rec = prepared
			}
		})
		if err != nil {
			return nil, fmt.Errorf("persist browser capability verifier: %w", err)
		}
		return maps.Clone(s.env), nil
	}
}

func (m *Manager) expireChatStandby(id domain.SessionID, standby *chatSwitchStandby) {
	lock := m.chatStandbyLock(id)
	lock <- struct{}{}
	defer func() { <-lock }()
	m.chatStandbyMu.Lock()
	if m.chatStandbys[id] != standby {
		m.chatStandbyMu.Unlock()
		return
	}
	delete(m.chatStandbys, id)
	m.chatStandbyMu.Unlock()
	m.releaseChatStandby(standby, !standby.keepWorkspace)
}

// dropChatStandby discards a session's standby. The caller holds its lock.
func (m *Manager) dropChatStandby(id domain.SessionID, cleanWorkspace bool) {
	m.chatStandbyMu.Lock()
	standby := m.chatStandbys[id]
	delete(m.chatStandbys, id)
	m.chatStandbyMu.Unlock()
	if standby != nil {
		standby.timer.Stop()
		m.releaseChatStandby(standby, cleanWorkspace)
	}
}

func (m *Manager) releaseChatStandby(standby *chatSwitchStandby, cleanWorkspace bool) {
	standby.handle.Discard()
	if !cleanWorkspace {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m.cleanupPreparedAgentWorkspace(ctx, standby.targetAgent, standby.session, standby.workspace, standby.setupEnv)
}

func (m *Manager) agentOperationActive(id domain.SessionID) bool {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	_, active := m.agentOperations[id]
	return active
}
