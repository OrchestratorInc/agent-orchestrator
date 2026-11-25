package agy

import (
	"context"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillbridge"
)

// SkillDirectories is the folder of bridged skills to add to a session: agy
// loads <dir>/.agents/skills from any directory added to its workspace, with
// --add-dir or ACP additionalDirectories, for that session only.
func SkillDirectories(dataDir, sessionID string) []string {
	if dir := skillbridge.Attached(dataDir, string(domain.HarnessAgy), sessionID); dir != "" {
		return []string{dir}
	}
	return nil
}

// NativeSkillRoots lists where agy itself finds skills: the user's global
// folders and plugins, agy's built-ins, the folders a skills.json declares, and
// ~/.agents/skills from 1.5.16.
func (p *Plugin) NativeSkillRoots(ctx context.Context, workspacePath string, env map[string]string) []string {
	gemini, err := GeminiHome(env)
	if err != nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	config := filepath.Join(gemini, "config")
	roots := []string{
		filepath.Join(config, "skills"),
		filepath.Join(gemini, "skills"),
	}
	// agy does not read ~/.agents/skills with another data folder (--gemini-dir).
	if home != "" && filepath.Clean(gemini) == filepath.Join(home, ".gemini") && p.loadsAgentsSkills(ctx) {
		roots = append(roots, filepath.Join(home, ".agents", "skills"))
	}
	plugins := []string{filepath.Join(config, "plugins")}
	for _, storage := range []string{ACPStorageDir, CLIStorageDir} {
		roots = append(roots,
			filepath.Join(gemini, storage, "skills"),
			filepath.Join(gemini, storage, "builtin", "skills"),
		)
		plugins = append(plugins, filepath.Join(gemini, storage, "builtin", "plugins"))
	}
	for _, dir := range plugins {
		found, _ := filepath.Glob(filepath.Join(dir, "*", "skills"))
		roots = append(roots, found...)
	}
	roots = append(roots, skillbridge.DeclaredRoots(filepath.Join(config, "skills.json"), gemini, home)...)
	if workspacePath != "" {
		roots = append(roots, skillbridge.DeclaredRoots(
			filepath.Join(workspacePath, ".agents", "skills.json"), workspacePath, home)...)
	}
	return roots
}

func (p *Plugin) loadsAgentsSkills(ctx context.Context) bool {
	binary, err := p.agyBinary(ctx)
	if err != nil {
		return false
	}
	version, err := CLIVersion(ctx, binary)
	return err == nil && readsAgentsSkills(version)
}
