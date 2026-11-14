package kimchi

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "kimchi"

const systemPromptMaxBytes = 128 * 1024

func readBoundedSystemPrompt(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("kimchi: read system prompt file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("kimchi: system prompt file %s is not a regular file (mode %s)", path, info.Mode())
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	f, err := os.Open(path) //nolint:gosec // path is AO-owned config
	if err != nil {
		return "", fmt.Errorf("kimchi: read system prompt file: %w", err)
	}
	defer func() { _ = f.Close() }()
	openedInfo, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("kimchi: inspect system prompt file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() {
		return "", fmt.Errorf("kimchi: system prompt file %s is not a regular file (mode %s)", path, openedInfo.Mode())
	}

	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: f}, systemPromptMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("kimchi: read system prompt file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > systemPromptMaxBytes {
		return "", fmt.Errorf("kimchi: system prompt file %s exceeds %d byte limit", path, systemPromptMaxBytes)
	}
	return string(data), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.SubmitActivitySignaler = (*Plugin)(nil)
var _ ports.BlockedActivitySignaler = (*Plugin)(nil)

func (p *Plugin) EmitsSubmitActivity() bool { return true }

func (p *Plugin) EmitsBlockedActivity() bool { return true }

var permissionConfigEnum = []string{
	string(ports.PermissionModeDefault),
	string(ports.PermissionModeAcceptEdits),
	string(ports.PermissionModeAuto),
	string(ports.PermissionModeBypassPermissions),
}

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Kimchi",
		Description: "Run Kimchi worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:         "model",
				Type:        ports.ConfigFieldString,
				Description: "Model override passed to `kimchi --model`.",
			},
			{
				Key:         "permissions",
				Type:        ports.ConfigFieldEnum,
				Description: "Starting permission mode.",
				Enum:        permissionConfigEnum,
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.kimchiBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}

	if model := strings.TrimSpace(cfg.Config.Model); model != "" {
		cmd = append(cmd, "--model", model)
	}

	permissions := cfg.Permissions
	if permissions == "" {
		permissions = cfg.Config.Permissions
	}
	appendPermissionFlags(&cmd, permissions)
	if err := appendToolFlags(&cmd, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, err
	}

	if cfg.SystemPromptFile != "" {
		data, err := readBoundedSystemPrompt(ctx, cfg.SystemPromptFile)
		if err != nil {
			return nil, err
		}
		cmd = append(cmd, "--append-system-prompt", data)
	} else if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", cfg.SystemPrompt)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, sanitizePrompt(cfg.Prompt))
	}
	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.kimchiBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if effort := strings.TrimSpace(cfg.Config.Effort); effort != "" {
		cmd = append(cmd, "--thinking", effort)
	}
	appendPermissionFlags(&cmd, cfg.Permissions)
	if err := appendToolFlags(&cmd, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, false, err
	}

	systemPrompt, err := resolveRestoreSystemPrompt(ctx, cfg)
	if err != nil {
		return nil, false, err
	}
	if systemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", systemPrompt)
	}

	cmd = append(cmd, "--session", agentSessionID)
	if cfg.Prompt != "" {
		cmd = append(cmd, sanitizePrompt(cfg.Prompt))
	}
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func appendToolFlags(cmd *[]string, allowed, disallowed []string) error {
	for _, rule := range allowed {
		if strings.Contains(rule, ",") {
			return fmt.Errorf("kimchi: allowed tool rule %q contains a comma; tool rules are comma-joined into a single flag value so a literal comma would silently split the rule", rule)
		}
	}
	for _, rule := range disallowed {
		if strings.Contains(rule, ",") {
			return fmt.Errorf("kimchi: disallowed tool rule %q contains a comma; tool rules are comma-joined into a single flag value so a literal comma would silently split the rule", rule)
		}
	}
	if len(allowed) > 0 {
		*cmd = append(*cmd, "--allow-tool", strings.Join(allowed, ","))
	}
	if len(disallowed) > 0 {
		*cmd = append(*cmd, "--deny-tool", strings.Join(disallowed, ","))
	}
	return nil
}

func appendPermissionFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch permissions {
	case ports.PermissionModeDefault, "":
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--auto")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--yolo")
	}
}

func sanitizePrompt(prompt string) string {
	if prompt == "" {
		return prompt
	}
	if strings.HasPrefix(prompt, "-") || strings.HasPrefix(prompt, "@") {
		return "\n" + prompt
	}
	return prompt
}

func resolveRestoreSystemPrompt(ctx context.Context, cfg ports.RestoreConfig) (string, error) {
	if cfg.SystemPromptFile != "" {
		return readBoundedSystemPrompt(ctx, cfg.SystemPromptFile)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return cfg.SystemPrompt, nil
}

var kimchiBinarySpec = binaryutil.BinarySpec{
	Label:         "kimchi",
	Names:         []string{"kimchi"},
	WinNames:      []string{"kimchi.cmd", "kimchi.exe", "kimchi"},
	UnixPaths:     []string{"/usr/local/bin/kimchi", "/opt/homebrew/bin/kimchi"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("kimchi"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kimchi.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kimchi.exe"}},
	},
}

func ResolveKimchiBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, kimchiBinarySpec)
}

func (p *Plugin) kimchiBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveKimchiBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
