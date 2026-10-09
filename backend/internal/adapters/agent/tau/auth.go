package tau

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Provider describes one row of the released `tau providers` TSV contract.
// It intentionally excludes endpoint and credential identifiers from AO output.
type Provider struct {
	Name         string
	Models       []string
	DefaultModel string
	Default      bool
	Configured   bool
}

// ParseProviders reads local provider metadata without retaining secrets.
func ParseProviders(output []byte) []Provider {
	var providers []Provider
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(fields) != 11 || strings.TrimSpace(fields[1]) == "" {
			continue
		}
		provider := Provider{Name: fields[1], Default: fields[0] == "*", DefaultModel: fields[3], Configured: strings.HasPrefix(fields[6], "stored:") || strings.HasPrefix(fields[6], "env:")}
		for _, id := range strings.Split(fields[4], ",") {
			if id = strings.TrimSpace(id); id != "" {
				provider.Models = append(provider.Models, id)
			}
		}
		providers = append(providers, provider)
	}
	return providers
}

// AuthStatus is configured-only evidence. Tau's provider listing never validates
// credentials with a provider, and a configured non-default provider cannot
// establish that an unqualified launch is ready.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	output, err := p.probe(ctx, binary, "providers")
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	for _, provider := range ParseProviders(output) {
		if provider.Default && provider.Configured {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}
