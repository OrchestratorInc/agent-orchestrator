package copilot

import (
	"context"
	"encoding/json"
	"errors"
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
	cleaned, err := stripJSONC(data)
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if err := json.Unmarshal(cleaned, &config); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	for _, key := range []string{"authToken", "accessToken", "token"} {
		var token string
		if err := json.Unmarshal(config[key], &token); err == nil && copilotUsableToken(token) {
			return ports.AgentAuthStatusAuthorized, true, nil
		}
	}
	if copilotLoggedInUser(config) {
		// Account metadata only. Copilot CLI keeps OAuth tokens in the OS
		// credential store, so a config login entry proves a sign-in happened
		// but not that the credential is still valid (issue #8219).
		return ports.AgentAuthStatusConfigured, true, nil
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

// copilotLoggedInUser reports whether Copilot's config records at least one
// account sign-in. Current Copilot CLI releases list loggedInUsers as an array
// of account objects; older releases stored a map keyed by host. Empty or
// stale records count as no login. An entry is account metadata only: the
// credential itself lives in the OS credential store and is never read here.
func copilotLoggedInUser(config map[string]json.RawMessage) bool {
	users := config["loggedInUsers"]
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(users, &legacy); err == nil {
		for _, user := range legacy {
			if copilotLoginRecord(user) {
				return true
			}
		}
		return false
	}
	var accounts []json.RawMessage
	if err := json.Unmarshal(users, &accounts); err != nil {
		return false
	}
	for _, user := range accounts {
		if copilotLoginRecord(user) {
			return true
		}
	}
	return false
}

// copilotLoginRecord reports whether a loggedInUsers entry records an actual
// account. Objects must carry at least one field, however their braces are
// spaced; nulls and empty or whitespace-only strings are not logins.
func copilotLoginRecord(user json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(user, &value); err != nil {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		return len(v) > 0
	case string:
		return strings.TrimSpace(v) != ""
	default:
		return false
	}
}

// stripJSONC removes // and /* */ comments and trailing commas so Copilot
// CLI's JSONC config.json (issue #8219) decodes with encoding/json. String
// contents are kept verbatim, including sequences that look like comments
// inside URLs. It mirrors modelcatalog.stripJSONC, except that a block
// comment left unterminated by a truncated write is reported as an error so
// malformed input takes the GitHub CLI fallback instead of parsing as if the
// missing tail were part of the comment; keep the two in sync otherwise.
func stripJSONC(raw []byte) ([]byte, error) {
	out := make([]byte, 0, len(raw))
	inString, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(raw) && raw[i+1] == '/':
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			if i < len(raw) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(raw) && raw[i+1] == '*':
			end := -1
			for j := i + 2; j+1 < len(raw); j++ {
				if raw[j] == '*' && raw[j+1] == '/' {
					end = j + 1
					break
				}
			}
			if end < 0 {
				return nil, errors.New("unterminated block comment")
			}
			i = end
		case c == ']' || c == '}':
			// Drop a trailing comma left before this closer.
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out, nil
}
