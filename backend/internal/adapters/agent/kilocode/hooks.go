package kilocode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	kilocodePluginDirName = ".kilocode"
	kilocodePluginSubDir  = "plugins"

	kilocodePluginFileName = "ao-activity.ts"

	kilocodePluginSentinel = "agent-orchestrator: managed kilocode activity plugin"

	kilocodeHookCommandPrefix = "ao hooks kilocode "
)

//go:embed assets/ao-activity.ts
var kilocodePluginSource string

var kilocodeManagedEvents = []string{"session-start", "user-prompt-submit", "permission-request", "stop"}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("kilocode.GetAgentHooks: WorkspacePath is required")
	}

	pluginPath := kilocodePluginPath(cfg.WorkspacePath)
	if _, err := os.Stat(pluginPath); err == nil {
		managed, err := isAOManagedPlugin(pluginPath)
		if err != nil {
			return fmt.Errorf("kilocode.GetAgentHooks: %w", err)
		}
		if !managed {
			return fmt.Errorf("kilocode.GetAgentHooks: refusing to overwrite non-Orchestrator file at %s — move it so Orchestrator can install its plugin", pluginPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("kilocode.GetAgentHooks: stat plugin: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o750); err != nil {
		return fmt.Errorf("kilocode.GetAgentHooks: create plugin dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(pluginPath, []byte(kilocodePluginSource), 0o600); err != nil {
		return fmt.Errorf("kilocode.GetAgentHooks: write plugin: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(pluginPath), kilocodePluginFileName); err != nil {
		return fmt.Errorf("kilocode.GetAgentHooks: gitignore: %w", err)
	}
	return nil
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("kilocode.UninstallHooks: workspacePath is required")
	}

	pluginPath := kilocodePluginPath(workspacePath)
	managed, err := isAOManagedPlugin(pluginPath)
	if err != nil {
		return fmt.Errorf("kilocode.UninstallHooks: %w", err)
	}
	if !managed {
		return nil
	}
	if err := os.Remove(pluginPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("kilocode.UninstallHooks: remove plugin: %w", err)
	}
	return nil
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("kilocode.AreHooksInstalled: workspacePath is required")
	}
	managed, err := isAOManagedPlugin(kilocodePluginPath(workspacePath))
	if err != nil {
		return false, fmt.Errorf("kilocode.AreHooksInstalled: %w", err)
	}
	return managed, nil
}

func kilocodePluginPath(workspacePath string) string {
	return filepath.Join(workspacePath, kilocodePluginDirName, kilocodePluginSubDir, kilocodePluginFileName)
}

func isAOManagedPlugin(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return strings.Contains(string(data), kilocodePluginSentinel), nil
}
