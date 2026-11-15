package copilot

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

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus returns the plugin's local authentication status.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	_, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if status, ok, err := copilotLocalAuthStatus(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	} else if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

// gitHubCLITokenEnvVars take precedence over the GitHub CLI's stored
// credentials for github.com, in this order.
var gitHubCLITokenEnvVars = []string{"GH_TOKEN", "GITHUB_TOKEN"}

var copilotTokenEnvVars = []string{
	"COPILOT_GITHUB_TOKEN",
	"GH_TOKEN",
	"GITHUB_TOKEN",
}

// copilotLocalAuthStatus follows Copilot CLI's own credential order: token
// environment variables first (one silently overrides a stored login), then
// Copilot's stored login, then the GitHub CLI. A BYOK provider is the fallback.
func copilotLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	for _, name := range copilotTokenEnvVars {
		if copilotUsableToken(os.Getenv(name)) {
			ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodToken))
			return ports.AgentAuthStatusAuthorized, true, nil
		}
	}
	status, ok, err := copilotStoredAuthStatus(ctx)
	if err == nil && ok && status.HasCredential() {
		return status, true, nil
	}
	if copilotBYOKConfigured() {
		// A bring-your-own provider; its API key, if any, is optional.
		ports.RecordAuthEvidence(ctx, domain.EnvironmentSource("COPILOT_PROVIDER_BASE_URL"))
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return status, ok, err
}

func copilotStoredAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if home == "" {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	// Copilot credentials can come from several independent sources. A corrupt
	// config file must not prevent the documented GitHub CLI token fallback.
	configPath := filepath.Join(copilotHomeDir(home), "config.json")
	configStatus, configOK, _ := copilotConfigAuthStatus(configPath)
	if configOK {
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(configPath).WithMethod(domain.AgentAccountMethodAccount))
		return configStatus, true, nil
	}
	if status, ok, err := copilotGHAuthStatus(ctx); err != nil || ok {
		return status, ok, err
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

// copilotBYOKConfigured recognizes Copilot CLI's documented local BYOK setup.
// An API key is optional because providers such as local Ollama do not require
// one; the endpoint and model identify a usable provider configuration.
func copilotBYOKConfigured() bool {
	return strings.TrimSpace(os.Getenv("COPILOT_PROVIDER_BASE_URL")) != "" &&
		strings.TrimSpace(os.Getenv("COPILOT_MODEL")) != ""
}

func copilotHomeDir(home string) string {
	if path := strings.TrimSpace(os.Getenv("COPILOT_HOME")); path != "" {
		return path
	}
	return filepath.Join(home, ".copilot")
}

func copilotConfigAuthStatus(path string) (ports.AgentAuthStatus, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	for _, key := range []string{"authToken", "accessToken", "token"} {
		var token string
		if err := json.Unmarshal(config[key], &token); err == nil && copilotUsableToken(token) {
			return ports.AgentAuthStatusAuthorized, true, nil
		}
	}
	if copilotLoggedInUser(config) {
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

func copilotGHAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	out, err := authprobe.CmdRunner(probeCtx, "gh", "auth", "token")
	if probeCtx.Err() != nil {
		if probeCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return ports.AgentAuthStatusUnknown, false, nil
		}
		return ports.AgentAuthStatusUnknown, false, probeCtx.Err()
	}
	if err == nil && copilotUsableToken(string(out)) {
		// GH_TOKEN and GITHUB_TOKEN override the GitHub CLI's stored login, and
		// `gh auth token` then prints that token; hosts.yml's user is not its owner.
		for _, name := range gitHubCLITokenEnvVars {
			if copilotUsableToken(os.Getenv(name)) {
				ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodToken))
				return ports.AgentAuthStatusAuthorized, true, nil
			}
		}
		evidence := domain.CommandSource("gh", "auth", "token").WithMethod(domain.AgentAccountMethodAccount)
		evidence.Name = gitHubCLIUser(ctx)
		ports.RecordAuthEvidence(ctx, evidence)
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	// GitHub CLI state is only one Copilot credential source. A negative result
	// cannot rule out Copilot's own stored OAuth or token credentials.
	return ports.AgentAuthStatusUnknown, false, nil
}

// gitHubCLIUser returns the github.com login the GitHub CLI is signed in as,
// read from its hosts.yml (GH_CONFIG_DIR, then XDG_CONFIG_HOME, then
// ~/.config/gh). Tokens in the file are never read.
func gitHubCLIUser(ctx context.Context) string {
	if ctx.Err() != nil {
		return ""
	}
	dir := strings.TrimSpace(os.Getenv("GH_CONFIG_DIR"))
	if dir == "" {
		if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
			dir = filepath.Join(xdg, "gh")
		} else if home, err := os.UserHomeDir(); err == nil && home != "" {
			dir = filepath.Join(home, ".config", "gh")
		}
	}
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "hosts.yml")) //nolint:gosec // GitHub CLI's own config
	if err != nil {
		return ""
	}
	inGitHub := false
	childIndent := -1
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			inGitHub = strings.TrimSuffix(trimmed, ":") == "github.com"
			childIndent = -1
			continue
		}
		if !inGitHub {
			continue
		}
		// The active account is the host's own "user:" entry, at the indent of
		// the host's first child; logins listed under "users:" sit deeper.
		if childIndent < 0 {
			childIndent = indent
		}
		if indent == childIndent && strings.HasPrefix(trimmed, "user:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "user:")), `"'`)
		}
	}
	return ""
}

func copilotUsableToken(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.HasPrefix(value, "ghp_")
}

func copilotLoggedInUser(config map[string]json.RawMessage) bool {
	var users map[string]json.RawMessage
	if err := json.Unmarshal(config["loggedInUsers"], &users); err != nil {
		return false
	}
	for _, user := range users {
		if len(user) > 0 && string(user) != "null" && string(user) != `""` {
			return true
		}
	}
	return false
}
