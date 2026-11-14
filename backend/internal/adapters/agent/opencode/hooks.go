package opencode

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

const (
	opencodePluginDirName = ".opencode"
	opencodePluginSubDir  = "plugins"

	opencodePluginFileName   = "ao-activity.ts"
	opencodeV2PluginFileName = "ao-activity-v2.ts"
	opencodeV2PluginSentinel = "agent-orchestrator: managed opencode-v2 activity plugin"

	opencodePluginSentinel = "agent-orchestrator: managed opencode activity plugin"

	opencodeHookCommandPrefix = "ao hooks opencode "

	opencodeSkillSubDir = "skills"

	opencodeSkillMarkerFile = ".using-ao.ao-managed"

	opencodeSkillSentinel = "agent-orchestrator: managed opencode using-ao skill"
)

//go:embed assets/ao-activity.ts
var opencodePluginSource string

var opencodeManagedEvents = []string{"session-start", "user-prompt-submit", "active", "stop", "permission-blocked", "permission-resolved"}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("opencode.GetAgentHooks: WorkspacePath is required")
	}

	pluginPath := opencodePluginPath(cfg.WorkspacePath)
	if _, err := os.Stat(pluginPath); err == nil {
		managed, err := isAOManagedPlugin(pluginPath)
		if err != nil {
			return fmt.Errorf("opencode.GetAgentHooks: %w", err)
		}
		if !managed {
			return fmt.Errorf("opencode.GetAgentHooks: refusing to overwrite non-Orchestrator file at %s — move it so Orchestrator can install its plugin", pluginPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("opencode.GetAgentHooks: stat plugin: %w", err)
	}
	if err := removeManagedPlugin(filepath.Join(filepath.Dir(pluginPath), opencodeV2PluginFileName), opencodeV2PluginSentinel); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: remove managed OpenCode 2 plugin: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o750); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: create plugin dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(pluginPath, []byte(opencodePluginSource), 0o600); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: write plugin: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(pluginPath), opencodePluginFileName); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: gitignore: %w", err)
	}
	if err := InstallUsingAOSkill(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: %w", err)
	}
	if err := hookutil.InstallBugTriageSkill(SkillsDir(cfg.WorkspacePath)); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: %w", err)
	}
	if err := seedPluginDependencyStub(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: %w", err)
	}
	return nil
}

const opencodePluginDependency = "@opencode-ai/plugin"

