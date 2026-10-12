package cursor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentAuthChecker        = (*Plugin)(nil)
	_ ports.AgentAuthAccountChecker = (*Plugin)(nil)
)

// AuthStatus returns the plugin's local authentication status.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, err := p.AuthStatusWithAccount(ctx)
	return status, err
}

// AuthStatusWithAccount also names the account: an API key, or the login
// Cursor records in its CLI config once `cursor-agent status` confirms it.
func (p *Plugin) AuthStatusWithAccount(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, err
	}
	if strings.TrimSpace(os.Getenv("CURSOR_API_KEY")) != "" {
		return ports.AgentAuthStatusAuthorized, domain.AgentAccountIdentity{Method: domain.AgentAccountMethodAPIKey}, nil
	}
	if status, err := cursorCLIAuthStatus(ctx, binary); err == nil && status != ports.AgentAuthStatusUnknown {
		var account domain.AgentAccountIdentity
		if status == ports.AgentAuthStatusAuthorized {
			account = cursorLoginAccount(cursorConfigPath())
		}
		return status, account, nil
	} else if err != nil && ctx.Err() != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, err
	}
	return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil
}

func cursorCLIAuthStatus(ctx context.Context, binary string) (ports.AgentAuthStatus, error) {
	return authprobe.CLIStatus(ctx, binary, [][]string{{"status"}})
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
func cursorLoginAccount(path string) domain.AgentAccountIdentity {
	if path == "" {
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
