package devin

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus returns the plugin's local authentication status.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if status, ok, err := devinLocalAuthStatus(ctx, binary); err != nil {
		return ports.AgentAuthStatusUnknown, err
	} else if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func devinLocalAuthStatus(ctx context.Context, binary string) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	// Devin's own stored login, as `devin auth status` reports it, decides
	// first; DEVIN_API_KEY in the environment is the fallback.
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, _ := authprobe.CmdRunner(probeCtx, binary, "auth", "status")
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	status := ports.AgentAuthStatusUnknown
	if probeCtx.Err() == nil {
		status = authprobe.StatusFromText(string(output))
	}
	if status == ports.AgentAuthStatusAuthorized {
		identity := devinAccountFromStatus(string(output))
		identity.SourceKind = domain.AgentCredentialSourceCommand
		identity.Source = domain.CommandSource(binary, "auth", "status").Source
		ports.RecordAuthEvidence(ctx, identity)
		return status, true, nil
	}
	if key := strings.TrimSpace(os.Getenv("DEVIN_API_KEY")); strings.HasPrefix(key, "cog_") && len(key) > len("cog_") {
		identity := domain.EnvironmentSource("DEVIN_API_KEY").WithMethod(domain.AgentAccountMethodAPIKey)
		identity.Provider = "Devin"
		ports.RecordAuthEvidence(ctx, identity)
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	if status != ports.AgentAuthStatusUnknown {
		return status, true, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

// devinAccountFromStatus reads only display fields from a successful native
// status report. Credential values, file paths and account IDs are not copied.
func devinAccountFromStatus(output string) domain.AgentAccountIdentity {
	identity := domain.AgentAccountIdentity{Method: domain.AgentAccountMethodAccount}
	section, tier := "", ""
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		switch line {
		case "Logged in (via Devin).":
			identity.Provider = "Devin"
		case "Logged in (via Windsurf).":
			identity.Provider = "Windsurf"
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if value == "" && rawLine == strings.TrimLeft(rawLine, " \t") {
			section = key
			continue
		}
		switch section {
		case "User":
			switch key {
			case "Name":
				identity.Name = value
			case "Email":
				identity.Email = value
			}
		case "Account":
			switch key {
			case "Plan":
				identity.Plan = value
			case "Tier":
				tier = value
			}
		}
	}
	if identity.Plan == "" {
		identity.Plan = tier
	}
	return identity
}
