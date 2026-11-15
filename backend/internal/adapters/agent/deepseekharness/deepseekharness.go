package deepseekharness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "deepseek-harness"

const headlessProfile = "headless"

const stdioTaskLiteral = "-"

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

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "DeepSeek",
		Description: "Run DeepSeek Harness (dsh) worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx,
		"Model override for DeepSeek Harness Chat sessions, using the value the session advertises (for example [\"deepseek-official\",\"deepseek-v4-flash\"]). Terminal launches use the model route configured in the Harness profile.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := p.deepseekBinary(ctx)
	if err != nil {
		return nil, err
	}
	standing, err := standingInstructions(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, err
	}
	task, err := headlessTask(cfg.Prompt, standing)
	if err != nil {
		return nil, err
	}
	return resolveWindowsShimArgv([]string{binary, "--profile", headlessProfile, task}, exec.LookPath), nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.deepseekBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	standing, err := standingInstructions(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, false, err
	}
	task, err := headlessTask(cfg.Prompt, standing)
	if err != nil {
		return nil, false, err
	}
	return resolveWindowsShimArgv([]string{binary, "--profile", headlessProfile, "--session-id", agentSessionID, task}, exec.LookPath), true, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func standingInstructions(inline, file string) (string, error) {
	if strings.TrimSpace(inline) != "" {
		return strings.TrimSpace(inline), nil
	}
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", fmt.Errorf("%s: read system prompt file: %w", adapterID, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func headlessTask(prompt, standing string) (string, error) {
	switch strings.TrimSpace(prompt) {
	case "":
		return "", fmt.Errorf("%s: a terminal session needs a task; DeepSeek Harness reads stdin when the %s profile is launched without one", adapterID, headlessProfile)
	case stdioTaskLiteral:
		return "", fmt.Errorf("%s: refusing to pass %q as a task; DeepSeek Harness would read stdin instead of running it", adapterID, stdioTaskLiteral)
	}
	if strings.TrimSpace(standing) == "" {
		return prompt, nil
	}
	return "Standing instructions from Agent Orchestrator. Follow them for this whole session:\n<ao_standing_instructions>\n" +
		strings.TrimSpace(standing) + "\n</ao_standing_instructions>\n\nTask:\n" + prompt, nil
}

var deepseekBinarySpec = binaryutil.BinarySpec{
	Label:         "dsh",
	Names:         []string{"dsh"},
	WinNames:      []string{"dsh.cmd", "dsh.exe", "dsh"},
	UnixPaths:     []string{"/usr/local/bin/dsh", "/opt/homebrew/bin/dsh"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("dsh"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "dsh.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "dsh.exe"}},
	},
}

func ResolveDeepSeekBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, deepseekBinarySpec)
}

func (p *Plugin) deepseekBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveDeepSeekBinary(ctx)
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
