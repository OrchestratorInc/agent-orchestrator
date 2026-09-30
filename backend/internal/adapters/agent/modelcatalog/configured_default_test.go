package modelcatalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// isolateHome points every config root discovery consults at an empty temp dir.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG_CONTENT",
		"COPILOT_HOME", "COPILOT_MODEL", "AIDER_MODEL"} {
		t.Setenv(key, "")
	}
	managed := openCodeManagedConfigDirs
	openCodeManagedConfigDirs = func() []string { return nil }
	t.Cleanup(func() { openCodeManagedConfigDirs = managed })
	return home
}

func TestOpenCodeDiscoveryMarksLocallyConfiguredModelAsDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a shell script")
	}
	home := isolateHome(t)
	workDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"model": "anthropic/claude-sonnet-4-6"}`)
	// The project config wins over the global one, and may be JSONC.
	writeConfig(t, filepath.Join(workDir, "opencode.jsonc"), `{
		// pinned for this repo
		"$schema": "https://opencode.ai/config.json",
		"model": "openai/gpt-5.4",
	}`)
	binary := filepath.Join(t.TempDir(), "opencode")
	writeConfig(t, binary, "#!/bin/sh\nprintf 'anthropic/claude-sonnet-4-6\\nopenai/gpt-5.4\\n'\n")
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}

	catalog, err := Discover(context.Background(), "opencode", binary, workDir, nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var defaults []string
	for _, item := range catalog.Models {
		if item.IsDefault {
			defaults = append(defaults, item.ID)
		}
	}
	if len(defaults) != 1 || defaults[0] != "openai/gpt-5.4" {
		t.Fatalf("defaults = %v, want [openai/gpt-5.4]", defaults)
	}
}

func TestApplyConfiguredDefault(t *testing.T) {
	listed := func() []ports.AgentModelInfo {
		return []ports.AgentModelInfo{{ID: "a/one", Label: "one"}, {ID: "b/two", Label: "two"}}
	}

	got := applyConfiguredDefault(listed(), "B/TWO")
	if got[0].IsDefault || !got[1].IsDefault || len(got) != 2 {
		t.Errorf("case-insensitive match = %#v", got)
	}

	got = applyConfiguredDefault(listed(), "c/three")
	if len(got) != 3 || got[2].ID != "c/three" || !got[2].IsDefault {
		t.Errorf("unlisted configured model should be appended as default: %#v", got)
	}

	reported := listed()
	reported[0].IsDefault = true
	got = applyConfiguredDefault(reported, "b/two")
	if !got[0].IsDefault || got[1].IsDefault {
		t.Errorf("a CLI-reported default must win over config: %#v", got)
	}

	for _, configured := range []string{"", "  ", "default", "Default"} {
		if got := applyConfiguredDefault(listed(), configured); len(got) != 2 || got[0].IsDefault || got[1].IsDefault {
			t.Errorf("placeholder %q must not select a default: %#v", configured, got)
		}
	}
}

func TestConfiguredDefaultModelSources(t *testing.T) {
	home := isolateHome(t)
	workDir := t.TempDir()

	writeConfig(t, filepath.Join(home, ".aider.conf.yml"), "model: sonnet\n")
	writeConfig(t, filepath.Join(workDir, ".aider.conf.yml"), "model: gpt-5.4\n")
	if got := configuredDefaultModel("aider", workDir, nil); got != "gpt-5.4" {
		t.Errorf("aider project config = %q, want gpt-5.4", got)
	}
	if got := configuredDefaultModel("aider", workDir, map[string]string{"AIDER_MODEL": "opus"}); got != "opus" {
		t.Errorf("aider AIDER_MODEL = %q, want opus", got)
	}

	writeConfig(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"defaultProvider": "anthropic", "defaultModel": "claude-opus-4-6"}`)
	if got := configuredDefaultModel("pi", workDir, nil); got != "anthropic/claude-opus-4-6" {
		t.Errorf("pi = %q", got)
	}

	writeConfig(t, filepath.Join(home, ".local", "share", "crush", "crush.json"), `{"models": {"large": {"provider": "openai", "model": "gpt-5.4"}}}`)
	if got := configuredDefaultModel("crush", workDir, nil); got != "openai/gpt-5.4" {
		t.Errorf("crush = %q", got)
	}

	writeConfig(t, filepath.Join(home, ".factory", "settings.json"), `{"model": "claude-sonnet-4-6"}`)
	if got := configuredDefaultModel("droid", workDir, nil); got != "claude-sonnet-4-6" {
		t.Errorf("droid = %q", got)
	}

	copilotHome := t.TempDir()
	copilotEnv := map[string]string{"COPILOT_HOME": copilotHome}
	// Legacy config.json is managed state, not the user's model choice.
	writeConfig(t, filepath.Join(copilotHome, "config.json"), `{"model": "stale-legacy-model"}`)
	if got := configuredDefaultModel("copilot", workDir, copilotEnv); got != "" {
		t.Errorf("copilot must ignore legacy config.json, got %q", got)
	}
	writeConfig(t, filepath.Join(copilotHome, "settings.json"), `{"model": "gpt-5.4"}`)
	if got := configuredDefaultModel("copilot", workDir, copilotEnv); got != "gpt-5.4" {
		t.Errorf("copilot user settings = %q, want gpt-5.4", got)
	}
	writeConfig(t, filepath.Join(workDir, ".github", "copilot", "settings.json"), `{"model": "claude-sonnet-4-6"}`)
	if got := configuredDefaultModel("copilot", workDir, copilotEnv); got != "claude-sonnet-4-6" {
		t.Errorf("copilot repository settings must override user settings, got %q", got)
	}
	copilotEnv["COPILOT_MODEL"] = "gpt-5.5"
	if got := configuredDefaultModel("copilot", workDir, copilotEnv); got != "gpt-5.5" {
		t.Errorf("COPILOT_MODEL must override settings files, got %q", got)
	}

	delete(copilotEnv, "COPILOT_MODEL")
	// workDir is not a git repository, so whether settings.local.json reaches
	// the launch worktree is unknown.
	writeConfig(t, filepath.Join(workDir, ".github", "copilot", "settings.local.json"), `{"model": "local-model"}`)
	if got := configuredDefaultModel("copilot", workDir, copilotEnv); got != "" {
		t.Errorf("copilot local settings with unknown tracking = %q, want unresolved", got)
	}

	if got := configuredDefaultModel("cursor", workDir, nil); got != "" {
		t.Errorf("agent without a config source = %q, want empty", got)
	}
}

