package zcode

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

const (
	// zcodeAccessTokenKey is the shared-store slot the CLI's `zcode login zai`
	// writes the raw z.ai OAuth access token into.
	zcodeAccessTokenKey = "oauth:zai:access_token" //nolint:gosec // a credential's name, not a credential value
	// zcodeCredentialPrefix marks an encrypted value in the shared store.
	zcodeCredentialPrefix = "enc:v1:"
	// zcodeDataBaseDirEnv redirects the CLI's credential-store root (the
	// standard wrapper points it at a directory the desktop app never writes).
	zcodeDataBaseDirEnv = "ZCODE_DATA_BASE_DIR"
	// zcodeCredentialSecretEnv overrides the credential cipher's key seed.
	zcodeCredentialSecretEnv = "ZCODE_CREDENTIAL_SECRET" //nolint:gosec // a credential's name, not a credential value
	// zcodeSecretFallbackPrefix seeds the default credential key.
	zcodeSecretFallbackPrefix = "zcode-credential-fallback:" //nolint:gosec // a key-derivation seed prefix, not a credential value
)

// AuthStatus reports the local login state for the zcode CLI. A coding-plan
// API key in the AO-owned personal provider config (written by
// `ao zcode-login`) is checked first and means authorized with API-key file
// evidence: the key has no hourly expiry, and the launch path exports the
// same file, so it is what a session will actually run on. The port doc
// would call file-held evidence "configured"; the deviation is deliberate so
// the UI reads as ready instead of asking for a login the key makes moot.
// Otherwise a valid, unexpired z.ai OAuth token in the (possibly redirected)
// shared credential store means authorized; a missing or expired one means
// the CLI is installed but not logged in — reported as unauthorized rather
// than unknown so readiness surfaces an actionable "needs auth" instead of a
// doomed spawn. The CLI has no refresh flow for z.ai and the raw token lives
// one hour, so expiry is the expected steady state between logins.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	return zcodeLocalAuthStatus(ctx)
}

func zcodeLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if configPath := PersonalProviderConfigPath(aoDataDir()); configPath != "" {
		hasKey, err := providerConfigHasKey(ctx, configPath)
		if err != nil {
			return ports.AgentAuthStatusUnknown, err
		}
		if hasKey {
			ports.RecordAuthEvidence(ctx, authprobe.FileSource(configPath).WithMethod(domain.AgentAccountMethodAPIKey))
			return ports.AgentAuthStatusAuthorized, nil
		}
	}
	path, ok := zcodeCredentialsPath()
	if !ok {
		return ports.AgentAuthStatusUnknown, nil
	}
	data, err := authprobe.ReadCredentialFile(ctx, path)
	if os.IsNotExist(err) || (err == nil && strings.TrimSpace(string(data)) == "") {
		// Never logged in. ReadCredentialFile already recorded the file
		// diagnostic; auth state itself is a definite "not logged in".
		return ports.AgentAuthStatusUnauthorized, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_file_invalid")
		return ports.AgentAuthStatusUnknown, err
	}
	value := strings.TrimSpace(store[zcodeAccessTokenKey])
	if value == "" {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	token, err := decryptZcodeCredential(value)
	if err != nil {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_decrypt_failed")
		return ports.AgentAuthStatusUnknown, err
	}
	exp, hasExpiry, err := zcodeTokenExpiry(token)
	if err != nil {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_token_unreadable")
		return ports.AgentAuthStatusUnknown, err
	}
	if !hasExpiry {
		// A credential is present but carries no expiry to judge (a token
		// shape written by another ZCode surface): configured, not authorized.
		return ports.AgentAuthStatusConfigured, nil
	}
	if !time.Now().Before(exp) {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_expired")
		return ports.AgentAuthStatusUnauthorized, nil
	}
	ports.RecordAuthEvidence(ctx, authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAccount))
	return ports.AgentAuthStatusAuthorized, nil
}

// zcodeCredentialsPath resolves the credential store the adapter probes. The
// probe, the agentauth login plan, and every session launch must agree on one
// store, so all three resolve it the same way: an explicit
// ZCODE_DATA_BASE_DIR wins; otherwise the adapter-owned location under AO's
// data dir, <aoDataDir>/harnesses/zcode (see runtime_env.go — the launch path
// injects exactly that value, and the login plan does the same).
func zcodeCredentialsPath() (string, bool) {
	base := zcodeCredentialStoreBase()
	if base == "" {
		return "", false
	}
	return filepath.Join(base, ".zcode", "v2", "credentials.json"), true
}

