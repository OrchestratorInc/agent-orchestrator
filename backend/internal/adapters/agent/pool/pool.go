// Package pool supplies unregistered metadata for user-installed Poolside pool.
package pool

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin is metadata only. Native ACP admission awaits independent qualification.
type Plugin struct{ agentbase.Base }

func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)
var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)

func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "pool", Name: "Poolside Pool", Description: "User-installed Poolside pool; ACP preparation only.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	// Discovery does not establish binary identity or version compatibility.
	return binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "Poolside pool", Names: []string{"pool"}, WinNames: []string{"pool.exe"}})
}

func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("pool terminal interface is not qualified for AO")
}

func (*Plugin) GetRestoreCommand(ctx context.Context, _ ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, errors.New("pool terminal restore is not qualified for AO")
}

func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return credentialStatus(os.Getenv("POOLSIDE_API_KEY")), nil
}

func (*Plugin) AuthStatusWithEnv(ctx context.Context, env map[string]string) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	key, present := env["POOLSIDE_API_KEY"]
	if !present {
		key = os.Getenv("POOLSIDE_API_KEY")
	}
	return credentialStatus(key), nil
}

func credentialStatus(key string) ports.AgentAuthStatus {
	if strings.TrimSpace(key) != "" {
		return ports.AgentAuthStatusConfigured
	}
	// Saved native login is not inspected: absent environment key is inconclusive.
	return ports.AgentAuthStatusUnknown
}
