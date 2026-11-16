package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// `debug models` reports only the active provider, so a saved key for a
// generated provider must add that provider's models with a selecting ID.
func TestOpenInterpreterListsKeyedGeneratedProviders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake interpreter requires a POSIX shell")
	}
	binary := filepath.Join(t.TempDir(), "interpreter")
	script := `#!/bin/sh
[ "$1" = debug ] && [ "$2" = models ] || exit 2
case "$4" in
'model_provider="deepseek"') printf '{"models":[{"slug":"deepseek-v4-pro","display_name":"DeepSeek V4 Pro","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}' ;;
'model_provider="anthropic"') exit 1 ;;
'') printf '{"models":[{"slug":"gpt-6-sol","display_name":"GPT-6 Sol","visibility":"list"}]}' ;;
*) exit 3 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME": t.TempDir(), "INTERPRETER_HOME": t.TempDir(),
		"DEEPSEEK_API_KEY": "saved-key", "ANTHROPIC_API_KEY": "saved-key",
		"GEMINI_API_KEY": "", "OPENROUTER_API_KEY": "",
	}
	catalog, err := discoverCommandCatalog(context.Background(), "open-interpreter", binary, t.TempDir(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ports.AgentModelInfo{}
	for _, model := range catalog.Models {
		byID[model.ID] = model
	}
	if _, ok := byID["gpt-6-sol"]; !ok {
		t.Fatalf("active provider model missing: %#v", catalog.Models)
	}
	deepseek, ok := byID["deepseek:deepseek-v4-pro"]
	if !ok || deepseek.Label != "DeepSeek V4 Pro (DeepSeek)" || deepseek.Provider != "deepseek" || len(deepseek.Efforts) != 1 {
		t.Fatalf("DeepSeek model = %#v (catalog %#v)", deepseek, catalog.Models)
	}
	// A provider that fails to list, or has no key, adds nothing.
	if len(catalog.Models) != 2 {
		t.Fatalf("catalog models = %#v, want the active and DeepSeek models only", catalog.Models)
	}
}

func TestOpenInterpreterFingerprintTracksProviderKeys(t *testing.T) {
	env := map[string]string{"INTERPRETER_HOME": t.TempDir(), "DEEPSEEK_API_KEY": "", "ANTHROPIC_API_KEY": "", "GEMINI_API_KEY": "", "OPENROUTER_API_KEY": ""}
	request := ports.AgentModelDiscoveryRequest{AgentID: "open-interpreter", Env: env}
	without := (Discoverer{}).CatalogFingerprint(context.Background(), request)
	env["DEEPSEEK_API_KEY"] = "saved-key"
	with := (Discoverer{}).CatalogFingerprint(context.Background(), request)
	if without == with {
		t.Fatal("saving a DeepSeek key did not invalidate the catalog")
	}
	env["DEEPSEEK_API_KEY"] = "rotated-key"
	if with != (Discoverer{}).CatalogFingerprint(context.Background(), request) {
		t.Fatal("fingerprint depends on the key value, not only its presence")
	}
}
