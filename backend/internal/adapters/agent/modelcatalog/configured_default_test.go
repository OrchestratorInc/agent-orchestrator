package modelcatalog

import (
	"context"
	"os"
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
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "OPENCODE_CONFIG", "COPILOT_HOME", "AIDER_MODEL"} {
		t.Setenv(key, "")
	}
	return home
}

func TestOpenCodeDiscoveryMarksLocallyConfiguredModelAsDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a shell script")
	}
	home := isolateHome(t)
	workDir := t.TempDir()
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
	writeConfig(t, filepath.Join(copilotHome, "config.json"), `{"model": "gpt-5.4"}`)
	if got := configuredDefaultModel("copilot", workDir, map[string]string{"COPILOT_HOME": copilotHome}); got != "gpt-5.4" {
		t.Errorf("copilot = %q", got)
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
