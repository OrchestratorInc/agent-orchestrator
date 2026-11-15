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

func LocalOAuthTokenPath(dataDir string) string {
	return filepath.Join(dataDir, "harnesses", "claude-code", "setup-token")
}

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

func RemoveLocalOAuthToken(dataDir string) error {
	if err := os.Remove(LocalOAuthTokenPath(dataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove saved Claude setup token: %w", err)
	}
	return nil
}

func (p *Plugin) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	p.augmentCredential(env, dataDir)
}

func (p *Plugin) augmentCredential(env map[string]string, dataDir string) {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(env[claudeConfigDirEnv]) != "" {
		return
	}
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

func hasNativeCredential(ctx context.Context, env map[string]string) bool {
	opts := (agentcreds.ResolveOptions{
		AllowKeychain: true,
		CommandEnv:    env,
	}).WithClaudeSettings(ctx)
	_, found := agentcreds.ResolveLocal(ctx, agentcreds.ProviderFirstParty, opts)
	return found
}

func (p *Plugin) RuntimeCredentialEnv(dataDir string, base map[string]string) map[string]string {
	env := make(map[string]string, len(base)+1)
	for name, value := range base {
		env[name] = value
	}
	p.AugmentRuntimeEnv(env, dataDir)
	if len(env) == 0 {
		return nil
	}
	return env
}
