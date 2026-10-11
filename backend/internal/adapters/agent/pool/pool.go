// Package pool supplies Chat-only metadata for user-installed Poolside pool.
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

// Plugin is metadata only. Pool has no admitted AO terminal surface.
type Plugin struct{ agentbase.Base }

// New creates metadata for a user-installed Pool executable.
func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)
var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)

// Manifest describes the user-installed Chat-only Pool agent.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "pool", Name: "Poolside Pool", Description: "Native Poolside Pool ACP Chat (Unix).", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// ResolveBinary discovers the installed executable without downloading it.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	// Discovery does not establish binary identity or version compatibility.
	return binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "Poolside pool", Names: []string{"pool"}, WinNames: []string{"pool.exe"}})
}

// GetLaunchCommand refuses unqualified terminal launches.
func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("Pool is available in Chat mode only")
}

// GetRestoreCommand hard-refuses restore so missing history cannot launch fresh.
func (*Plugin) GetRestoreCommand(ctx context.Context, _ ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, errors.New("Pool terminal restore is unsupported; use Chat")
}

// AuthStatus reports credential presence without claiming remote authorization.
func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	return credentialStatus(os.Getenv("POOLSIDE_API_KEY")), nil
}

// AuthStatusWithEnv respects explicit session credential overrides.
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

// GetConfigSpec exposes provider-owned model selection.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model must exist in the native Pool provider catalog.")
}