func seedPluginDependencyStub(workspacePath string) error {
	dir := filepath.Join(workspacePath, opencodePluginDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	stub := hasDependencyStub(dir)
	foreign := false
	for _, entry := range entries {
		switch entry.Name() {
		case opencodeSkillSubDir, ".gitignore":
		case "node_modules", "package-lock.json":
			foreign = foreign || !stub
		case opencodePluginSubDir:
			plugins, err := os.ReadDir(filepath.Join(dir, opencodePluginSubDir))
			if err != nil {
				return fmt.Errorf("read %s: %w", filepath.Join(dir, opencodePluginSubDir), err)
			}
			for _, plugin := range plugins {
				if name := plugin.Name(); name != opencodePluginFileName && name != ".gitignore" {
					foreign = true
				}
			}
		default:
			foreign = true
		}
	}
	if foreign {
		if stub {
			_ = os.Remove(filepath.Join(dir, "package-lock.json"))
			_ = os.Remove(filepath.Join(dir, "node_modules"))
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o750); err != nil {
		return fmt.Errorf("seed opencode node_modules: %w", err)
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(dir, "package-lock.json"), []byte(dependencyStubLock+"\n"), 0o600); err != nil {
		return fmt.Errorf("seed opencode package-lock.json: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(dir, "node_modules", "package.json", "package-lock.json", "bun.lock"); err != nil {
		return fmt.Errorf("seed opencode .gitignore: %w", err)
	}
	return nil
}

var dependencyStubLock = fmt.Sprintf(`{"name":"ao-opencode","lockfileVersion":3,"requires":true,"packages":{"":{"dependencies":{%q:"*"}}}}`, opencodePluginDependency)

func hasDependencyStub(dir string) bool {
	lock, err := os.ReadFile(filepath.Join(dir, "package-lock.json"))
	if err != nil || strings.TrimSpace(string(lock)) != dependencyStubLock {
		return false
	}
	modules, err := os.ReadDir(filepath.Join(dir, "node_modules"))
	return err == nil && len(modules) == 0
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("opencode.UninstallHooks: workspacePath is required")
	}

	pluginPath := opencodePluginPath(workspacePath)
	managed, err := isAOManagedPlugin(pluginPath)
	if err != nil {
		return fmt.Errorf("opencode.UninstallHooks: %w", err)
	}
	if managed {
		if err := os.Remove(pluginPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("opencode.UninstallHooks: remove plugin: %w", err)
		}
	}
	if err := UninstallUsingAOSkill(workspacePath); err != nil {
		return fmt.Errorf("opencode.UninstallHooks: %w", err)
	}
	if err := hookutil.UninstallBugTriageSkill(SkillsDir(workspacePath)); err != nil {
		return fmt.Errorf("opencode.UninstallHooks: %w", err)
	}
	return nil
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("opencode.AreHooksInstalled: workspacePath is required")
	}
	managed, err := isAOManagedPlugin(opencodePluginPath(workspacePath))
	if err != nil {
		return false, fmt.Errorf("opencode.AreHooksInstalled: %w", err)
	}
	return managed, nil
}

func opencodePluginPath(workspacePath string) string {
	return filepath.Join(workspacePath, opencodePluginDirName, opencodePluginSubDir, opencodePluginFileName)
}

func opencodeSkillDir(workspacePath string) string {
	return filepath.Join(workspacePath, opencodePluginDirName, opencodeSkillSubDir, skillassets.SkillName)
}

func SkillsDir(workspacePath string) string {
	return filepath.Join(workspacePath, opencodePluginDirName, opencodeSkillSubDir)
}

func opencodeSkillMarkerPath(workspacePath string) string {
	return filepath.Join(SkillsDir(workspacePath), opencodeSkillMarkerFile)
}

func InstallUsingAOSkill(workspacePath string) error {
	skillDir := opencodeSkillDir(workspacePath)
	if info, err := os.Stat(skillDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("refusing to overwrite non-directory at %s — move it so Orchestrator can install using-ao", skillDir)
		}
		managed, err := isAOManagedSkill(workspacePath)
		if err != nil {
			return err
		}
		if !managed {
			return fmt.Errorf("refusing to overwrite non-Orchestrator skill at %s — move it so Orchestrator can install using-ao", skillDir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat skill dir: %w", err)
	}

	skillsParent := SkillsDir(workspacePath)
	if err := os.MkdirAll(skillsParent, 0o750); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(opencodeSkillMarkerPath(workspacePath), []byte(opencodeSkillSentinel+"\n"), 0o600); err != nil {
		return fmt.Errorf("write skill marker: %w", err)
	}
	if err := skillassets.Materialize(skillDir); err != nil {
		return fmt.Errorf("materialize using-ao skill: %w", err)
	}
	if err := ensureSkillTreeGitignored(skillDir); err != nil {
		return fmt.Errorf("skill gitignore: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(skillsParent, opencodeSkillMarkerFile); err != nil {
		return fmt.Errorf("skill marker gitignore: %w", err)
	}
	return nil
}

func ensureSkillTreeGitignored(skillRoot string) error {
	byDir := map[string][]string{}
	err := filepath.WalkDir(skillRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		dir := filepath.Dir(path)
		byDir[dir] = append(byDir[dir], filepath.Base(path))
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk skill tree: %w", err)
	}
	for dir, names := range byDir {
		if err := hookutil.EnsureWorkspaceGitignore(dir, names...); err != nil {
			return err
		}
	}
	return nil
}

func UninstallUsingAOSkill(workspacePath string) error {
	managed, err := isAOManagedSkill(workspacePath)
	if err != nil {
		return err
	}
	if !managed {
		return nil
	}
	if err := os.RemoveAll(opencodeSkillDir(workspacePath)); err != nil {
		return fmt.Errorf("remove skill dir: %w", err)
	}
	markerPath := opencodeSkillMarkerPath(workspacePath)
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove skill marker: %w", err)
	}
	return nil
}

func isAOManagedPlugin(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return strings.Contains(string(data), opencodePluginSentinel), nil
}

func removeManagedPlugin(path, sentinel string) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is rooted in the caller-owned workspace
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), sentinel) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func isAOManagedSkill(workspacePath string) (bool, error) {
	data, err := os.ReadFile(opencodeSkillMarkerPath(workspacePath)) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read skill marker: %w", err)
	}
	return strings.Contains(string(data), opencodeSkillSentinel), nil
}
