package claudecode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalOAuthTokenPath is where AO persists the setup-token a user signs in with
// through AO's Claude login menu, so the SAME credential authenticates both
// local sessions and the cloud copy pushed to the control plane ("one login for
// local + cloud"). It lives under the AO data dir, never the user's ~/.claude.
// Written by the login menu; read here at launch.
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

// RemoveLocalOAuthToken forgets a saved setup-token. AO's other Claude sign-in
// methods call it so the most recent login is the one sessions use.
func RemoveLocalOAuthToken(dataDir string) error {
	if err := os.Remove(LocalOAuthTokenPath(dataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove saved Claude setup token: %w", err)
	}
	return nil
}

// AugmentRuntimeEnv signs local sessions in with the setup-token the user chose
// in AO's Claude login menu, injected as CLAUDE_CODE_OAUTH_TOKEN. Choosing the
// token is an explicit login, so it applies even when Claude Code also has its
// own login; signing in through another menu option removes the token again.
//
// The whole method is inert unless the user picked the setup-token login: no
// token on disk -> immediate return, zero change for existing users.
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
	}
}
