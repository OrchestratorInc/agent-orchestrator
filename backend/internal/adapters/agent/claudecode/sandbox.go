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
			return fmt.Errorf("%w: Claude Code in a sandbox needs AO's Claude login (run `ao claude-login`) or ANTHROPIC_API_KEY in the project env", ports.ErrSandboxMisconfigured)
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
func ensureOnboardingComplete(configPath string) error {
	claudeTrustMu.Lock()
	defer claudeTrustMu.Unlock()
	cfg := map[string]any{}
	if raw, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("claude-code: refusing to rewrite corrupt %s: %w", configPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("claude-code: read %s: %w", configPath, err)
	}
	if done, _ := cfg["hasCompletedOnboarding"].(bool); done {
		return nil
	}
	cfg["hasCompletedOnboarding"] = true
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("claude-code: write %s: %w", configPath, err)
	}
	return os.Rename(tmp, configPath)
}
