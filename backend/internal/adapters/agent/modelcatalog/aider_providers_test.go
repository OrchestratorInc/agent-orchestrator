package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAiderConfiguredProvidersLayersEnvDotenvAndConfig(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	for _, sets := range aiderProviderKeys {
		for _, set := range sets {
			for _, name := range set {
				t.Setenv(name, "")
			}
		}
	}
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "GROQ_API_KEY",
		"ZAI_API_KEY", "DEEPSEEK_API_KEY", "MISTRAL_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
		"VERTEXAI_PROJECT", "VERTEXAI_LOCATION", "AIDER_ENV_FILE", "AIDER_OPENAI_API_KEY", "AIDER_ANTHROPIC_API_KEY",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		t.Setenv(name, "")
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Aider's OAuth key file, a configured env-file, the workspace .env that
	// blanks a key the environment sets, and config-file keys.
	write(filepath.Join(home, ".aider", "oauth-keys.env"), "OPENROUTER_API_KEY=sk-or\n")
	write(filepath.Join(home, "keys", "aider.env"), "export ZAI_API_KEY='zai' # z.ai\n")
	write(filepath.Join(workspace, ".env"), "GROQ_API_KEY=\n")
	write(filepath.Join(home, ".aider.conf.yml"), "env-file: ~/keys/aider.env\napi-key:\n  - deepseek=sk-ds\nset-env: VERTEXAI_PROJECT=proj\n")
	write(filepath.Join(workspace, ".aider.conf.yml"), "openai-api-key: sk-openai\n")
	env := map[string]string{"GROQ_API_KEY": "gsk", "MISTRAL_API_KEY": "mis", "VERTEXAI_LOCATION": "us-central1"}

	got := aiderConfiguredProviders(workspace, env)
	sort.Strings(got)
	want := []string{"deepseek", "mistral", "openai", "openrouter", "vertex_ai",
		"vertex_ai-ai21_models", "vertex_ai-deepseek_models", "vertex_ai-language-models", "vertex_ai-llama_models", "vertex_ai-zai_models", "zai"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}

	// Without a workspace, the home config's keys still count but the
	// workspace's openai-api-key and .env do not.
	got = aiderConfiguredProviders("", env)
	sort.Strings(got)
	if want := []string{"deepseek", "groq", "mistral", "openrouter", "vertex_ai",
		"vertex_ai-ai21_models", "vertex_ai-deepseek_models", "vertex_ai-language-models", "vertex_ai-llama_models", "vertex_ai-zai_models", "zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured without workspace = %v, want %v", got, want)
	}
}

func TestAiderConfiguredProvidersNeedsEveryPartOfACredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, sets := range aiderProviderKeys {
		for _, set := range sets {
			for _, name := range set {
				t.Setenv(name, "")
			}
		}
	}
	env := map[string]string{"AWS_ACCESS_KEY_ID": "AKIA", "AWS_SECRET_ACCESS_KEY": "", "CLOUDFLARE_API_KEY": "cf", "CLOUDFLARE_API_BASE": "https://cf"}
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "ZAI_API_KEY"} {
		env[name] = ""
	}
	got := aiderConfiguredProviders("", env)
	if want := []string{"cloudflare"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}
}

func TestAiderCatalogMarksAnthropicCredentials(t *testing.T) {
	for _, source := range []string{"environment", "config", "option"} {
		t.Run(source, func(t *testing.T) {
			home := t.TempDir()
			env := map[string]string{"HOME": home, "AIDER_ENV_FILE": "", "AIDER_OPENAI_API_KEY": "", "AIDER_ANTHROPIC_API_KEY": "", "ANTHROPIC_API_KEY": ""}
			for _, sets := range aiderProviderKeys {
				for _, set := range sets {
					for _, name := range set {
						env[name] = ""
					}
				}
			}
			switch source {
			case "environment":
				env["ANTHROPIC_API_KEY"] = "test-key"
			case "config":
				writeProviderTestFile(t, filepath.Join(home, ".aider.conf.yml"), "anthropic-api-key: test-key\n")
			case "option":
				env["AIDER_ANTHROPIC_API_KEY"] = "test-key"
			}
			catalog := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "claude-sonnet", Provider: "anthropic"}}}
			markConfiguredProviders(context.Background(), &catalog, ports.AgentModelDiscoveryRequest{AgentID: "aider", Env: env})
			if want := []string{"anthropic"}; !reflect.DeepEqual(catalog.ConfiguredProviders, want) {
				t.Fatalf("configured = %v, want %v", catalog.ConfiguredProviders, want)
			}
		})
	}
}
