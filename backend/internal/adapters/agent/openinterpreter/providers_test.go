package openinterpreter

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSplitProviderModel(t *testing.T) {
	for _, tc := range []struct {
		model, provider, slug string
		ok                    bool
	}{
		{"deepseek:deepseek-v4-pro", "deepseek", "deepseek-v4-pro", true},
		{"openrouter:deepseek/deepseek-chat:free", "openrouter", "deepseek/deepseek-chat:free", true},
		{"google:gemini-3.8-flash", "google", "gemini-3.8-flash", true},
		{"gpt-6-sol", "", "gpt-6-sol", false},
		// A slug from the active provider may itself contain ":".
		{"deepseek/deepseek-chat:free", "", "deepseek/deepseek-chat:free", false},
		{"unknown:model", "", "unknown:model", false},
		{"deepseek:", "", "deepseek:", false},
		{"openai:gpt-6-sol", "openai", "gpt-6-sol", true},
	} {
		provider, slug, ok := SplitProviderModel(tc.model)
		if provider != tc.provider || slug != tc.slug || ok != tc.ok {
			t.Fatalf("SplitProviderModel(%q) = %q, %q, %v; want %q, %q, %v", tc.model, provider, slug, ok, tc.provider, tc.slug, tc.ok)
		}
		if tc.ok && ProviderModelID(provider, slug) != tc.model {
			t.Fatalf("ProviderModelID(%q, %q) does not round-trip %q", provider, slug, tc.model)
		}
	}
}

func TestLaunchSelectsGeneratedProvider(t *testing.T) {
	p := &Plugin{resolvedBinary: "/bin/interpreter"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{Model: "deepseek:deepseek-v4-pro"}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsRun(cmd, []string{"-c", `model_provider="deepseek"`, "--model", "deepseek-v4-pro"}) {
		t.Fatalf("generated provider launch argv: %q", cmd)
	}

	cmd, err = p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{Model: "gpt-6-sol"}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsRun(cmd, []string{"--model", "gpt-6-sol"}) || strings.Contains(strings.Join(cmd, " "), "model_provider") {
		t.Fatalf("active provider launch argv: %q", cmd)
	}
}

func TestGeneratedProviderKeysFollowPlatformWire(t *testing.T) {
	previous := chatWireSupported
	t.Cleanup(func() { chatWireSupported = previous })
	chatWireSupported = true
	if got, want := generatedProviderKeyNames(), []string{"ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY", "DEEPSEEK_API_KEY", "ZAI_API_KEY"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys with Chat Completions support = %q, want %q", got, want)
	}
	chatWireSupported = false
	if got, want := generatedProviderKeyNames(), []string{"ANTHROPIC_API_KEY"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys without Chat Completions support = %q, want %q", got, want)
	}
}

func TestLaunchPinsOpenAIModel(t *testing.T) {
	p := &Plugin{resolvedBinary: "/bin/interpreter"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{Model: "openai:gpt-6-sol"}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsRun(cmd, []string{"-c", `model_provider="openai"`, "--model", "gpt-6-sol"}) {
		t.Fatalf("OpenAI launch argv: %q", cmd)
	}
}

func TestConfiguredProvider(t *testing.T) {
	for _, tc := range []struct {
		name, config, system, want string
		ok                         bool
	}{
		{"missing config", "", "", OpenAIProvider, true},
		{"no provider", "model = \"gpt-6-sol\"\n", "", OpenAIProvider, true},
		{"selected provider", "model_provider = \"zai\"\nmodel = \"glm-5.1\"\n", "", "zai", true},
		{"named profile", "profile = \"work\"\n", "", "", false},
		{"malformed", "model_provider = [", "", "", false},
		// The system layer selects a provider when the user layer names none,
		// so native discovery would list that provider's models, not OpenAI's.
		{"system provider", "", "model_provider = \"anthropic\"\n", "anthropic", true},
		{"system provider below unnamed user config", "model = \"claude\"\n", "model_provider = \"anthropic\"\n", "anthropic", true},
		{"user provider overrides system", "model_provider = \"openai\"\n", "model_provider = \"anthropic\"\n", OpenAIProvider, true},
		{"malformed system config", "", "model_provider = [", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			system := filepath.Join(t.TempDir(), "config.toml")
			if tc.system != "" {
				if err := os.WriteFile(system, []byte(tc.system), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			previous := systemConfigPath
			systemConfigPath = system
			t.Cleanup(func() { systemConfigPath = previous })
			if got, ok := ConfiguredProvider(home); got != tc.want || ok != tc.ok {
				t.Fatalf("ConfiguredProvider = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func containsRun(cmd, run []string) bool {
	for i := 0; i+len(run) <= len(cmd); i++ {
		if reflect.DeepEqual(cmd[i:i+len(run)], run) {
			return true
		}
	}
	return false
}
