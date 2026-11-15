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
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

// AuthStatusWithDiagnostics reports which check produced the native verdict.
func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	// Native login decides first; the environment key is the fallback.
	var identity domain.AgentAccountIdentity
	status, steps, err := authprobe.CLIStatusWithDiagnostics(ctx, binary, [][]string{{"auth", "status"}}, 8*time.Second, func(output []byte) (ports.AgentAuthStatus, bool) {
		status := authprobe.StatusFromText(string(output))
		if status == ports.AgentAuthStatusAuthorized {
			identity = devinAccountFromStatus(string(output))
		}
		return status, true
	})
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, err
	}
	if status == ports.AgentAuthStatusAuthorized {
		identity.SourceKind = domain.AgentCredentialSourceCommand
		identity.Source = domain.CommandSource(binary, "auth", "status").Source
		ports.RecordAuthEvidence(ctx, identity)
		return status, identity, steps, nil
	}
	if key := strings.TrimSpace(os.Getenv("DEVIN_API_KEY")); strings.HasPrefix(key, "cog_") && len(key) > len("cog_") {
		identity = domain.EnvironmentSource("DEVIN_API_KEY").WithMethod(domain.AgentAccountMethodAPIKey)
		identity.Provider = "Devin"
		ports.RecordAuthEvidence(ctx, identity)
		return ports.AgentAuthStatusAuthorized, identity, steps, nil
	}
	return status, domain.AgentAccountIdentity{}, steps, nil
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
