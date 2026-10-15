package sessionmanager

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// ErrTestingHarness means a Litmus testing session was asked for on anything
// but a claude-code or codex TUI worker.
var ErrTestingHarness = errors.New("session: testing sessions run on the claude-code or codex harness as TUI workers")

type testingSpawnKey struct{}

// WithTestingProfile marks the spawn made with ctx as a Litmus testing session:
// it works in its run dir with no worktree or branch, and launches under the
// fixed tool policy. Restores recognise such a session by its workspace path.
func WithTestingProfile(ctx context.Context) context.Context {
	return context.WithValue(ctx, testingSpawnKey{}, true)
}

func isTestingSpawn(ctx context.Context) bool {
	on, _ := ctx.Value(testingSpawnKey{}).(bool)
	return on
}

// createTestingWorkspace makes the session's run dir its working directory.
func createTestingWorkspace(dataDir string, id domain.SessionID, projectID domain.ProjectID) (ports.WorkspaceInfo, error) {
	dir := litmus.RunDir(dataDir, string(id))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return ports.WorkspaceInfo{Path: dir, SessionID: id, ProjectID: projectID}, nil
}

// applyTestingLaunch gives a launch in a run dir the unattended permission mode, the tool lists and a model. Claude Code
// gets dontAsk and the deny list; Codex has neither, so it runs with approvals and sandbox off and relies on GuardEnv.
func applyTestingLaunch(cfg *ports.LaunchConfig, dataDir string, harness domain.AgentHarness) {
	if litmus.IsRunDir(dataDir, cfg.WorkspacePath) {
		cfg.Permissions, cfg.AllowedTools, cfg.DisallowedTools = testingPolicy(harness)
		testerModel(&cfg.Config, harness)
	}
}

// applyTestingRestore is applyTestingLaunch for the native resume command.
func applyTestingRestore(cfg *ports.RestoreConfig, dataDir string, harness domain.AgentHarness) {
	if litmus.IsRunDir(dataDir, cfg.Session.WorkspacePath) {
		cfg.Permissions, cfg.AllowedTools, cfg.DisallowedTools = testingPolicy(harness)
		testerModel(&cfg.Config, harness)
	}
}

func testingPolicy(harness domain.AgentHarness) (ports.PermissionMode, []string, []string) {
	if harness == domain.HarnessCodex {
		return ports.PermissionModeBypassPermissions, nil, nil
	}
	allowed, disallowed := litmus.ToolPolicy()
	return ports.PermissionModeDontAsk, allowed, disallowed
}

// testerSettings gives a testing spawn the model and effort set in the project's Litmus settings, unless the spawn
// names its own. As the spawn override they beat the project's worker config and get the same catalog check; with
// neither set, the worker config stands and testerModel supplies the defaults at launch.
func (m *Manager) testerSettings(cfg *ports.SpawnConfig) error {
	if cfg.ProjectID == "" {
		return nil
	}
	ps, err := litmus.LoadProjectSettings(m.dataDir, string(cfg.ProjectID))
	if err != nil {
		return fmt.Errorf("litmus settings of project %s: %w", cfg.ProjectID, err)
	}
	cfg.AgentConfig.Model = cmp.Or(cfg.AgentConfig.Model, ps.Model)
	cfg.AgentConfig.Effort = cmp.Or(cfg.AgentConfig.Effort, ps.Effort)
	return nil
}

// testerModel keeps the model and effort the project chose (in its Litmus settings or for its workers) and
// fills in Litmus's defaults only where the project left them empty. The default model is a Claude alias, so a Codex
// tester without a model keeps Codex's own default.
func testerModel(c *ports.AgentConfig, harness domain.AgentHarness) {
	if c.Model == "" && harness != domain.HarnessCodex {
		c.Model = litmus.TesterModel
	}
	if c.Effort == "" {
		c.Effort = litmus.TesterEffort
	}
}

// applyTestingEnv names the run in the environment of a session working in its run dir, blocks git push there and gives gh no login.
func applyTestingEnv(env map[string]string, dataDir string, id domain.SessionID, workspacePath string) {
	if litmus.IsRunDir(dataDir, workspacePath) {
		env[litmus.EnvRun] = litmus.RunID(string(id))
		litmus.GuardEnv(env, workspacePath)
	}
}

// isTestingSession reports whether id is a Litmus testing session: the spawn that
// is making it (marked on ctx), or a session whose stored workspace is a run dir.
// The run dir alone is not proof: any session's `ao testing intake` creates one.
func (m *Manager) isTestingSession(ctx context.Context, id domain.SessionID) bool {
	if isTestingSpawn(ctx) {
		return true
	}
	rec, found, err := m.store.GetSession(ctx, id)
	return err == nil && found && litmus.IsRunDir(m.dataDir, rec.Metadata.WorkspacePath)
}

// ErrTestingSessionFixed means a testing session was asked to change its harness
// or interface: the tool policy is applied only at spawn and relaunch.
var ErrTestingSessionFixed = errors.New("session: a testing session cannot change its harness or interface")

func (m *Manager) refuseTestingSession(ctx context.Context, id domain.SessionID) error {
	if m.isTestingSession(ctx, id) {
		return fmt.Errorf("%w: %s", ErrTestingSessionFixed, id)
	}
	return nil
}

// litmusSkillPointer replaces the generic ao guide in a testing session's system
// prompt: that guide steers toward previews and builds, which the policy denies.
func (m *Manager) litmusSkillPointer() string {
	skillFile := filepath.ToSlash(filepath.Join(skillassets.LitmusDir(m.dataDir), "SKILL.md"))
	return "\n\n## Testing a pull request\n\n" +
		"You are a Litmus tester. Read `" + skillFile + "` and follow it. Do not load other ao guides."
}
