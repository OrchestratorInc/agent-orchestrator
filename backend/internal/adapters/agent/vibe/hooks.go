package vibe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const (
	vibeDirName       = ".vibe"
	vibeHooksFileName = "hooks.toml"

	vibeHooksSentinelStart = "# managed by agent-orchestrator: vibe hooks"
	vibeHooksSentinelEnd   = "# /managed by agent-orchestrator: vibe hooks"
)

// GetAgentHooks installs Vibe callbacks that capture the native session id.
// Vibe enables interaction logging by default, which is required for
// --resume. AO deliberately leaves the user-owned .vibe/config.toml untouched;
// users who disable log_interactions also disable native session restore.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("vibe.GetAgentHooks: WorkspacePath is required")
	}

	dir := filepath.Join(cfg.WorkspacePath, vibeDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("vibe.GetAgentHooks: create config dir: %w", err)
	}
	if err := mergeVibeHooksFile(filepath.Join(dir, vibeHooksFileName)); err != nil {
		return fmt.Errorf("vibe.GetAgentHooks: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(dir, vibeHooksFileName); err != nil {
		return fmt.Errorf("vibe.GetAgentHooks: gitignore: %w", err)
	}
	return nil
}

// UninstallHooks removes only AO's managed Vibe hook block.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("vibe.UninstallHooks: workspacePath is required")
	}
	path := filepath.Join(workspacePath, vibeDirName, vibeHooksFileName)
	data, err := os.ReadFile(path) //nolint:gosec // workspace-local adapter config
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("vibe.UninstallHooks: read %s: %w", path, err)
	}
	body := replaceVibeManagedBlock(string(data), "")
	if err := hookutil.AtomicWriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("vibe.UninstallHooks: write %s: %w", path, err)
	}
	return nil
}

// AreHooksInstalled reports whether AO's managed Vibe hook block is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("vibe.AreHooksInstalled: workspacePath is required")
	}
	path := filepath.Join(workspacePath, vibeDirName, vibeHooksFileName)
	data, err := os.ReadFile(path) //nolint:gosec // workspace-local adapter config
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("vibe.AreHooksInstalled: read %s: %w", path, err)
	}
	return strings.Contains(string(data), vibeHooksSentinelStart), nil
}

func mergeVibeHooksFile(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // workspace-local adapter config
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	body := replaceVibeManagedBlock(string(data), vibeHooksBlock())
	if err := hookutil.AtomicWriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func vibeHooksBlock() string {
	// Vibe's hooks.toml contract uses [[hooks]] entries with name, type,
	// optional match, command, and a floating-point timeout measured in seconds.
	// Native event names use underscores; AO's hook CLI uses hyphenated tokens.
	return vibeHooksSentinelStart + "\n\n" +
		vibeHookEntry("ao-session-metadata", "post_agent", "", "ao hooks vibe post-agent") +
		vibeHookEntry("ao-pre-tool", "pre_tool", "*", "ao hooks vibe pre-tool") +
		vibeHookEntry("ao-post-tool", "post_tool", "*", "ao hooks vibe post-tool") +
		vibeHooksSentinelEnd + "\n"
}

func vibeHookEntry(name, hookType, match, command string) string {
	var b strings.Builder
	b.WriteString("[[hooks]]\n")
	fmt.Fprintf(&b, "name = %q\n", name)
	fmt.Fprintf(&b, "type = %q\n", hookType)
	if match != "" {
		fmt.Fprintf(&b, "match = %q\n", match)
	}
	fmt.Fprintf(&b, "command = %q\n", command)
	b.WriteString("timeout = 30.0\n\n")
	return b.String()
}

func replaceVibeManagedBlock(existing, block string) string {
	start := strings.Index(existing, vibeHooksSentinelStart)
	if start < 0 {
		return joinVibeTOML(existing, block, "")
	}
	afterStart := existing[start+len(vibeHooksSentinelStart):]
	endRel := strings.Index(afterStart, vibeHooksSentinelEnd)
	if endRel < 0 {
		return joinVibeTOML(existing[:start], block, "")
	}
	end := start + len(vibeHooksSentinelStart) + endRel + len(vibeHooksSentinelEnd)
	return joinVibeTOML(existing[:start], block, existing[end:])
}

func joinVibeTOML(prefix, block, suffix string) string {
	var b strings.Builder
	prefix = strings.TrimRight(prefix, "\n")
	if prefix != "" {
		b.WriteString(prefix)
		b.WriteString("\n\n")
	}
	b.WriteString(block)
	suffix = strings.TrimLeft(suffix, "\n")
	if suffix != "" {
		b.WriteString("\n")
		b.WriteString(suffix)
	}
	return b.String()
}
