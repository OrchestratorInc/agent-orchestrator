package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// AO keeps two Claude setup-tokens under the AO data dir, never the user's
// ~/.claude, and they differ in how strongly the user chose them:
//
//   - LocalOAuthTokenPath holds the token the user picked as their login in AO's
//     Claude login menu (option 4). It is an explicit sign-in, so it signs local
//     sessions in even over Claude Code's own login.
//   - CloudOAuthTokenPath holds the token the desktop app saves when Claude Code
//     is connected to AO Cloud (frontend/src/main/cloud-auth.ts), so the same
//     credential can also serve local sessions. Nobody chose it for local work,
//     so it only applies when Claude Code has no login of its own -- otherwise
//     a stale cloud token would shadow a healthy native login (#5878).

// LocalOAuthTokenPath is where AO's Claude login menu saves the setup-token the
// user signed in with. Written by the login menu; read here at launch.
func LocalOAuthTokenPath(dataDir string) string {
	return filepath.Join(dataDir, "harnesses", "claude-code", "setup-token")
}

// CloudOAuthTokenPath is where the desktop app's AO Cloud sign-in saves its
// Claude setup-token. Written by cloud-auth.ts; read here at launch.
func CloudOAuthTokenPath(dataDir string) string {
	return filepath.Join(dataDir, "harnesses", "claude-code", "oauth-token")
}

func readLocalOAuthToken(dataDir string) string {
	return readTokenFile(LocalOAuthTokenPath(dataDir))
}

func readTokenFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveLocalOAuthToken writes the setup-token readable by the owner only. It
// replaces the file by rename, so a launch reading it concurrently sees the old
// token or the new one, never a truncated one.
func SaveLocalOAuthToken(dataDir string, token []byte) (err error) {
	path := LocalOAuthTokenPath(dataDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Claude credential directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".setup-token-*")
	if err != nil {
		return fmt.Errorf("save Claude setup token: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect Claude setup token: %w", err)
	}
	if _, err := tmp.Write(token); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save Claude setup token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save Claude setup token: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save Claude setup token: %w", err)
	}
	return nil
}

// RemoveLocalOAuthToken forgets the setup-token chosen in the login menu. AO's
// other Claude sign-in methods call it so the most recent login is the one
// sessions use. The AO Cloud token is left alone: it never outranks a native
// login, so it cannot shadow the login the user just completed.
func RemoveLocalOAuthToken(dataDir string) error {
	if err := os.Remove(LocalOAuthTokenPath(dataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove saved Claude setup token: %w", err)
	}
	return nil
}

// AugmentRuntimeEnv signs local sessions in with a setup-token AO saved,
// injected as CLAUDE_CODE_OAUTH_TOKEN. The token chosen in AO's login menu
// applies even when Claude Code also has its own login; signing in through
// another menu option removes it again. The AO Cloud token applies only when
// Claude Code has no first-party login of its own.
//
// The whole method is inert unless AO saved a token: no token on disk ->
// immediate return, zero change for existing users.
func (p *Plugin) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	// An account launch signs in through its own config folder only.
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(env[claudeConfigDirEnv]) != "" {
		return
	}
	// A credential set explicitly for this launch (project environment) is
	// more specific than the machine-wide login.
	for _, name := range claudeCredentialEnv {
		if strings.TrimSpace(env[name]) != "" {
			return
		}
	}
	if token := readLocalOAuthToken(dataDir); token != "" {
		env["CLAUDE_CODE_OAUTH_TOKEN"] = token
		return
	}
	token := readTokenFile(CloudOAuthTokenPath(dataDir))
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if hasNativeCredential(ctx, env) {
		return
	}
	env["CLAUDE_CODE_OAUTH_TOKEN"] = token
}

// hasNativeCredential is a cheap presence check (no network) for Claude Code's
// own first-party credential: environment, Claude settings env,
// ~/.claude/.credentials.json, or the keychain.
func hasNativeCredential(ctx context.Context, env map[string]string) bool {
	opts := (agentcreds.ResolveOptions{
		AllowKeychain: true,
		CommandEnv:    env,
	}).WithClaudeSettings(ctx)
	_, found := agentcreds.ResolveLocal(ctx, agentcreds.ProviderFirstParty, opts)
	return found
}

// RuntimeCredentialEnv is the setup-token AugmentRuntimeEnv gives a session
// that sets no credential of its own, or nil when it would add none.
func (p *Plugin) RuntimeCredentialEnv(dataDir string) map[string]string {
	env := map[string]string{}
	p.AugmentRuntimeEnv(env, dataDir)
	if len(env) == 0 {
		return nil
	}
	return env
}
