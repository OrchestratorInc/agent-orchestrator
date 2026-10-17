// Package dockeragent provides metadata for the user-installed Docker Agent.
// Only its native ACP Chat interface is supported by AO.
package dockeragent

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ConfigEnv selects an existing native YAML configuration. AO never rewrites it.
const ConfigEnv = "AO_DOCKER_AGENT_CONFIG"

// Plugin supplies installation discovery and configuration metadata for Chat.
type Plugin struct{ agentbase.Base }

// New returns the Docker Agent metadata plugin.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)

// Manifest describes the native standalone harness.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "docker-agent", Name: "Docker Agent", Description: "User-installed Docker Agent, native ACP Chat only.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes native configured model choices, not arbitrary model IDs.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Native ACP model option (default or model:<configured-name>); configuration selected with AO_DOCKER_AGENT_CONFIG.")
}

// GetLaunchCommand refuses terminal execution rather than fabricating a TUI.
func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("docker agent is available in Chat mode only")
}

// ResolveBinary selects the standalone binary; Docker Desktop is not required.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "Docker Agent", Names: []string{"docker-agent"}, WinNames: []string{"docker-agent.exe"}, UnixPaths: []string{"/usr/local/bin/docker-agent"}, UnixHomePaths: [][]string{{".local", "bin", "docker-agent"}}})
}

// AuthStatus cannot verify arbitrary native config/provider credentials. Docker
// Agent's ACP host-credentials handshake owns that decision; even that handshake
// checks availability, not remote authorization.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return ports.AgentAuthStatusUnknown, nil
}
