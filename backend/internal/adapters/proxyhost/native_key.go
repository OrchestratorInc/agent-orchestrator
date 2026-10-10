package proxyhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// The addresses each provider's own API is reached at, and the label the
// imported key is known by in Account Manager.
const (
	claudeAPIBaseURL  = "https://api.anthropic.com"
	codexAPIBaseURL   = "https://api.openai.com/v1"
	nativeAPIKeyLabel = "Global API key"
)

// ReadNativeAPIKey finds the API key this computer's own agent is set up to
// use, and the address it uses it against. It reads only: nothing is changed
// where the key is kept. No key is not an error.
//
// It reports the key the agent would really use, and nothing more:
//   - Claude Code takes a key from its environment or its own settings, the
//     same way AO already works out what Claude Code is signed in with. A
//     subscription token is a sign-in, not a key, and a cloud provider
//     (Bedrock, Vertex, Foundry) has no key AO could carry.
//   - Codex uses a key only when it was signed in with one; that is the key in
//     its own auth file. A key sitting in the environment for other tools is
//     not what Codex uses and is left alone.
func (c *Client) ReadNativeAPIKey(ctx context.Context, provider string) (ports.NativeProviderAPIKey, error) {
	key, base := "", ""
	switch provider {
	case "claude":
		key, base = claudeNativeAPIKey(ctx)
	case "codex":
		var err error
		if key, base, err = codexNativeAPIKey(); err != nil {
			return ports.NativeProviderAPIKey{}, err
		}
	default:
		return ports.NativeProviderAPIKey{}, ports.ErrProviderAccountIncompatible
	}
	key, base = strings.TrimSpace(key), strings.TrimRight(strings.TrimSpace(base), "/")
	if key == "" || !importableBaseURL(base, c.Endpoint()) {
		return ports.NativeProviderAPIKey{}, nil
	}
	sum := sha256.Sum256([]byte(provider + "\x00" + key + "\x00" + base))
	return ports.NativeProviderAPIKey{Fingerprint: hex.EncodeToString(sum[:]), APIKey: key, BaseURL: base}, nil
}

func claudeNativeAPIKey(ctx context.Context) (string, string) {
	// Stored sign-ins are imported as sign-ins; only a key is wanted here.
	opts := (agentcreds.ResolveOptions{DisableStoredCredentials: true}).WithClaudeSettings(ctx)
	provider, ok := agentcreds.ResolveProvider("", opts)
	if !ok {
		return "", ""
	}
	credential, found := agentcreds.ResolveLocal(ctx, provider, opts)
	if !found || (credential.Kind != agentcreds.KindAPIKey && credential.Kind != agentcreds.KindAuthToken) {
		return "", ""
	}
	if credential.BaseURL == "" {
		return credential.Secret, claudeAPIBaseURL
	}
	return credential.Secret, credential.BaseURL
}

func codexNativeAPIKey() (string, string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", "", nil
		}
		home = filepath.Join(user, ".codex")
	}
	file, err := os.Open(filepath.Join(home, "auth.json"))
	if os.IsNotExist(err) {
		return "", "", nil
	}
	if err != nil {
		return "", "", errors.New("native API key could not be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return "", "", errors.New("native API key could not be read")
	}
	var auth struct {
		Mode   string `json:"auth_mode"`
		Key    string `json:"OPENAI_API_KEY"`
		Tokens struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil || strings.TrimSpace(auth.Key) == "" {
		return "", "", nil
	}
	// Codex says which of the two it is signed in with. Older files do not, and
	// there a ChatGPT sign-in wins over a key kept beside it.
	switch mode := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(auth.Mode))); {
	case mode == "apikey":
	case mode == "" && strings.TrimSpace(auth.Tokens.Access) == "":
	default:
		return "", "", nil
	}
	if base := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")); base != "" {
		return auth.Key, base, nil
	}
	return auth.Key, codexAPIBaseURL, nil
}

// importableBaseURL accepts what Account Manager accepts for a key typed in by
// hand, and refuses AO's own account helper: a daemon started from inside a
// managed session sees the helper's address and a session ticket in its
// environment, and those are not a key.
func importableBaseURL(base, helper string) bool {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	own, err := url.Parse(helper)
	return err != nil || own.Host == "" || !strings.EqualFold(own.Host, parsed.Host)
}

// ImportNativeAPIKey adds the key as an account's credential and is safe to
// repeat for the same key, including after a daemon crash before the database
// commit. A key that is already an account, added by hand, is reported as a
// conflict and left as it is.
func (c *Client) ImportNativeAPIKey(ctx context.Context, provider string, native ports.NativeProviderAPIKey) (ports.VerifiedProviderLogin, error) {
	sum, err := hex.DecodeString(native.Fingerprint)
	if err != nil || len(sum) != sha256.Size || (provider != "codex" && provider != "claude") || native.APIKey == "" {
		return ports.VerifiedProviderLogin{}, ports.ErrProviderAccountIncompatible
	}
	if err = c.Ensure(ctx); err != nil {
		return ports.VerifiedProviderLogin{}, err
	}
	id := "native-key-" + provider + "-" + native.Fingerprint
	verified, err := c.VerifiedAccountLogin(ctx, id)
	if err == nil {
		return verified, nil
	}
	var status statusError
	if !errors.As(err, &status) || status.status != http.StatusNotFound {
		return verified, err
	}
	// Whether the helper already holds this key is decided here, not from the
	// wording of a refusal.
	entries, err := c.apiKeys(ctx, provider+"-api-key")
	if err != nil {
		return verified, err
	}
	for _, entry := range entries {
		held, _ := entry["api-key"].(string)
		at, _ := entry["base-url"].(string)
		if held == native.APIKey && strings.TrimRight(at, "/") == native.BaseURL {
			return verified, ports.ErrProviderAccountConflict
		}
	}
	if _, err = c.StartAccountLoginMode(ctx, provider, id, "api_key", ports.ProviderLoginInput{APIKey: native.APIKey, BaseURL: native.BaseURL, Label: nativeAPIKeyLabel}); err != nil {
		return verified, err
	}
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
