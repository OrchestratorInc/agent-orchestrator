package grok

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var grokBinarySpec = binaryutil.BinarySpec{
	Label:         "grok",
	Names:         []string{"grok"},
	WinNames:      []string{"grok.cmd", "grok.exe", "grok"},
	UnixPaths:     []string{"/usr/local/bin/grok", "/opt/homebrew/bin/grok"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("grok", []string{".grok", "bin", "grok"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "grok.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "grok.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".grok", "bin", "grok.exe"}},
	},
}

const (
	grokClaudeSettingsDirName  = ".claude"
	grokClaudeSettingsFileName = "settings.local.json"
	grokHookCommandPrefix      = "ao hooks grok "
	grokHookTimeout            = 30
)

var grokStartupMatcher = "startup"

var grokManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &grokStartupMatcher, Command: grokHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: grokHookCommandPrefix + "user-prompt-submit"},
	{Event: "PreToolUse", Command: grokHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: grokHookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFailure", Command: grokHookCommandPrefix + "post-tool-use-failure"},
	{Event: "PermissionRequest", Command: grokHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: grokHookCommandPrefix + "stop"},
	{Event: "Notification", Command: grokHookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: grokHookCommandPrefix + "session-end"},
}

var grokHooks = hooksjson.Manager{
	Label:         "grok",
	CommandPrefix: grokHookCommandPrefix,
	Timeout:       grokHookTimeout,
	Path:          grokClaudeSettingsPath,
	Managed:       grokManagedHooks,
}

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "grok",
		Name:        "Grok Build",
		Description: "Run xAI Grok Build worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `grok --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.grokBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary, "--no-auto-update"}
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")

	systemPrompt, err := launchSystemPromptText(cfg)
	if err != nil {
		return nil, err
	}
	if systemPrompt != "" {
		cmd = append(cmd, "--rules", systemPrompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

const grokTrustDialogMarker = "Do you trust the contents of this directory?"

func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay:     750 * time.Millisecond,
		Patterns:         []string{"Grok Build"},
		PollInterval:     200 * time.Millisecond,
		Timeout:          8 * time.Second,
		Lines:            80,
		RenderedScreen:   true,
		BlockingPatterns: []string{grokTrustDialogMarker},
	}, nil
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return grokHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return grokHooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return grokHooks.AreInstalled(ctx, workspacePath)
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.grokBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 4)
	cmd = append(cmd, binary, "--no-auto-update")
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	systemPrompt, err := restoreSystemPromptText(cfg)
	if err != nil {
		return nil, false, err
	}
	if systemPrompt != "" {
		cmd = append(cmd, "--rules", systemPrompt)
	}
	cmd = append(cmd, "-r", agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func ResolveGrokBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, grokBinarySpec)
}

func (p *Plugin) grokBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveGrokBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func grokClaudeSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, grokClaudeSettingsDirName, grokClaudeSettingsFileName)
}

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--permission-mode", "acceptEdits")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--permission-mode", "auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--permission-mode", "bypassPermissions")
	}
}

func launchSystemPromptText(cfg ports.LaunchConfig) (string, error) {
	return systemPromptTextFrom(cfg.SystemPrompt, cfg.SystemPromptFile)
}

func restoreSystemPromptText(cfg ports.RestoreConfig) (string, error) {
	return systemPromptTextFrom(cfg.SystemPrompt, cfg.SystemPromptFile)
}

func systemPromptTextFrom(inline, file string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if file == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", fmt.Errorf("grok: read system prompt file: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)
