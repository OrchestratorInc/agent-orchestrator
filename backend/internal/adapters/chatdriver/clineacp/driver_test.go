package clineacp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigureStartsACPMode(t *testing.T) {
	isolateProviderSettings(t, `{"providers":{}}`)
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	want := []string{"--acp"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if env != nil {
		t.Fatalf("env = %#v, want nil", env)
	}
}

func TestConfigureEnablesAutoApproveForAutoAndBypass(t *testing.T) {
	isolateProviderSettings(t, `{"providers":{}}`)
	for _, mode := range []ports.PermissionMode{
		ports.PermissionModeAuto,
		ports.PermissionModeBypassPermissions,
	} {
		args, _, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: mode})
		if err != nil {
			t.Fatalf("configure(%q): %v", mode, err)
		}
		want := []string{"--acp", "--auto-approve", "true"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("mode %q: args = %#v, want %#v", mode, args, want)
		}
	}
}

func TestConfigureKeepsPromptingForDefaultAndAcceptEdits(t *testing.T) {
	isolateProviderSettings(t, `{"providers":{}}`)
	for _, mode := range []ports.PermissionMode{
		ports.PermissionModeDefault,
		ports.PermissionModeAcceptEdits,
	} {
		args, _, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: mode})
		if err != nil {
			t.Fatalf("configure(%q): %v", mode, err)
		}
		want := []string{"--acp"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("mode %q: args = %#v, want %#v", mode, args, want)
		}
	}
}

func TestConfigureDeliversModelThroughLaunchEnv(t *testing.T) {
	isolateProviderSettings(t, `{"providers":{}}`)
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{Model: "anthropic/claude-sonnet-4"})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if want := []string{"--acp"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	wantEnv := map[string]string{"CLINE_MODEL": "anthropic/claude-sonnet-4"}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Fatalf("env = %#v, want %#v", env, wantEnv)
	}
}

func isolateProviderSettings(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLINE_PROVIDER_SETTINGS_PATH", path)
	t.Setenv("CLINE_PROVIDER", "")
}

// Both OAuth logins can coexist; ACP must follow the saved selection rather
// than selecting the first available account and charging a different provider.
func TestConfigureUsesSavedProviderWithAOModelOverride(t *testing.T) {
	isolateProviderSettings(t, `{"lastUsedProvider":"openai-codex","providers":{"cline":{"settings":{"provider":"cline"}},"openai-codex":{"settings":{"provider":"openai-codex"}}}}`)
	_, env, err := configure(t.Context(), acpdriver.LaunchConfig{Model: " gpt-5.3-codex "})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"CLINE_PROVIDER": "openai-codex", "CLINE_MODEL": "gpt-5.3-codex"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("launch env = %#v, want %#v", env, want)
	}
}

func TestConfigureHonorsExplicitProvider(t *testing.T) {
	isolateProviderSettings(t, `not valid JSON`)
	_, env, err := configure(t.Context(), acpdriver.LaunchConfig{Env: map[string]string{"CLINE_PROVIDER": "anthropic"}})
	if err != nil {
		t.Fatal(err)
	}
	if env["CLINE_PROVIDER"] != "anthropic" {
		t.Fatalf("provider = %q, want explicit anthropic", env["CLINE_PROVIDER"])
	}
}
