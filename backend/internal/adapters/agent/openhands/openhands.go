package openhands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	adapterID = "openhands"

	modelEnvVar = "LLM_MODEL"
)

type Plugin struct {
	agentbase.Base

	binaryMu       sync.Mutex
	resolvedBinary string

	versionMu      sync.Mutex
	verifiedBinary string
}

func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)
var _ adapters.Adapter = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:           adapterID,
		Name:         "OpenHands",
		Description:  "Run OpenHands CLI worker sessions.",
		Version:      "0.0.1",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override (LiteLLM name, e.g. anthropic/claude-sonnet-4-5) applied over the saved OpenHands settings via LLM_MODEL.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	cmd, err := p.command(ctx, cfg.Permissions, cfg.Config, cfg.AllowedTools, cfg.DisallowedTools, cfg.Env)
	if err != nil {
		return nil, err
	}
	appendTask(&cmd, cfg.Prompt)
	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if id == "" {
		return nil, false, nil
	}
	cmd, err := p.command(ctx, cfg.Permissions, cfg.Config, cfg.AllowedTools, cfg.DisallowedTools, cfg.Env)
	if err != nil {
		return nil, false, err
	}
	cmd = append(cmd, "--resume", id)
	appendTask(&cmd, cfg.Prompt)
	return cmd, true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(ref)
	return info, ok, nil
}

func (p *Plugin) command(ctx context.Context, mode ports.PermissionMode, cfg ports.AgentConfig, allow, deny []string, env map[string]string) ([]string, error) {
	if len(allow) != 0 || len(deny) != 0 {
		return nil, fmt.Errorf("openhands: tool restrictions are not supported by this adapter")
	}
	bin, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.verifyVersion(ctx, bin); err != nil {
		return nil, err
	}
	var cmd []string
	if model := strings.TrimSpace(cfg.Model); model != "" {
		cmd = append(cmd, "env", modelEnvVar+"="+model, bin, "--override-with-envs")
	} else if sessionEnv(env, "LLM_API_KEY") != "" {
		cmd = append(cmd, bin, "--override-with-envs")
	} else {
		cmd = append(cmd, bin)
	}
	if flag := approvalFlag(mode); flag != "" {
		cmd = append(cmd, flag)
	}
	return cmd, nil
}

func sessionEnv(env map[string]string, name string) string {
	if value, ok := env[name]; ok {
		return value
	}
	return os.Getenv(name)
}

func approvalFlag(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAuto:
		return "--llm-approve"
	case ports.PermissionModeBypassPermissions:
		return "--always-approve"
	default:
		return ""
	}
}

func appendTask(cmd *[]string, prompt string) {
	if prompt != "" {
		*cmd = append(*cmd, "--task="+prompt)
	}
}

var binarySpec = binaryutil.BinarySpec{
	Label:         "openhands",
	Names:         []string{"openhands"},
	WinNames:      []string{"openhands.exe"},
	UnixPaths:     []string{"/usr/local/bin/openhands", "/opt/homebrew/bin/openhands"},
	UnixHomePaths: [][]string{{".local", "bin", "openhands"}},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "openhands.exe"}},
	},
}

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	bin, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = bin
	return bin, nil
}

func (p *Plugin) ResolveBinaryPresence(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binarySpec)
}

func (p *Plugin) verifyVersion(ctx context.Context, bin string) error {
	p.versionMu.Lock()
	defer p.versionMu.Unlock()
	if p.verifiedBinary == bin {
		return nil
	}
	if err := checkVersion(ctx, bin); err != nil {
		return err
	}
	p.verifiedBinary = bin
	return nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
