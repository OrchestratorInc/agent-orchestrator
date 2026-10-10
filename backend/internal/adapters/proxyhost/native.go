package proxyhost

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// ReadNativeAccount only reads the provider's local OAuth material. The source
// remains untouched and no token is persisted in AO's account database.
func (c *Client) ReadNativeAccount(ctx context.Context, provider string) (ports.NativeProviderCredential, error) {
	var data []byte
	var err error
	switch provider {
	case "codex":
		home := os.Getenv("CODEX_HOME")
		if home == "" {
			home, err = os.UserHomeDir()
			home = filepath.Join(home, ".codex")
		}
		if err == nil {
			var f *os.File
			f, err = os.Open(filepath.Join(home, "auth.json"))
			if os.IsNotExist(err) {
				return ports.NativeProviderCredential{}, nil
			}
			if err == nil {
				defer f.Close()
				data, err = io.ReadAll(io.LimitReader(f, (1<<20)+1))
			}
		}
	case "claude":
		data, err = agentcreds.ReadLocalOAuthCredentials(ctx, agentcreds.ResolveOptions{AllowKeychain: true})
	default:
		return ports.NativeProviderCredential{}, ports.ErrProviderAccountIncompatible
	}
	if err != nil {
		return ports.NativeProviderCredential{}, errors.New("native credential could not be read")
	}
	if len(data) == 0 {
		return ports.NativeProviderCredential{}, nil
	}
	return normalizeNative(provider, data)
}

// ImportNativeAccount is idempotent for a native snapshot, including after a
// daemon crash before the database commit. Never overwrite a file: CLIProxy
// may already have rotated its tokens since the original import.
func (c *Client) ImportNativeAccount(ctx context.Context, provider string, native ports.NativeProviderCredential) (ports.VerifiedProviderLogin, error) {
	sum, err := hex.DecodeString(native.Fingerprint)
	if err != nil || len(sum) != sha256.Size || (provider != "codex" && provider != "claude") {
		return ports.VerifiedProviderLogin{}, ports.ErrProviderAccountIncompatible
	}
	if err = c.Ensure(ctx); err != nil {
		return ports.VerifiedProviderLogin{}, err
	}
	id := "native-" + provider + "-" + native.Fingerprint
	verified, err := c.VerifiedAccountLogin(ctx, id)
	if err == nil {
		return verified, nil
	}
	var status statusError
	if !errors.As(err, &status) || status.status != http.StatusNotFound {
		return verified, err
	}
	if _, err = c.StartAccountLoginMode(ctx, provider, id, "import", ports.ProviderLoginInput{CredentialJSON: native.CredentialJSON}); err != nil {
		return verified, err
	}
	// Upload may be acknowledged before the file watcher registers the auth.
	for {
		verified, err = c.VerifiedAccountLogin(ctx, id)
		if err == nil {
			return verified, nil
		}
		if !errors.As(err, &status) || status.status != http.StatusNotFound {
			return verified, err
		}
		select {
		case <-ctx.Done():
			return verified, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func normalizeNative(provider string, data []byte) (ports.NativeProviderCredential, error) {
	if len(data) > 1<<20 {
		return ports.NativeProviderCredential{}, errors.New("native credential exceeds size limit")
	}
	var root map[string]any
	if json.Unmarshal(data, &root) != nil || root == nil {
		return ports.NativeProviderCredential{}, errors.New("native credential is malformed")
	}
	tokens := root
	key := "tokens"
	if provider == "claude" {
		key = "claudeAiOauth"
	}
	if nested, ok := root[key].(map[string]any); ok {
		tokens = nested
	}
	get := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := tokens[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	access, refresh := get("access_token", "accessToken"), get("refresh_token", "refreshToken")
	if access == "" {
		return ports.NativeProviderCredential{}, nil
	} // API-key-only native logins are not OAuth accounts.
	value := map[string]any{"type": provider, "access_token": access, "refresh_token": refresh}
	if provider == "codex" {
		idToken := get("id_token")
		claims := jwtClaims(idToken)
		scope, _ := claims["https://api.openai.com/auth"].(map[string]any)
		value["id_token"], value["email"] = idToken, claims["email"]
		value["account_id"], value["plan_type"] = scope["chatgpt_account_id"], scope["chatgpt_plan_type"]
		if id := get("account_id"); id != "" {
			value["account_id"] = id
		}
		if email := get("email"); email != "" {
			value["email"] = email
		}
		if exp, ok := jwtClaims(access)["exp"].(float64); ok {
			value["expired"] = time.Unix(int64(exp), 0).UTC().Format(time.RFC3339)
		}
	} else {
		// Claude OAuth tokens are opaque, not JWTs. CLIProxy resolves their identity
		// through the authenticated profile endpoint when reading the import result.
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ports.NativeProviderCredential{}, errors.New("native credential could not be normalized")
	}
	sum := sha256.Sum256(encoded)
	email, _ := value["email"].(string)
	return ports.NativeProviderCredential{Fingerprint: hex.EncodeToString(sum[:]), CredentialJSON: string(encoded), Email: strings.TrimSpace(email)}, nil
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	_ = json.Unmarshal(data, &claims)
	return claims
}
