package proxyhost

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DeviceEmail names whose login this computer's own agent holds. AO never copies
// that login: the helper would renew the copy and sign the computer's agent out.
func (c *Client) DeviceEmail(_ context.Context, provider string) string {
	home, _ := os.UserHomeDir()
	if provider == "claude" {
		// Claude Code records whose login it holds beside its settings, not in the login.
		data, _ := os.ReadFile(filepath.Join(cmp.Or(os.Getenv("CLAUDE_CONFIG_DIR"), home), ".claude.json"))
		var settings any
		_ = json.Unmarshal(data, &settings)
		return strings.ToLower(strings.TrimSpace(text(settings, "oauthAccount", "emailAddress")))
	}
	data, _ := os.ReadFile(filepath.Join(cmp.Or(os.Getenv("CODEX_HOME"), filepath.Join(home, ".codex")), "auth.json"))
	var auth any
	_ = json.Unmarshal(data, &auth)
	email, _ := jwtClaims(text(auth, "tokens", "id_token"))["email"].(string)
	return strings.ToLower(strings.TrimSpace(email))
}

func jwtClaims(token string) (claims map[string]any) {
	if parts := strings.Split(token, "."); len(parts) == 3 {
		data, _ := base64.RawURLEncoding.DecodeString(parts[1])
		_ = json.Unmarshal(data, &claims)
	}
	return claims
}
