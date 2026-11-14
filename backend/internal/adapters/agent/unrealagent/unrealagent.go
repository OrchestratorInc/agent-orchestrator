package unrealagent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "unreal-agent"

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	executable     func() (string, error)
}

func New() *Plugin { return &Plugin{executable: os.Executable} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)

func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID: adapterID, Name: "Unreal Agent",
		Description:  "Built-in async-first Unreal Agent harness.",
		Version:      "0.1.1",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model passed to the built-in Unreal Agent harness.")
}

func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("unreal agent is available in chat mode only")
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
	resolve := p.executable
	if resolve == nil {
		resolve = os.Executable
	}
	binary, err := resolve()
	if err != nil {
		return "", err
	}
	p.resolvedBinary = strings.TrimSpace(binary)
	return p.resolvedBinary, nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
