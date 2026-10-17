package minimaxcode

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var authProbeTimeout = 20 * time.Second
var runAuthProbe = func(ctx context.Context, binary string) ([]byte, error) {
	return aoprocess.CommandContext(ctx, binary, "provider", "list", "--json").Output()
}

// AuthStatus reports configured credentials only. A saved connectivity status
// may be stale; it does not authorize a later provider call.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	probe, cancel := context.WithTimeout(ctx, authProbeTimeout)
	defer cancel()
	out, err := runAuthProbe(probe, binary)
	if ctx.Err() != nil {
		return ports.AgentAuthStatusUnknown, ctx.Err()
	}
	if err != nil || probe.Err() != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	return providerAuthStatus(out), nil
}
func providerAuthStatus(data []byte) ports.AgentAuthStatus {
	var snapshot struct {
		Providers []struct {
			Active    bool `json:"active"`
			Enabled   bool `json:"enabled"`
			HasAPIKey bool `json:"hasApiKey"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &snapshot) != nil {
		return ports.AgentAuthStatusUnknown
	}
	for _, p := range snapshot.Providers {
		if p.Active && p.Enabled && p.HasAPIKey {
			return ports.AgentAuthStatusConfigured
		}
	}
	return ports.AgentAuthStatusUnknown
}
