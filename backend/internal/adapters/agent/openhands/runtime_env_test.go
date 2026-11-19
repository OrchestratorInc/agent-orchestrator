package openhands

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func clearOpenHandsLLMEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"LLM_API_KEY", "LLM_MODEL", "LLM_BASE_URL"} {
		t.Setenv(name, "")
	}
}

func TestOpenHandsRuntimeEnvironmentPreservesProviderOverrides(t *testing.T) {
	clearOpenHandsLLMEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://unrelated.invalid/v1")
	t.Setenv("LLM_API_KEY", "inherited-synthetic-key")
	env := map[string]string{
		"LLM_API_KEY":     "session-synthetic-key",
		"LLM_MODEL":       "openai/gpt-4.1-mini",
		"LLM_BASE_URL":    "https://explicit-provider.invalid/v1",
		"OPENAI_BASE_URL": "https://unrelated.invalid/v1",
		"UNRELATED":       "retained",
	}
	New().AugmentRuntimeEnv(env, t.TempDir())
	want := map[string]string{
		"LLM_API_KEY":     "session-synthetic-key",
		"LLM_MODEL":       "openai/gpt-4.1-mini",
		"LLM_BASE_URL":    "https://explicit-provider.invalid/v1",
		"OPENAI_BASE_URL": "https://api.openai.com/v1",
		"UNRELATED":       "retained",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatal("runtime environment did not retain the OpenHands overrides and replace the unrelated endpoint")
	}
	cmd, err := testPlugin().GetLaunchCommand(t.Context(), ports.LaunchConfig{Env: env})
	if err != nil || !reflect.DeepEqual(cmd, []string{"openhands", "--override-with-envs"}) {
		t.Fatalf("environment launch = %q, %v", cmd, err)
	}
	if strings.Contains(strings.Join(cmd, " "), "synthetic-key") {
		t.Fatal("provider key entered command arguments")
	}
}

func TestOpenHandsRuntimeEnvironmentDoesNotMaterializeInheritedCredentials(t *testing.T) {
	clearOpenHandsLLMEnv(t)
	t.Setenv("LLM_API_KEY", "inherited-synthetic-key")
	t.Setenv("LLM_MODEL", "openai/gpt-4.1-mini")
	t.Setenv("LLM_BASE_URL", "https://api.openai.com/v1")
	env := map[string]string{}
	dataDir := t.TempDir()
	New().AugmentRuntimeEnv(env, dataDir)
	if !reflect.DeepEqual(env, map[string]string{"OPENAI_BASE_URL": "https://api.openai.com/v1"}) {
		t.Fatal("inherited provider values were copied into the launch override map")
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("runtime environment wrote files: %v, %v", entries, err)
	}
	cmd, ok, err := testPlugin().GetRestoreCommand(t.Context(), ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native"}},
		Env:     env,
	})
	if want := []string{"openhands", "--override-with-envs", "--resume", "native"}; err != nil || !ok || !reflect.DeepEqual(cmd, want) {
		t.Fatalf("inherited environment restore = %q, %v, %v", cmd, ok, err)
	}
}

func TestOpenHandsAppliesEachProcessEnvironmentOverride(t *testing.T) {
	clearOpenHandsLLMEnv(t)
	for name, value := range map[string]string{
		"LLM_API_KEY":  "synthetic-key",
		"LLM_MODEL":    "openai/gpt-4.1-mini",
		"LLM_BASE_URL": "https://api.openai.com/v1",
	} {
		t.Run(name, func(t *testing.T) {
			cmd, err := testPlugin().GetLaunchCommand(t.Context(), ports.LaunchConfig{Env: map[string]string{name: value}})
			if err != nil || !reflect.DeepEqual(cmd, []string{"openhands", "--override-with-envs"}) {
				t.Fatalf("override launch = %q, %v", cmd, err)
			}
		})
	}
}

func TestOpenHandsEnvironmentReadinessWithoutSavedSettings(t *testing.T) {
	clearOpenHandsLLMEnv(t)
	t.Setenv("LLM_API_KEY", "daemon-synthetic-key")
	t.Setenv("LLM_MODEL", "openai/gpt-4.1-mini")
	dir := t.TempDir()
	t.Setenv("OPENHANDS_PERSISTENCE_DIR", dir)
	for _, tc := range []struct {
		name string
		env  map[string]string
		want ports.AgentAuthStatus
	}{
		{"inherited complete", nil, ports.AgentAuthStatusAuthorized},
		{"session complete", map[string]string{"LLM_API_KEY": "session-synthetic-key", "LLM_MODEL": "openai/gpt-4.1-mini"}, ports.AgentAuthStatusAuthorized},
		{"key explicitly removed", map[string]string{"LLM_API_KEY": ""}, ports.AgentAuthStatusUnauthorized},
		{"model explicitly removed", map[string]string{"LLM_MODEL": ""}, ports.AgentAuthStatusUnauthorized},
		{"endpoint alone", map[string]string{"LLM_API_KEY": "", "LLM_MODEL": "", "LLM_BASE_URL": "https://api.openai.com/v1"}, ports.AgentAuthStatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := testPlugin().AuthStatus(ports.WithAuthEnvironment(context.Background(), tc.env))
			if err != nil || status != tc.want {
				t.Fatalf("readiness = %s, %v; want %s", status, err, tc.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "agent_settings.json")); !os.IsNotExist(err) {
		t.Fatalf("environment readiness created native settings: %v", err)
	}
}
