package goose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	adapterID = "goose"

	gooseModeEnvVar = "GOOSE_MODE"

	gooseIdentityWaitDelay = 100 * time.Millisecond
)

var gooseIdentityProbeTimeout = time.Minute

var gooseIdentityCommand = runGooseIdentityCommand

func runGooseIdentityCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, args...) //nolint:gosec // binary is resolved by binaryutil; args are static
	cmd.WaitDelay = gooseIdentityWaitDelay
	return cmd.CombinedOutput()
}

type Plugin struct {
	agentbase.Base
	binaryMu           sync.Mutex
	resolvedBinary     string
	resolvedBinaryInfo os.FileInfo
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Goose",
		Description: "Run Goose worker sessions.",
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
				Description: "Model override passed to `goose run --model`.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.gooseBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = append(gooseModeEnvPrefix(cfg.Permissions), binary, "run")

	systemPrompt, err := systemPromptText(cfg)
	if err != nil {
		return nil, err
	}
	if systemPrompt != "" {
		cmd = append(cmd, "--system", systemPrompt)
	}
	appendModelFlag(&cmd, cfg.Config)

	cmd = append(cmd, "-t", "", "--interactive")

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.gooseBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = append(gooseModeEnvPrefix(cfg.Permissions), binary, "session")
	systemPrompt, err := restoreSystemPromptText(cfg)
	if err != nil {
		return nil, false, err
	}
	if systemPrompt != "" {
		cmd = append(cmd, "--system", systemPrompt)
	}
	appendModelFlag(&cmd, cfg.Config)
	cmd = append(cmd, "--resume", "--session-id", agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func systemPromptText(cfg ports.LaunchConfig) (string, error) {
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
		return "", fmt.Errorf("read %s: %w", file, err)
	}
	if text := strings.TrimSpace(string(data)); text != "" {
		return text, nil
	}
	return "", nil
}

func appendModelFlag(cmd *[]string, cfg ports.AgentConfig) {
	if model := strings.TrimSpace(cfg.Model); model != "" {
		*cmd = append(*cmd, "--model", model)
	}
}

func gooseModeEnvPrefix(mode ports.PermissionMode) []string {
	value := gooseMode(mode)
	if value == "" {
		return nil
	}
	return []string{"env", gooseModeEnvVar + "=" + value}
}

func gooseMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return "smart_approve"
	case ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeBypassPermissions:
		return "auto"
	default:
		return ""
	}
}

var gooseBinarySpec = binaryutil.BinarySpec{
	Label:         "goose",
	Names:         []string{"goose"},
	WinNames:      []string{"goose.exe"},
	UnixPaths:     []string{"/usr/local/bin/goose", "/opt/homebrew/bin/goose"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("goose", []string{".cargo", "bin", "goose"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "goose.exe"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"Programs", "goose", "goose.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".cargo", "bin", "goose.exe"}},
	},
	ValidateIdentity: isOfficialGooseBinary,
}

func ResolveGooseBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, gooseBinarySpec)
}

func isOfficialGooseBinary(ctx context.Context, binary string) bool {
	if err := ctx.Err(); err != nil || binary == "" {
		return false
	}
	if runtime.GOOS == "windows" && !isNativelyLaunchableWindowsGoose(binary) {
		return false
	}

	probeCtx, cancel := context.WithTimeout(ctx, gooseIdentityProbeTimeout)
	defer cancel()
	out, err := gooseIdentityCommand(probeCtx, binary, "--help")
	if probeCtx.Err() != nil || err != nil {
		return false
	}
	return hasGooseHelpCommand(out, "session") && hasGooseHelpCommand(out, "recipe")
}

func hasGooseHelpCommand(output []byte, command string) bool {
	command = strings.ToLower(command)
	for _, line := range strings.Split(strings.ToLower(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		token := strings.Trim(fields[0], "`'\"")
		if strings.HasPrefix(token, "-") {
			continue
		}
		token = strings.TrimSuffix(token, ":")
		if token == command {
			return true
		}
	}
	return false
}

func isNativelyLaunchableWindowsGoose(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".exe")
}

func (p *Plugin) gooseBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if cached := p.cachedGooseBinary(); cached != "" {
		return cached, nil
	}

	binary, err := ResolveGooseBinary(ctx)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	p.cacheGooseBinary(binary)
	return binary, nil
}
