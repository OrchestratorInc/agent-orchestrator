package cursor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
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
		// Cursor. `models` sends the saved login to Cursor, so its answer tells
		// them apart: a rejection signs Cursor out (the env key may still
		// apply); anything else keeps the login with the caveat.
		rejected, step, verifyErr := verifyCursorLogin(ctx, binary)
		if verifyErr != nil {
			return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, append(steps, step), verifyErr
		}
		if rejected {
			status = ports.AgentAuthStatusUnauthorized
			steps = append(steps, step)
			// `status` recorded itself as the deciding credential; Cursor
			// rejected that login, so it no longer decides anything.
			ports.RecordAuthEvidence(ctx, domain.AgentAccountIdentity{})
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
// unconfirmed login. With the 3s `status` probe it stays inside the Cursor
// readiness auth budget (cursorAuthCheckTimeout in the agent service).
const cursorLoginVerifyTimeout = 6 * time.Second

// cursorLoginCredentialEnv are the variables that authorize the Cursor CLI
// without the saved login. The verification drops them, so it tests the
// saved login alone; a configured key is judged separately afterwards.
var cursorLoginCredentialEnv = []string{"CURSOR_API_KEY", "CURSOR_AUTH_TOKEN"}

// cursorModelsRunner runs `cursor-agent models` with env; tests replace it.
var cursorModelsRunner = func(ctx context.Context, binary string, env []string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, "models")
	cmd.Env = env
	return cmd.CombinedOutput()
}

// verifyCursorLogin reports whether Cursor rejected the saved login. Only a
// clear sign-in answer ("Authentication required") is a rejection; a model
// list, a timeout or any other failure is inconclusive. It returns an error
// only when the caller's context ended.
func verifyCursorLogin(ctx context.Context, binary string) (bool, domain.AgentAuthDiagnostic, error) {
	probeCtx, cancel := context.WithTimeout(ctx, cursorLoginVerifyTimeout)
	defer cancel()
	started := time.Now()
	out, runErr := cursorModelsRunner(probeCtx, binary, withoutEnv(agentlaunch.ProcessEnvForLaunchBinary(probeCtx, binary), cursorLoginCredentialEnv))
	status := ports.AgentAuthStatusUnknown
	if probeCtx.Err() == nil && authprobe.StatusFromText(string(out)) == ports.AgentAuthStatusUnauthorized {
		status = ports.AgentAuthStatusUnauthorized
	}
	step := authprobe.CommandStep(binary, []string{"models"}, status, runErr, probeCtx.Err(), started)
	if err := ctx.Err(); err != nil {
		return false, step, err
	}
	return status == ports.AgentAuthStatusUnauthorized, step, nil
}

// withoutEnv returns env minus the named variables.
func withoutEnv(env []string, names []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(names, name) {
			kept = append(kept, entry)
		}
	}
	return kept
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
