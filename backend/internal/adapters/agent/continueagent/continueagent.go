package continueagent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "continue"

const (
	continueClaudeSettingsDirName  = ".claude"
	continueClaudeSettingsFileName = "settings.local.json"
	continueHookCommandPrefix      = "ao hooks continue "
	continueHookTimeout            = 30
)

var continueBinarySpec = binaryutil.BinarySpec{
	Label:         "cn",
	Names:         []string{"cn"},
	WinNames:      []string{"cn.cmd", "cn.exe", "cn"},
	UnixPaths:     []string{"/usr/local/bin/cn", "/opt/homebrew/bin/cn"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("cn"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "cn.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "cn.exe"}},
	},
}

var continueSessionStartMatcher = "startup|resume|clear|compact"

var continueManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &continueSessionStartMatcher, Command: continueHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: continueHookCommandPrefix + "user-prompt-submit"},
	{Event: "PreToolUse", Command: continueHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: continueHookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFailure", Command: continueHookCommandPrefix + "post-tool-use-failure"},
	{Event: "PermissionRequest", Command: continueHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: continueHookCommandPrefix + "stop"},
	{Event: "Notification", Command: continueHookCommandPrefix + "notification"},
	{Event: "SubagentStop", Command: continueHookCommandPrefix + "subagent-stop"},
	{Event: "SessionEnd", Command: continueHookCommandPrefix + "session-end"},
}

var continueHooks = hooksjson.Manager{
	Label:                 adapterID,
	CommandPrefix:         continueHookCommandPrefix,
	LegacyCommandPrefixes: []string{"ao hooks claude-code "},
	Timeout:               continueHookTimeout,
	Path:                  continueClaudeSettingsPath,
	Managed:               continueManagedHooks,
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
		ID:          adapterID,
		Name:        "Continue",
		Description: "Run Continue CLI worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model slug passed to `cn --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.continueBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	appendSystemPromptRule(&cmd, cfg.SystemPrompt, cfg.SystemPromptFile)

	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cfg.Prompt != "" {
		return ports.PromptDeliveryInCommand, nil
	}
	return ports.PromptDeliveryAfterStart, nil
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return continueHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.continueBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 5)
	cmd = append(cmd, binary)
	appendApprovalFlags(&cmd, cfg.Permissions)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	appendSystemPromptRule(&cmd, cfg.SystemPrompt, cfg.SystemPromptFile)
	cmd = append(cmd, "--fork", agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	return claudecode.DeriveActivityState(event, payload)
}

func ResolveContinueBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, continueBinarySpec)
}

func (p *Plugin) continueBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveContinueBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func continueClaudeSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, continueClaudeSettingsDirName, continueClaudeSettingsFileName)
}

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits:
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--auto")
	}
}

func appendSystemPromptRule(cmd *[]string, inline, file string) {
	if inline != "" {
		*cmd = append(*cmd, "--rule", inline)
		return
	}
	if file != "" {
		*cmd = append(*cmd, "--rule", file)
	}
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
