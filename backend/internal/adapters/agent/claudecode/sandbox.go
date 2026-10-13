package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SandboxBinary is the Claude Code executable expected on the image PATH.
func (p *Plugin) SandboxBinary() string { return "claude" }

// PrepareSandbox points Claude at a per-project config dir that is mounted at
// the same path in the container, so AO's transcript lookup on the host
// (NativeConversationExists honours env CLAUDE_CONFIG_DIR) sees what the
// sandboxed Claude writes. The host keychain is unreachable from a container,
// so the credential must be an explicit env key or AO's captured setup-token.
func (p *Plugin) PrepareSandbox(env map[string]string, dataDir, projectDir, workspacePath string) error {
	configDir := filepath.Join(projectDir, "claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("claude-code: sandbox config dir: %w", err)
	}
	env["CLAUDE_CONFIG_DIR"] = configDir
	if env["ANTHROPIC_API_KEY"] == "" && env["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
		token := readLocalOAuthToken(dataDir)
		if token == "" {
			return fmt.Errorf("%w: Claude Code in a sandbox cannot use your keychain login; set ANTHROPIC_API_KEY, or CLAUDE_CODE_OAUTH_TOKEN (create one with \"claude setup-token\"), in the project env", ports.ErrSandboxMisconfigured)
		}
		env["CLAUDE_CODE_OAUTH_TOKEN"] = token
	}
	configPath := filepath.Join(configDir, ".claude.json")
	if err := ensureOnboardingComplete(configPath); err != nil {
		return err
	}
	return ensureWorkspaceTrusted(configPath, workspacePath)
}

// ensureOnboardingComplete marks first-run onboarding done in a fresh sandbox
// config so the interactive theme/login wizard never blocks a spawn.
func ensureOnboardingComplete(configPath string) (err error) {
	claudeTrustMu.Lock()
	defer claudeTrustMu.Unlock()
	cfg := map[string]any{}
	if raw, readErr := os.ReadFile(configPath); readErr == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("claude-code: refusing to rewrite corrupt %s: %w", configPath, err)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("claude-code: read %s: %w", configPath, readErr)
	}
	if done, _ := cfg["hasCompletedOnboarding"].(bool); done {
		return nil
	}
	cfg["hasCompletedOnboarding"] = true
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(configPath), ".claude.json-*")
	if err != nil {
		return fmt.Errorf("claude-code: create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("claude-code: chmod temp config: %w", err)
	}
	if _, err = tmp.Write(raw); err != nil {
		return fmt.Errorf("claude-code: write %s: %w", configPath, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("claude-code: close temp config: %w", err)
	}
	if err = os.Rename(tmpPath, configPath); err != nil {
		return fmt.Errorf("claude-code: replace %s: %w", configPath, err)
	}
	return nil
}
