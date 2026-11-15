package authprobe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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

func CodexAuthFileIdentity(ctx context.Context, path string) (email, name string) {
	if ctx.Err() != nil {
		return "", ""
	}
	data, err := os.ReadFile(path) //nolint:gosec // Codex's own auth file
	if err != nil {
		return "", ""
	}
	var auth struct {
		Tokens struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil {
		return "", ""
	}
	return TokenIdentity(auth.Tokens.IDToken)
}

func providerEntries(data []byte) map[string]json.RawMessage {
	var entries map[string]json.RawMessage
	if json.Unmarshal(data, &entries) != nil {
		return nil
	}
	for name := range entries {
		if trimmed := strings.TrimSpace(name); trimmed == "" || strings.HasPrefix(strings.ToLower(trimmed), "mcp:") {
			delete(entries, name)
		}
	}
	return entries
}

func JSONProviderNames(data []byte) string {
	entries := providerEntries(data)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, strings.TrimSpace(name))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func JSONProviderMethod(data []byte) domain.AgentAccountMethod {
	method := domain.AgentAccountMethod("")
	for _, raw := range providerEntries(data) {
		var entry struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(entry.Type)) {
		case "oauth", "wellknown":
			return domain.AgentAccountMethodAccount
		case "api", "api_key", "apikey":
			method = domain.AgentAccountMethodAPIKey
		}
	}
	return method
}
