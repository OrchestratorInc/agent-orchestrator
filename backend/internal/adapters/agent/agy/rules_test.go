package agy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func readRule(t *testing.T, workspace string) string {
	t.Helper()
	data, err := os.ReadFile(agyStandingRulePath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGetAgentHooksInstallsAlwaysOnStandingRule(t *testing.T) {
	workspace := t.TempDir()
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, SystemPrompt: "You are an AO worker."}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	got := readRule(t, workspace)
	// AGY ignores a rule without `trigger: always_on`, so the frontmatter is
	// part of the contract, not decoration.
	if !strings.HasPrefix(got, "---\ntrigger: always_on\n---\n") {
		t.Fatalf("rule must open with always_on frontmatter, got %q", got)
	}
	if !strings.Contains(got, agyStandingRuleMark) || !strings.Contains(got, "You are an AO worker.") {
		t.Fatalf("rule is missing the AO marker or the instructions: %q", got)
	}
	ignore, err := os.ReadFile(filepath.Join(workspace, ".agents", "rules", ".gitignore"))
	if err != nil {
		t.Fatalf("rule must be gitignored so it cannot block worktree removal: %v", err)
	}
	if !strings.Contains(string(ignore), "/"+agyStandingRuleName) {
		t.Fatalf("gitignore does not name the rule: %q", ignore)
	}
}

func TestGetAgentHooksReadsStandingRuleFromPromptFile(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(file, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, SystemPrompt: "inline", SystemPromptFile: file}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	if got := readRule(t, workspace); !strings.Contains(got, "from file") || strings.Contains(got, "inline") {
		t.Fatalf("the prompt file must win over inline text: %q", got)
	}
}

func TestGetAgentHooksRefreshesStandingRuleOnRestore(t *testing.T) {
	workspace := t.TempDir()
	plugin := &Plugin{}
	for _, prompt := range []string{"first", "second"} {
		cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, SystemPrompt: prompt}
		if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}

	if got := readRule(t, workspace); !strings.Contains(got, "second") || strings.Contains(got, "first") {
		t.Fatalf("a re-install must replace the managed rule: %q", got)
	}
}

func TestGetAgentHooksWithoutStandingInstructionsWritesNoRule(t *testing.T) {
	workspace := t.TempDir()

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(agyStandingRulePath(workspace)); !os.IsNotExist(err) {
		t.Fatalf("no prompt must write no rule, stat err = %v", err)
	}
}

func TestGetAgentHooksRefusesToOverwriteForeignRule(t *testing.T) {
	workspace := t.TempDir()
	rulePath := agyStandingRulePath(workspace)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o750); err != nil {
		t.Fatal(err)
	}
	const foreign = "the project's own rule\n"
	if err := os.WriteFile(rulePath, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, SystemPrompt: "x"})

	if err == nil {
		t.Fatal("expected a refusal, not a silent skip that launches without the role prompt")
	}
	if got := readRule(t, workspace); got != foreign {
		t.Fatalf("the foreign rule was modified: %q", got)
	}
}

func TestGetAgentHooksPreservesProjectAgentsMD(t *testing.T) {
	workspace := t.TempDir()
	const project = "project conventions\n"
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, SystemPrompt: "x"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "AGENTS.md"))
	if err != nil || string(data) != project {
		t.Fatalf("the project's AGENTS.md must be untouched, got %q (err %v)", data, err)
	}
}

func TestUninstallHooksRemovesOnlyTheManagedRule(t *testing.T) {
	workspace := t.TempDir()
	plugin := &Plugin{}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, SystemPrompt: "x"}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	userRule := filepath.Join(workspace, ".agents", "rules", "user.md")
	if err := os.WriteFile(userRule, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := plugin.UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(agyStandingRulePath(workspace)); !os.IsNotExist(err) {
		t.Fatalf("the managed rule must be removed, stat err = %v", err)
	}
	if _, err := os.Stat(userRule); err != nil {
		t.Fatalf("a user rule must survive uninstall: %v", err)
	}
}

func TestUninstallHooksLeavesAForeignRuleAlone(t *testing.T) {
	workspace := t.TempDir()
	rulePath := agyStandingRulePath(workspace)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulePath, []byte("not ours\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&Plugin{}).UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}

	if got := readRule(t, workspace); got != "not ours\n" {
		t.Fatalf("a foreign file at the generated path was touched: %q", got)
	}
}
