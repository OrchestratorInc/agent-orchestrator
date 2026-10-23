package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// LocalOAuthTokenPath is where AO persists a captured Claude setup-token so the
// SAME credential authenticates both local sessions and the cloud copy pushed to
// the control plane ("one login for local + cloud"). It lives under the AO data
// dir, never the user's ~/.claude. Written by AO's local login menu or desktop
// provider login; read here at launch.
func LocalOAuthTokenPath(dataDir string) string {
	return filepath.Join(dataDir, "harnesses", "claude-code", "oauth-token")
}

func readLocalOAuthToken(dataDir string) string {
	data, err := os.ReadFile(LocalOAuthTokenPath(dataDir))
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
	tmp, err := os.CreateTemp(dir, ".oauth-token-*")
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

// HasNativeCredential reports whether Claude Code already has its own
// first-party credential (environment, Claude settings env, credentials file,
// or keychain). When it does, AO's saved setup-token is never injected.
func HasNativeCredential(ctx context.Context, env map[string]string) bool {
	opts := (agentcreds.ResolveOptions{
		AllowKeychain: true,
		CommandEnv:    env,
	}).WithClaudeSettings(ctx)
	_, found := agentcreds.ResolveLocal(ctx, agentcreds.ProviderFirstParty, opts)
	return found
}

// AugmentRuntimeEnv injects AO's captured setup-token as CLAUDE_CODE_OAUTH_TOKEN
// for local sessions -- but ONLY when the machine has no native Claude login, so
// it can NEVER shadow a user's existing keychain / ~/.claude/.credentials.json
// login (CLAUDE_CODE_OAUTH_TOKEN sits at the top of agentcreds' resolution
// ladder, so an unconditional inject would beat a healthy native login -- the
// #5878 stale-credential-wins failure mode).
//
// The whole method is inert unless the user opted into the unified flow: no
// captured token on disk -> immediate return, zero change for existing users.
func (p *Plugin) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	// An account launch signs in through its own config folder only.
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(env[claudeConfigDirEnv]) != "" {
		return
	}
	token := readLocalOAuthToken(dataDir)
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// A cheap presence resolve (no network): env vars, Claude settings env,
	// ~/.claude/.credentials.json, and the keychain. If any native first-party
	// credential is present, defer to it and do not inject.
	if HasNativeCredential(ctx, env) {
		return
	}
	env["CLAUDE_CODE_OAUTH_TOKEN"] = token
}
