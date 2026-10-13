package zcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PrepareProtocol installs the same additive native hooks for the stock
// app-server host, which explicitly enables workspace-hook trust. This is not
// ACP and does not make the stock TUI's blocked hook path available.
func (p *Plugin) PrepareProtocol(ctx context.Context, cfg ports.WorkspaceHookConfig) (string, error) {
	if cfg.SessionID == "" || filepath.Base(cfg.SessionID) != cfg.SessionID || strings.ContainsAny(cfg.SessionID, `/\`) {
		return "", fmt.Errorf("zcode: invalid AO session identity")
	}
	if !filepath.IsAbs(cfg.DataDir) || !filepath.IsAbs(cfg.WorkspacePath) {
		return "", fmt.Errorf("zcode: absolute data/workspace paths are required")
	}
	if err := ValidateProtocolHookBudget(ctx, cfg); err != nil {
		return "", err
	}
	if err := p.RefreshProtocolInstructions(ctx, cfg); err != nil {
		return "", err
	}
	if err := p.GetAgentHooks(ctx, cfg); err != nil {
		return "", err
	}
	binary, err := p.zcodeBinary(ctx)
	if err != nil {
		return "", err
	}
	if err := p.prepareHookTrust(ctx, binary, cfg.WorkspacePath, cfg.Env); err != nil {
		return "", err
	}
	return binary, nil
}

// ValidateProtocolRestore refuses missing history and a different worktree
// before creating a replacement native process. No latest/fresh fallback exists.
func ValidateProtocolRestore(ctx context.Context, workspace, id string, env map[string]string) error {
	if !nativeIDPattern.MatchString(id) {
		return fmt.Errorf("zcode: invalid native session ID")
	}
	return validateNativeRestore(ctx, workspace, id, env)
}

// RefreshProtocolInstructions changes only AO's private per-session input.
// It is also used for an attachment to the same live native process.
func (p *Plugin) RefreshProtocolInstructions(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if cfg.SessionID == "" || filepath.Base(cfg.SessionID) != cfg.SessionID || strings.ContainsAny(cfg.SessionID, `/\`) || !filepath.IsAbs(cfg.DataDir) {
		return fmt.Errorf("zcode: invalid private instruction ownership")
	}
	prompt, err := ReadStandingInstructions(ctx, cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return err
	}
	path := filepath.Join(cfg.DataDir, "prompts", cfg.SessionID, "system.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, []byte(prompt), 0o600)
}
