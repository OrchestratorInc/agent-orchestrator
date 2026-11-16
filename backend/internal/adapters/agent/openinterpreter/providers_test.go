package openinterpreter

import (
	"context"
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

func TestGeneratedProviderKeysMatchOfferedKeys(t *testing.T) {
	want := []string{"ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY", "DEEPSEEK_API_KEY"}
	if got := generatedProviderKeyNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("generated provider keys = %q, want %q", got, want)
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
