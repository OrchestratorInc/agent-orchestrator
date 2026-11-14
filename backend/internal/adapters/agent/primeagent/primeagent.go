package primeagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "prime-agent"

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	runCommand     primeCommandRunner
	extensionMu    sync.Mutex
	extensionFlag  map[string]bool
}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

func (p *Plugin) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	dir, err := primeDataDir(dataDir)
	if err != nil {
		return
	}
	env[primeAgentCodingAgentDirEnv] = dir
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.SemanticMessageAcceptanceSignaler = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentNativeSessionTerminator = (*Plugin)(nil)
var _ ports.ActiveTurnSteerer = (*Plugin)(nil)
var _ ports.SubmitActivitySignaler = (*Plugin)(nil)
var _ ports.BlockedActivitySignaler = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Prime Agent",
		Description: "Run Prime Agent worker sessions.",
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
	return ports.ConfigSpec{Fields: []ports.ConfigField{{
		Key:         "model",
		Type:        ports.ConfigFieldString,
		Description: "Model override passed to `prime-agent --model`.",
	}}}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd, err := p.baseCommand(ctx, cfg.DataDir, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Config)
	if err != nil {
		return nil, err
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd, nil
}

func (p *Plugin) baseCommand(ctx context.Context, dataDir, systemPrompt, systemPromptFile string, cfg ports.AgentConfig) ([]string, error) {
	binary, err := p.primeAgentBinary(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := sessionDir(dataDir)
	if err != nil {
		return nil, err
	}
	extensionPath, err := extensionPath(dataDir)
	if err != nil {
		return nil, err
	}

	cmd := []string{binary, "--session-dir", sessions}
	if p.acceptsExtension(ctx, binary) {
		cmd = append(cmd, "--extension", extensionPath)
	}
	resolvedPrompt, err := resolveSystemPrompt(ctx, systemPrompt, systemPromptFile)
	if err != nil {
		return nil, err
	}
	if resolvedPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", resolvedPrompt)
	}
	agentbase.AppendModelFlag(&cmd, cfg, "--model")
	if effort := strings.TrimSpace(cfg.Effort); effort != "" {
		cmd = append(cmd, "--thinking", effort)
	}
	return cmd, nil
}

func resolveSystemPrompt(ctx context.Context, inline, file string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(inline) != "" {
		return inline, nil
	}
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(file) //nolint:gosec // path comes from AO-owned launch configuration
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if err != nil {
		return "", fmt.Errorf("prime-agent: read system prompt file: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", nil
	}
	return string(data), nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}
	cmd, err := p.baseCommand(ctx, cfg.DataDir, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Config)
	if err != nil {
		return nil, false, err
	}
	return append(cmd, "--resume", agentSessionID), true, nil
}

func (p *Plugin) SteersActiveTurn() bool { return true }

func (p *Plugin) EmitsSubmitActivity() bool { return true }

func (p *Plugin) EmitsBlockedActivity() bool { return false }

var primeAgentBinarySpec = binaryutil.BinarySpec{
	Label:         "prime-agent",
	Names:         []string{"prime-agent"},
	WinNames:      []string{"prime-agent.cmd", "prime-agent.exe", "prime-agent"},
	UnixPaths:     []string{"/usr/local/bin/prime-agent", "/opt/homebrew/bin/prime-agent"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("prime-agent"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "prime-agent.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "prime-agent.exe"}},
	},
}

func ResolvePrimeAgentBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, primeAgentBinarySpec)
}

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.primeAgentBinary(ctx)
}

func (p *Plugin) primeAgentBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := ResolvePrimeAgentBinary(ctx)
	if err != nil {
		return "", err
	}
	if binary == "" {
		return "", errors.New("prime-agent: resolved an empty binary path")
	}
	p.resolvedBinary = binary
	return binary, nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
	p.extensionMu.Lock()
	p.extensionFlag = nil
	p.extensionMu.Unlock()
}

func (p *Plugin) acceptsExtension(ctx context.Context, binary string) bool {
	p.extensionMu.Lock()
	defer p.extensionMu.Unlock()
	if accepts, ok := p.extensionFlag[binary]; ok {
		return accepts
	}
	runner := p.runCommand
	if runner == nil {
		runner = runPrimeCommand
	}
	runCtx, cancel := context.WithTimeout(ctx, primeCommandTimeout)
	defer cancel()
	out, err := runner(runCtx, binary, "", nil, "--help")
	if err != nil && len(out) == 0 {
		return true
	}
	if p.extensionFlag == nil {
		p.extensionFlag = map[string]bool{}
	}
	p.extensionFlag[binary] = strings.Contains(string(out), "--extension")
	return p.extensionFlag[binary]
}
