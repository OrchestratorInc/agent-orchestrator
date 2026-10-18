package cursor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentAuthChecker            = (*Plugin)(nil)
	_ ports.AgentAuthDiagnosticsChecker = (*Plugin)(nil)
)

// AuthStatus returns the plugin's local authentication status.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

// AuthStatusWithDiagnostics also names the account: an API key, or the login
// Cursor records in its CLI config once `cursor-agent status` confirms it. It
// retains the evidence from the same auth check.
func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	// Native login decides first; the environment key is the fallback.
	status, steps, err := authprobe.CLIStatusWithDiagnostics(ctx, binary, [][]string{{"status"}}, 3*time.Second, nil)
	if err != nil && ctx.Err() != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, err
	}
	if err == nil && status == ports.AgentAuthStatusAuthorized {
		return status, cursorLoginAccount(ctx, cursorConfigPath()), steps, nil
	}
	if name := authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, "CURSOR_API_KEY"); name != "" {
		return ports.AgentAuthStatusAuthorized, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey), steps, nil
	}
	return status, domain.AgentAccountIdentity{}, steps, err
}

// cursorConfigPath is where the Cursor CLI records its login; tests point it
// at a temporary file.
var cursorConfigPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".cursor", "cli-config.json")
}

// cursorLoginAccount reads the display identity from the CLI config's
// authInfo. Ids are never read: they are provider identity, not display.
func cursorLoginAccount(ctx context.Context, path string) domain.AgentAccountIdentity {
	if path == "" || ctx.Err() != nil {
		return domain.AgentAccountIdentity{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.AgentAccountIdentity{}
	}
	var config struct {
		AuthInfo struct {
			Email       string `json:"email"`
			DisplayName string `json:"displayName"`
		} `json:"authInfo"`
	}
	if json.Unmarshal(data, &config) != nil {
		return domain.AgentAccountIdentity{}
	}
	identity := domain.AgentAccountIdentity{Email: config.AuthInfo.Email, Name: config.AuthInfo.DisplayName}
	if identity != (domain.AgentAccountIdentity{}) {
		identity.Method = domain.AgentAccountMethodAccount
	}
	return identity
}