// zcodeCredentialStoreBase mirrors the launch-side resolution: explicit
// ZCODE_DATA_BASE_DIR first, then the AO-owned store derived from the AO data
// dir (AO_DATA_DIR env, else ~/.ao/data — the same default config.Load uses).
func zcodeCredentialStoreBase() string {
	if v := strings.TrimSpace(os.Getenv(zcodeDataBaseDirEnv)); v != "" {
		return v
	}
	return zcodeHarnessStoreBase(aoDataDir())
}

// zcodeHarnessStoreBase returns the adapter-owned credential root under an AO
// data dir, or "" when the dir is unknown.
func zcodeHarnessStoreBase(aoDataDir string) string {
	if strings.TrimSpace(aoDataDir) == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(aoDataDir), "harnesses", "zcode")
}

// envAODataDir mirrors config.Load's data-dir resolution for the probe, which
// runs without a LaunchConfig. Adapters cannot import internal/config
// (layering), so the two-name rule is restated here; config.go:215 is the
// authoritative description.
//
// Known limitation: config.Load absolutizes a relative AO_DATA_DIR against the
// launch cwd before the daemon chdirs into it, while this reads the raw value
// (and cannot Abs it post-chdir without double-nesting). agentauth and the
// session manager use the absolutized cfg.DataDir, so with a relative override
// (AO_DATA_DIR=data) the probe would look under a different directory than the
// login menu writes and the launches export. The fix belongs in daemon startup
// (re-export AO_DATA_DIR as cfg.DataDir right after config.Load); until then a
// relative AO_DATA_DIR is unsupported for the zcode credential and API-key
// probes.
func aoDataDir() string {
	// EnvDataDir in session_manager; restated to avoid the import cycle.
	const envAODataDir = "AO_DATA_DIR"
	if v := strings.TrimSpace(os.Getenv(envAODataDir)); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ao", "data")
}

// decryptZcodeCredential returns the plaintext credential. Values prefixed
// enc:v1: are AES-256-GCM sealed with a key derived from the CLI's secret
// seed (ZCODE_CREDENTIAL_SECRET, else the platform/home/username fallback);
// unprefixed values pass through unchanged. The plaintext never leaves this
// package's auth probing: only its exp claim is ever inspected.
func decryptZcodeCredential(value string) (string, error) {
	if !strings.HasPrefix(value, zcodeCredentialPrefix) {
		return value, nil
	}
	parts := strings.Split(strings.TrimPrefix(value, zcodeCredentialPrefix), ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("zcode: malformed credential envelope")
	}
	iv, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("zcode: credential iv: %w", err)
	}
	tag, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("zcode: credential tag: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("zcode: credential ciphertext: %w", err)
	}
	block, err := aes.NewCipher(zcodeCredentialKey())
	if err != nil {
		return "", fmt.Errorf("zcode: credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("zcode: credential gcm: %w", err)
	}
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plaintext, err := gcm.Open(nil, iv, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("zcode: credential seal: %w", err)
	}
	return string(plaintext), nil
}

// zcodeCredentialKey derives the AES-256 key exactly as the CLI does:
// sha256 of ZCODE_CREDENTIAL_SECRET when set, else of the
// zcode-credential-fallback:<goos>:<home>:<username> seed.
func zcodeCredentialKey() []byte {
	seed := strings.TrimSpace(os.Getenv(zcodeCredentialSecretEnv))
	if seed == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = ""
		}
		username := "unknown"
		if u, err := user.Current(); err == nil && strings.TrimSpace(u.Username) != "" {
			username = strings.TrimSpace(u.Username)
		}
		seed = zcodeSecretFallbackPrefix + runtime.GOOS + ":" + home + ":" + username
	}
	sum := sha256.Sum256([]byte(seed))
	return sum[:]
}

// zcodeTokenExpiry reads the exp claim off the token's JWT payload without
// verifying a signature: authenticity of encrypted values is the AES seal's
// business, and the expiry is all the auth probe needs. hasExpiry is false
// when the token carries no exp claim.
func zcodeTokenExpiry(token string) (exp time.Time, hasExpiry bool, err error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false, fmt.Errorf("zcode: token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false, fmt.Errorf("zcode: token payload: %w", err)
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, false, fmt.Errorf("zcode: token claims: %w", err)
	}
	if claims.Exp <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(int64(claims.Exp), 0).UTC(), true, nil
}
