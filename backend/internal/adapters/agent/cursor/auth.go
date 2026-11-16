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
	unconfirmed := false
	noteUnconfirmed := func(out []byte) (ports.AgentAuthStatus, bool) {
		unconfirmed = cursorAccountUnconfirmed(out)
		return ports.AgentAuthStatusUnknown, false // the shared classifier decides
	}
	status, steps, err := authprobe.CLIStatusWithDiagnostics(ctx, binary, [][]string{{"status"}}, 3*time.Second, noteUnconfirmed)
	if err != nil && ctx.Err() != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, err
	}
	if err == nil && status == ports.AgentAuthStatusAuthorized && unconfirmed {
		// The lookup failure means either a rejected login or an unreachable
		// Cursor. `models` sends the login to Cursor, so its answer tells them
		// apart: a rejection signs Cursor out (the env key may still apply);
		// anything else keeps the login with the caveat.
		verified, verifySteps, verifyErr := authprobe.CLIStatusWithDiagnostics(ctx, binary, [][]string{{"models"}}, cursorLoginVerifyTimeout, cursorLoginRejection)
		steps = append(steps, verifySteps...)
		if verifyErr != nil && ctx.Err() != nil {
			return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, verifyErr
		}
		if verified == ports.AgentAuthStatusUnauthorized {
			status = ports.AgentAuthStatusUnauthorized
		} else {
			steps = append(steps, domain.NewAgentAuthDiagnostic("cursor-agent status", "account_unconfirmed"))
		}
	}
	if err == nil && status == ports.AgentAuthStatusAuthorized {
		return status, cursorLoginAccount(ctx, cursorConfigPath()), steps, nil
	}
	if name := authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, "CURSOR_API_KEY"); name != "" {
		return ports.AgentAuthStatusAuthorized, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey), steps, nil
	}
	return status, domain.AgentAccountIdentity{}, steps, err
}

// cursorAccountUnconfirmed reports `cursor-agent status` output for a saved
// login whose account lookup failed. The CLI still exits 0 and says "Logged
// in", whether Cursor rejected the login or could not be reached, so the login
// is verified with `models` before it counts as a sign-out.
func cursorAccountUnconfirmed(out []byte) bool {
	return strings.Contains(strings.ToLower(string(out)), "unable to fetch user details")
}

// cursorLoginVerifyTimeout bounds the `models` call that verifies an
// unconfirmed login. It fits inside the readiness auth budget after `status`.
const cursorLoginVerifyTimeout = 6 * time.Second

// cursorLoginRejection classifies `cursor-agent models` output while
// verifying a login: only a clear sign-in answer ("Authentication required")
// counts. A model list or any other failure is inconclusive, never a login.
func cursorLoginRejection(out []byte) (ports.AgentAuthStatus, bool) {
	if authprobe.StatusFromText(string(out)) == ports.AgentAuthStatusUnauthorized {
		return ports.AgentAuthStatusUnauthorized, true
	}
	return ports.AgentAuthStatusUnknown, true
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