func TestStripJSONCKeepsStringsAndDropsTrailingCommas(t *testing.T) {
	got := parseJSONCModelKey([]byte(`{
		/* block */ "url": "https://example.com//x", // line
		"model": "a/b",
	}`))
	if got != "a/b" {
		t.Errorf("model = %q, want a/b", got)
	}
}

func TestConfiguredDefaultFingerprint(t *testing.T) {
	home := isolateHome(t)
	workDir := t.TempDir()
	if got := discoveryConfigInputs(context.Background(), "opencode", workDir, nil); got != "" {
		t.Fatalf("no configured default must keep the binary-only fingerprint, got %q", got)
	}
	writeConfig(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"model": "openai/gpt-5.4"}`)
	if got := discoveryConfigInputs(context.Background(), "opencode", workDir, nil); got != "default=openai/gpt-5.4" {
		t.Fatalf("configured default must feed the fingerprint, got %q", got)
	}
}

// Sessions launch from worktrees holding only tracked files, so Copilot's
// .github/copilot/settings.local.json counts only when git tracks it.
func TestCopilotLocalSettingsCountOnlyWhenTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	isolateHome(t)
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	env := map[string]string{"COPILOT_HOME": t.TempDir()}
	writeConfig(t, filepath.Join(repo, ".github", "copilot", "settings.json"), `{"model": "repo-model"}`)
	writeConfig(t, filepath.Join(repo, ".github", "copilot", "settings.local.json"), `{"model": "local-model"}`)

	// Untracked (typically gitignored): absent from the worktree, so skipped.
	if got := configuredDefaultModel("copilot", repo, env); got != "repo-model" {
		t.Errorf("untracked local settings = %q, want repo-model", got)
	}

	// Tracked: present in every worktree and overrides repository settings.
	git("add", "-f", ".github/copilot/settings.local.json")
	if got := configuredDefaultModel("copilot", repo, env); got != "local-model" {
		t.Errorf("tracked local settings = %q, want local-model", got)
	}

	// COPILOT_MODEL still wins over every file.
	env["COPILOT_MODEL"] = "env-model"
	if got := configuredDefaultModel("copilot", repo, env); got != "env-model" {
		t.Errorf("COPILOT_MODEL over tracked local settings = %q, want env-model", got)
	}
}
