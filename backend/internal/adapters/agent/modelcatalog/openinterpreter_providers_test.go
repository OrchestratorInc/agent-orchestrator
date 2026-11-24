package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// `debug models` reports only the active provider, so a saved key for a
// generated provider adds that provider's models with a selecting ID, and the
// default OpenAI catalog is pinned to OpenAI. Chat Completions providers are
// offered only where the platform's release can run them.
func TestOpenInterpreterListsKeyedGeneratedProviders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake interpreter requires a POSIX shell")
	}
	binary := filepath.Join(t.TempDir(), "interpreter")
	script := `#!/bin/sh
[ "$1" = debug ] && [ "$2" = models ] || exit 2
case "$4" in
'model_provider="deepseek"') printf '{"models":[{"slug":"deepseek-v4-pro","display_name":"DeepSeek V4 Pro","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}' ;;
'model_provider="anthropic"') printf '{"models":[{"slug":"claude-opus-4-7","display_name":"Claude Opus 4.7","visibility":"list"}]}' ;;
'model_provider="openrouter"') exit 1 ;;
'') printf '{"models":[{"slug":"gpt-6-sol","display_name":"GPT-6 Sol","visibility":"list"}]}' ;;
*) exit 3 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME": t.TempDir(), "INTERPRETER_HOME": t.TempDir(),
		"DEEPSEEK_API_KEY": "saved-key", "ANTHROPIC_API_KEY": "saved-key", "OPENROUTER_API_KEY": "saved-key",
		"GEMINI_API_KEY": "", "ZAI_API_KEY": "",
	}
	catalog, err := discoverCommandCatalog(context.Background(), "open-interpreter", binary, t.TempDir(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ports.AgentModelInfo{}
	for _, model := range catalog.Models {
		byID[model.ID] = model
	}
	if model, ok := byID["openai:gpt-6-sol"]; !ok || model.Provider != "openai" {
		t.Fatalf("default OpenAI model not pinned: %#v", catalog.Models)
	}
	if model, ok := byID["anthropic:claude-opus-4-7"]; !ok || model.Label != "Claude Opus 4.7 (Anthropic)" || model.Provider != "anthropic" {
		t.Fatalf("Anthropic model = %#v (catalog %#v)", model, catalog.Models)
	}
	want := 2
	if deepseek, ok := byID["deepseek:deepseek-v4-pro"]; runtime.GOOS == "linux" {
		if !ok || deepseek.Label != "DeepSeek V4 Pro (DeepSeek)" || len(deepseek.Efforts) != 1 {
			t.Fatalf("DeepSeek model = %#v (catalog %#v)", deepseek, catalog.Models)
		}
		want = 3
	} else if ok {
		t.Fatalf("Chat Completions provider offered where its wire is unsupported: %#v", catalog.Models)
	}
	// A provider that fails to list, or has no key, adds nothing.
	if len(catalog.Models) != want {
		t.Fatalf("catalog models = %#v, want %d", catalog.Models, want)
	}
}

// A provider chosen inside Open Interpreter keeps its own model IDs: only the
// default OpenAI catalog is pinned.
func TestOpenInterpreterKeepsConfiguredProviderModels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake interpreter requires a POSIX shell")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = \"custom\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "interpreter")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '{\"models\":[{\"slug\":\"custom-model\",\"visibility\":\"list\"}]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": t.TempDir(), "INTERPRETER_HOME": home, "DEEPSEEK_API_KEY": "", "ANTHROPIC_API_KEY": "", "OPENROUTER_API_KEY": "", "GEMINI_API_KEY": "", "ZAI_API_KEY": ""}
	catalog, err := discoverCommandCatalog(context.Background(), "open-interpreter", binary, t.TempDir(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].ID != "custom-model" {
		t.Fatalf("configured provider models = %#v", catalog.Models)
	}
}

func TestOpenInterpreterFingerprintTracksProviderKeys(t *testing.T) {
	env := map[string]string{"INTERPRETER_HOME": t.TempDir(), "DEEPSEEK_API_KEY": "", "ANTHROPIC_API_KEY": "", "GEMINI_API_KEY": "", "OPENROUTER_API_KEY": ""}
	request := ports.AgentModelDiscoveryRequest{AgentID: "open-interpreter", Env: env}
	without := (Discoverer{}).CatalogFingerprint(context.Background(), request)
	env["ANTHROPIC_API_KEY"] = "saved-key"
	with := (Discoverer{}).CatalogFingerprint(context.Background(), request)
	if without == with {
		t.Fatal("saving an Anthropic key did not invalidate the catalog")
	}
	env["ANTHROPIC_API_KEY"] = "rotated-key"
	if with != (Discoverer{}).CatalogFingerprint(context.Background(), request) {
		t.Fatal("fingerprint depends on the key value, not only its presence")
	}
}
