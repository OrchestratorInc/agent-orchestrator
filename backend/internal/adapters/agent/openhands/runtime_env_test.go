package openhands

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func clearOpenHandsLLMEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"LLM_API_KEY", "LLM_MODEL", "LLM_BASE_URL"} {
		t.Setenv(name, "")
	}
}

func TestOpenHandsImportedRootUsesPhysicalPaths(t *testing.T) {
	base, other := t.TempDir(), t.TempDir()
	root := filepath.Join(base, "store")
	for _, dir := range []string{root, filepath.Join(other, "nested"), filepath.Join(other, "store")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(t.TempDir(), "store")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	wrong := filepath.Join(t.TempDir(), "store")
	if err := os.Symlink(other, wrong); err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(base, "redirect")
	if err := os.Symlink(filepath.Join(other, "nested"), redirect); err != nil {
		t.Fatal(err)
	}
	masked := redirect + string(filepath.Separator) + ".." + string(filepath.Separator) + "store"
	if filepath.Clean(masked) != filepath.Clean(root) {
		t.Fatal("fixture does not hide a different physical store behind lexical cleanup")
	}
	for _, tc := range []struct {
		name     string
		recorded string
		override string
		refuse   bool
	}{
		{"override alias", root, alias, false},
		{"recorded alias", alias, root, false},
		{"look-alike alias", root, wrong, true},
		{"symlink plus parent traversal", root, masked, true},
		{"missing override", root, filepath.Join(other, "missing"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{
				ports.MetadataKeyImportSourceNativeID: "native",
				ports.MetadataKeyImportConfigDir:      tc.recorded,
			}}, Env: map[string]string{"OPENHANDS_CONVERSATIONS_DIR": tc.override}}
			_, err := importedConversationCommand([]string{"openhands", "--resume", "native"}, cfg)
			if (err != nil) != tc.refuse {
				t.Fatalf("root validation error = %v, want refusal %v", err, tc.refuse)
			}
		})
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
	want := map[string]string{
		"LLM_API_KEY":     "session-synthetic-key",
		"LLM_MODEL":       "openai/gpt-4.1-mini",
		"LLM_BASE_URL":    "https://explicit-provider.invalid/v1",
		"OPENAI_BASE_URL": "https://unrelated.invalid/v1",
		"UNRELATED":       "retained",
	}
	cmd, err := testPlugin().GetLaunchCommand(t.Context(), ports.LaunchConfig{Env: env})
	if err != nil || !reflect.DeepEqual(cmd, []string{"openhands", "--override-with-envs"}) {
		t.Fatalf("environment launch = %q, %v", cmd, err)
	}
	if strings.Contains(strings.Join(cmd, " "), "synthetic-key") {
		t.Fatal("provider key entered command arguments")
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatal("runtime environment did not retain explicit provider endpoints")
	}
}

