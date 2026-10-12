package authprobe

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TokenIdentity reads the display identity a JWT carries in its payload (an
// OIDC id_token or a provider access token): the email and a name. It never
// verifies the token and never returns any part of it; callers use it only to
// show who a stored login belongs to.
func TokenIdentity(token string) (email, name string) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", ""
	}
	var claims struct {
		Email      string `json:"email"`
		Name       string `json:"name"`
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
		FirstName  string `json:"firstName"`
		LastName   string `json:"lastName"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return "", ""
	}
	name = strings.TrimSpace(claims.Name)
	if name == "" {
		name = strings.TrimSpace(claims.GivenName + " " + claims.FamilyName)
	}
	if name == "" {
		name = strings.TrimSpace(claims.FirstName + " " + claims.LastName)
	}
	return strings.TrimSpace(claims.Email), name
}

// JSONProviderNames returns the top-level keys of a JSON credential file that
// is keyed by provider (OpenCode, Pi and Kilo's auth.json), sorted and joined.
// Only key names are read back, never the stored credentials.
func JSONProviderNames(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // harness credential file chosen by the adapter
	if err != nil {
		return ""
	}
	var entries map[string]json.RawMessage
	if json.Unmarshal(data, &entries) != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// JSONProviderMethod reads how a provider-keyed credential file signs in from
// each entry's "type": "api" entries are API keys, "oauth" or "wellknown"
// entries are account logins. An account login anywhere wins; "" when no
// entry says.
func JSONProviderMethod(path string) domain.AgentAccountMethod {
	data, err := os.ReadFile(path) //nolint:gosec // harness credential file chosen by the adapter
	if err != nil {
		return ""
	}
	var entries map[string]struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &entries) != nil {
		return ""
	}
	method := domain.AgentAccountMethod("")
	for _, entry := range entries {
		switch strings.ToLower(strings.TrimSpace(entry.Type)) {
		case "oauth", "wellknown":
			return domain.AgentAccountMethodAccount
		case "api", "api_key", "apikey":
			method = domain.AgentAccountMethodAPIKey
		}
	}
	return method
}

// GitHubCLIUser returns the github.com login the GitHub CLI is signed in as,
// read from its hosts.yml (GH_CONFIG_DIR, then XDG_CONFIG_HOME, then
// ~/.config/gh). Tokens in the file are never read.
func GitHubCLIUser() string {
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

// SignedInEmail returns the email a status command prints after "signed in
// as" (Amp's usage output), or "".
func SignedInEmail(output string) string {
	lower := strings.ToLower(output)
	index := strings.Index(lower, "signed in as")
	if index < 0 {
		return ""
	}
	fields := strings.Fields(output[index+len("signed in as"):])
	if len(fields) == 0 {
		return ""
	}
	candidate := strings.Trim(fields[0], `"'()<>,.;:`)
	if !strings.Contains(candidate, "@") {
		return ""
	}
	return candidate
}
