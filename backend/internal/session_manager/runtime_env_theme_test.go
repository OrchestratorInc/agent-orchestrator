package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/termtheme"
)

func TestAugmentAgentRuntimeEnvAppliesTerminalTheme(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, termtheme.FileName), []byte("light\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{dataDir: dataDir}

	env := map[string]string{}
	m.augmentAgentRuntimeEnv(fakeAgent{}, env)
	if env[termtheme.EnvTheme] != "light" || env[termtheme.EnvColorFgBg] != "0;15" {
		t.Fatalf("env = %v, want light theme hints", env)
	}

	env = map[string]string{termtheme.EnvColorFgBg: "15;0"}
	m.augmentAgentRuntimeEnv(envAugmentingAgent{key: "X", value: "y"}, env)
	if env[termtheme.EnvColorFgBg] != "15;0" {
		t.Fatalf("COLORFGBG = %q, want project value kept", env[termtheme.EnvColorFgBg])
	}
	if env["X"] != filepath.Join(dataDir, "y") {
		t.Fatalf("adapter augmentation lost: %v", env)
	}
}

func TestAugmentAgentRuntimeEnvLeavesThemeUnsetWithoutHint(t *testing.T) {
	m := &Manager{dataDir: t.TempDir()}
	env := map[string]string{}
	m.augmentAgentRuntimeEnv(fakeAgent{}, env)
	if _, ok := env[termtheme.EnvTheme]; ok {
		t.Fatalf("env = %v, want no theme hints", env)
	}
	if _, ok := env[termtheme.EnvColorFgBg]; ok {
		t.Fatalf("env = %v, want no theme hints", env)
	}
}

func TestAugmentAgentRuntimeEnvWindowsKeepsCaseVariantOverride(t *testing.T) {
	previous := envKeysCaseInsensitive
	envKeysCaseInsensitive = true
	t.Cleanup(func() { envKeysCaseInsensitive = previous })

	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, termtheme.FileName), []byte("light\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{dataDir: dataDir}
	env := map[string]string{"term_theme": "dark"}
	m.augmentAgentRuntimeEnv(fakeAgent{}, env)

	var variants []string
	for key := range env {
		if strings.EqualFold(key, termtheme.EnvTheme) {
			variants = append(variants, key)
		}
	}
	if len(variants) != 1 || env["term_theme"] != "dark" {
		t.Fatalf("TERM_THEME variants = %v (env %v), want only the project's term_theme=dark", variants, env)
	}
	if env[termtheme.EnvColorFgBg] != "0;15" {
		t.Fatalf("COLORFGBG = %q, want 0;15", env[termtheme.EnvColorFgBg])
	}
}