func TestOpenHandsRuntimeEnvironmentDoesNotMaterializeInheritedCredentials(t *testing.T) {
	clearOpenHandsLLMEnv(t)
	t.Setenv("LLM_API_KEY", "inherited-synthetic-key")
	t.Setenv("LLM_MODEL", "openai/gpt-4.1-mini")
	t.Setenv("LLM_BASE_URL", "https://api.openai.com/v1")
	env := map[string]string{}
	dataDir := t.TempDir()
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
	if len(env) != 0 {
		t.Fatal("inherited provider values were copied into the launch override map")
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

// An inherited endpoint or model alone must not enable --override-with-envs:
// that would send the saved native key to an ambient LLM_BASE_URL.
func TestOpenHandsAmbientEndpointDoesNotOverrideSavedSettings(t *testing.T) {
	for _, name := range []string{"LLM_BASE_URL", "LLM_MODEL"} {
		t.Run(name, func(t *testing.T) {
			clearOpenHandsLLMEnv(t)
			value := "https://ambient-gateway.invalid/v1"
			if name == "LLM_MODEL" {
				value = "openai/gpt-4.1-mini"
			}
			t.Setenv(name, value)
			cmd, err := testPlugin().GetLaunchCommand(t.Context(), ports.LaunchConfig{Env: map[string]string{}})
			if err != nil || !reflect.DeepEqual(cmd, []string{"openhands"}) {
				t.Fatalf("ambient %s launch = %q, %v", name, cmd, err)
			}
			cmd, ok, err := testPlugin().GetRestoreCommand(t.Context(), ports.RestoreConfig{
				Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native"}},
				Env:     map[string]string{},
			})
			if want := []string{"openhands", "--resume", "native"}; err != nil || !ok || !reflect.DeepEqual(cmd, want) {
				t.Fatalf("ambient %s restore = %q, %v, %v", name, cmd, ok, err)
			}
		})
	}
}

// Endpoint routing is generic: an imported restore never injects a provider
// endpoint, so the saved native settings or explicit session env decide it.
func TestOpenHandsImportedRestoreNeverPinsAProviderEndpoint(t *testing.T) {
	clearOpenHandsLLMEnv(t)
	t.Setenv("OPENAI_BASE_URL", "https://ambient.invalid/v1")
	for _, tc := range []struct {
		name     string
		imported bool
		model    string
		env      map[string]string
		pin      bool
	}{
		{"imported OpenAI inherited endpoint", true, "openai/gpt-4.1-mini", nil, false},
		{"managed OpenAI", false, "openai/gpt-4.1-mini", nil, false},
		{"explicit project endpoint", true, "openai/gpt-4.1-mini", map[string]string{"OPENAI_BASE_URL": "https://gateway.invalid/v1"}, false},
		{"explicit clear", true, "openai/gpt-4.1-mini", map[string]string{"OPENAI_BASE_URL": ""}, false},
		{"explicit native endpoint", true, "openai/gpt-4.1-mini", map[string]string{"LLM_BASE_URL": "https://native-gateway.invalid/v1"}, false},
		{"other provider", true, "anthropic/claude-sonnet", nil, false},
		{"unknown saved model", true, "", nil, false},
		{"explicit process model", true, "", map[string]string{"LLM_MODEL": "openai/gpt-4.1-mini"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := map[string]string{ports.MetadataKeyAgentSessionID: "native"}
			if tc.imported {
				metadata[ports.MetadataKeyImportSourceNativeID] = "source"
				metadata[ports.MetadataKeyImportConfigDir] = t.TempDir()
			}
			before := make(map[string]string, len(tc.env))
			for k, v := range tc.env {
				before[k] = v
			}
			cmd, ok, err := testPlugin().GetRestoreCommand(t.Context(), ports.RestoreConfig{Session: ports.SessionRef{Metadata: metadata}, Config: ports.AgentConfig{Model: tc.model}, Env: tc.env})
			if err != nil || !ok {
				t.Fatalf("restore command = %v, %v", ok, err)
			}
			pinned := len(cmd) > 1 && cmd[0] == "env" && cmd[1] == "OPENAI_BASE_URL=https://api.openai.com/v1"
			if pinned != tc.pin {
				t.Fatalf("endpoint pin = %v, want %v: %q", pinned, tc.pin, cmd)
			}
			for k, v := range before {
				if tc.env[k] != v {
					t.Fatalf("explicit environment changed: %s", k)
				}
			}
		})
	}
	t.Run("no configured endpoint", func(t *testing.T) {
		t.Setenv("OPENAI_BASE_URL", "")
		cfg := ports.RestoreConfig{Config: ports.AgentConfig{Model: "openai/gpt-4.1-mini"}, Session: ports.SessionRef{Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "native", ports.MetadataKeyImportSourceNativeID: "source", ports.MetadataKeyImportConfigDir: t.TempDir(),
		}}}
		cmd, ok, err := testPlugin().GetRestoreCommand(t.Context(), cfg)
		if err != nil || !ok || strings.Contains(strings.Join(cmd, " "), "OPENAI_BASE_URL=") {
			t.Fatalf("native default restore pinned an endpoint = %q, %v, %v", cmd, ok, err)
		}
	})
}

func TestOpenHandsImportedRestorePinsPrivateRootOnEveryOpen(t *testing.T) {
	clearOpenHandsLLMEnv(t)
	r := openHandsImportFixture(t)
	ref, err := New().PrepareImportedSession(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"first open", map[string]string{"OPENHANDS_CONVERSATIONS_DIR": ref.ConfigDir}},
		// The source still has the same UUID. A generic environment lookup can
		// consequently omit the private root on an adopted restore.
		{"adopted reopen", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{
				ports.MetadataKeyAgentSessionID:       ref.NativeSessionID,
				ports.MetadataKeyImportSourceNativeID: r.NativeSessionID,
				ports.MetadataKeyImportConfigDir:      ref.ConfigDir,
			}}, Env: tc.env}
			cmd, ok, err := testPlugin().GetRestoreCommand(t.Context(), cfg)
			want := []string{"env", "OPENHANDS_CONVERSATIONS_DIR=" + ref.ConfigDir, "openhands", "--resume", ref.NativeSessionID}
			if err != nil || !ok || !reflect.DeepEqual(cmd, want) {
				t.Fatalf("private restore = %q, %v, %v; want %q", cmd, ok, err, want)
			}
			cfg.Env = map[string]string{"OPENHANDS_CONVERSATIONS_DIR": r.ConfigDir}
			if _, _, err := testPlugin().GetRestoreCommand(t.Context(), cfg); err == nil {
				t.Fatal("accepted a source-store override for the private import")
			}
			delete(cfg.Session.Metadata, ports.MetadataKeyImportConfigDir)
			if _, _, err := testPlugin().GetRestoreCommand(t.Context(), cfg); err == nil {
				t.Fatal("accepted an imported restore without a recorded store")
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
