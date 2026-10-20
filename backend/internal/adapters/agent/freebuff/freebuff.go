// Package freebuff integrates the Freebuff terminal coding agent
// (https://freebuff.com, npm package "freebuff").
//
// Freebuff's CLI exposes only `--cwd`, `--continue [conversation-id]` and the
// `login` subcommand: there is no prompt argument, model flag or
// system-prompt flag. AO therefore:
//
//   - launches `freebuff --cwd <workspace>` and types the first turn after the
//     composer is ready, carrying AO's standing instructions in that turn;
//   - applies a model/effort choice through Freebuff's own settings.json
//     (`freebuffModel`, `freebuffReasoningEfforts`), the same preference its
//     /model picker persists, holding it until the launched TUI has read it;
//   - restores with `--continue <chat-id>`, resolving the exact chat by the
//     project root Freebuff records in each chat's run-state.json, inside the
//     config directory that session's process actually used.
//
// Freebuff has no approval prompts, so every AO permission mode launches the
// same command.
package freebuff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "freebuff"

var freebuffBinarySpec = binaryutil.BinarySpec{
	Label:         "freebuff",
	Names:         []string{"freebuff"},
	WinNames:      []string{"freebuff.cmd", "freebuff.exe", "freebuff"},
	UnixPaths:     []string{"/usr/local/bin/freebuff", "/opt/homebrew/bin/freebuff"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("freebuff"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "freebuff.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "freebuff.exe"}},
	},
}

// Plugin is the Freebuff TUI adapter. The resolved binary path is cached under
// binaryMu.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	leases         *modelLeases
}

// New returns a ready-to-register Freebuff adapter.
func New() *Plugin { return &Plugin{leases: newModelLeases()} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAfterStartPromptBuilder = (*Plugin)(nil)
var _ ports.AgentExitDetector = (*Plugin)(nil)
var _ ports.AgentStartupReadyObserver = (*Plugin)(nil)
var _ ports.AgentAfterStartSubmitConfirmer = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:           adapterID,
		Name:         "Freebuff",
		Description:  "Run Freebuff terminal coding sessions.",
		Version:      "0.1.0",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// ExitDetectionMode opts Freebuff into AO's process supervisor: Freebuff has
// no lifecycle hooks to report that its TUI exited.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// GetConfigSpec reports the model and reasoning-effort choices AO applies via
// Freebuff's settings.json before launch.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelEffortConfigSpec(ctx,
		"Freebuff model id, saved as Freebuff's picker selection before launch.",
		"Reasoning effort for the chosen model; Freebuff ignores rungs the model does not offer.")
}

// GetLaunchCommand builds `freebuff --cwd <workspace>`. The model choice is
// applied by GetAgentHooks, which sees the session's real environment.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspace, err := validateWorkspace(cfg.WorkspacePath)
	if err != nil {
		return nil, err
	}
	binary, err := p.freebuffBinary(ctx)
	if err != nil {
		return nil, err
	}
	return []string{binary, "--cwd", workspace}, nil
}

// GetPromptDeliveryStrategy keeps task text out of argv: Freebuff has no
// prompt argument, so the first turn is typed once the composer is ready.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

// BuildAfterStartPrompt sends AO's standing instructions with the task as one
// clearly marked first turn, because Freebuff has no per-session
// system-prompt input. A restored conversation keeps them in its history.
func (p *Plugin) BuildAfterStartPrompt(ctx context.Context, cfg ports.LaunchConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" && cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return "", fmt.Errorf("freebuff: read standing instructions: %w", err)
		}
		standing = strings.TrimSpace(string(data))
	}
	task := strings.TrimSpace(cfg.Prompt)
	if standing == "" {
		return task, nil
	}
	if task == "" {
		return "## AO Standing Instructions\n\n" + standing +
			"\n\nFollow these instructions for the rest of this session. Reply briefly that you are ready, then wait for a task.", nil
	}
	return "## AO Standing Instructions\n\n" + standing + "\n\n## AO Task\n\n" + task, nil
}

// GetAgentHooks installs no hooks (Freebuff has none). It is the one
// pre-launch step AO runs with the session's real environment on spawn,
// restore and agent switch, so it records the config directory this session's
// Freebuff uses and writes the requested model there. The selection stays
// leased until AgentStartupReady, so a concurrent launch with another model
// cannot replace it before this process reads it.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.SessionID == "" {
		return nil
	}
	dir := configDir(cfg.Env)
	if err := writeSessionRecord(cfg.DataDir, cfg.SessionID, dir); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Config.Model) == "" {
		return nil
	}
	return p.modelLeases().acquire(ctx, dir, cfg.SessionID, modelSelection(cfg.Config), func() error {
		return applyModelSelection(dir, cfg.Config)
	})
}

// AgentStartupReady releases the session's model lease: a Freebuff TUI that
// shows its composer has already read settings.json.
func (p *Plugin) AgentStartupReady(sessionID string) {
	p.modelLeases().release(sessionID)
}

func (p *Plugin) modelLeases() *modelLeases {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.leases == nil {
		p.leases = newModelLeases()
	}
	return p.leases
}

// GetRestoreCommand continues the session's exact Freebuff chat. Without a
// recorded chat for this workspace it reports ok=false rather than letting
// `--continue` fall back to whichever chat is most recent.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	workspace, err := validateWorkspace(cfg.Session.WorkspacePath)
	if err != nil {
		return nil, false, err
	}
	dir := sessionConfigDir(cfg.DataDir, cfg.Session.ID)
	chatID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if chatID == "" || !chatExists(dir, workspace, chatID) {
		chatID, err = latestChatID(dir, workspace)
		if err != nil {
			return nil, false, err
		}
	}
	if chatID == "" {
		return nil, false, nil
	}
	binary, err := p.freebuffBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	return []string{binary, "--cwd", workspace, "--continue", chatID}, true, nil
}

// SessionInfo reports the Freebuff chat id backing this session's workspace.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	if info.AgentSessionID == "" && session.WorkspacePath != "" {
		if chatID, err := latestChatID(sessionConfigDir(session.DataDir, session.ID), session.WorkspacePath); err == nil && chatID != "" {
			info.AgentSessionID = chatID
			ok = true
		}
	}
	return info, ok, nil
}

// ResolveBinary resolves the executable path for the plugin.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.freebuffBinary(ctx)
}

// InvalidateBinaryResolution forgets the cached executable path, so the next
// launch resolves it again: after AO installs a managed copy, for example.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func (p *Plugin) freebuffBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, freebuffBinarySpec)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func validateWorkspace(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("freebuff: workspace must be an absolute directory path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("freebuff: workspace unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("freebuff: workspace must be a directory")
	}
	return path, nil
}
