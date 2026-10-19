package minimaxcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	dataDir, err := sourceProfile(nil)
	if err != nil {
		dataDir = ""
	}
	return providerAuthStatus(out, dataDir), nil
}

// providerAuthStatus reads the active provider from `mcode provider list
// --json`. That list always reports the MiniMax OAuth row without a key, so
// a /login account is read from MiniMax's OAuth state under dataDir instead.
func providerAuthStatus(data []byte, dataDir string) ports.AgentAuthStatus {
	var snapshot struct {
		Providers []struct {
			Kind      string `json:"kind"`
			Active    bool   `json:"active"`
			Enabled   bool   `json:"enabled"`
			HasAPIKey bool   `json:"hasApiKey"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &snapshot) != nil {
		return ports.AgentAuthStatusUnknown
	}
	for _, p := range snapshot.Providers {
		if !p.Active || !p.Enabled {
			continue
		}
		switch p.Kind {
		case "minimax-oauth":
			return oauthAuthStatus(dataDir)
		case "minimax-api-key":
			if p.HasAPIKey {
				return ports.AgentAuthStatusConfigured
			}
			return ports.AgentAuthStatusUnauthorized
		}
		// Custom providers may read their key from an environment variable
		// the list does not report, so a missing key stays unknown.
		if p.HasAPIKey {
			return ports.AgentAuthStatusConfigured
		}
		return ports.AgentAuthStatusUnknown
	}
	return ports.AgentAuthStatusUnknown
}

// oauthAuthStatus reads MiniMax's non-secret OAuth state files:
// <data>/auth/<buildEnv>/<region>/mcode-public/auth-state.json, plus the
// legacy <data>/auth/auth-state.json. Credentials themselves are never read.
func oauthAuthStatus(dataDir string) ports.AgentAuthStatus {
	if dataDir == "" {
		return ports.AgentAuthStatusUnknown
	}
	authHome := filepath.Join(dataDir, "auth")
	paths, err := filepath.Glob(filepath.Join(authHome, "*", "*", "mcode-public", "auth-state.json"))
	if err != nil {
		return ports.AgentAuthStatusUnknown
	}
	paths = append(paths, filepath.Join(authHome, "auth-state.json"))
	status := ports.AgentAuthStatusUnauthorized
	for _, path := range paths {
		switch readOAuthState(path) {
		case "authenticated", "refreshing", "scope_upgrade_required":
			return ports.AgentAuthStatusConfigured
		case "", "anonymous", "expired", "logging_out", "logout_pending":
		default:
			// authorizing or error: a login may be in flight or need repair.
			status = ports.AgentAuthStatusUnknown
		}
	}
	return status
}

// readOAuthState returns the state file's status, "" when it is absent, and
// "unreadable" when it exists but cannot be read as bounded JSON.
func readOAuthState(path string) string {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "unreadable"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "unreadable"
	}
	var state struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &state) != nil || state.Status == "" {
		return "unreadable"
	}
	return state.Status
}
