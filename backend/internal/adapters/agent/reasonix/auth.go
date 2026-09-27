package reasonix

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AuthStatus reports configured credentials without asserting a successful
// provider login. A missing key may be valid for a local anonymous provider.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		return ports.AgentAuthStatusUnknown, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := p.runProbe(ctx, binary, "doctor", "--json")
	if err != nil {
		return ports.AgentAuthStatusUnknown, probeError(ctx, "authentication")
	}
	var report struct {
		Providers []struct {
			IsDefault  bool `json:"is_default"`
			KeyPresent bool `json:"key_present"`
		} `json:"providers"`
	}
	if json.Unmarshal(output, &report) != nil {
		return ports.AgentAuthStatusUnknown, errors.New("reasonix: unrecognized doctor output")
	}
	for _, provider := range report.Providers {
		if provider.IsDefault && provider.KeyPresent {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}
