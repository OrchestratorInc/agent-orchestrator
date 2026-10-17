package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeProviderTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKimchiConfiguredProvidersReadsKimchiHarnessStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMCHI_API_KEY", "")
	t.Setenv("KIMCHI_CUSTOM_KEY", "")
	harness := filepath.Join(home, ".config", "kimchi", "harness")
	writeProviderTestFile(t, filepath.Join(harness, "auth.json"), `{
		"openai-codex":{"type":"oauth","access":"a","refresh":"r"},
		"expired":{"type":"oauth"},
		"scripted":{"type":"api_key","key":"!security find-generic-password"}
	}`)
	writeProviderTestFile(t, filepath.Join(harness, "models.json"), `{"providers":{
		"kimchi-dev/anthropic":{"models":[]},
		"custom":{"apiKey":"KIMCHI_CUSTOM_KEY","models":[]}
	}}`)
	// Pi's own store is not Kimchi's: Kimchi overrides PI_CODING_AGENT_DIR.
	writeProviderTestFile(t, filepath.Join(home, ".pi", "agent", "auth.json"), `{"zai":{"type":"api_key","key":"k"}}`)
	env := map[string]string{"HOME": home}

	if got, want := kimchiConfiguredProviders("", env), []string{"openai-codex"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}

	// The Kimchi key in its config unlocks kimchi-dev and its sub-providers;
	// a models.json variable counts once it is set.
	writeProviderTestFile(t, filepath.Join(home, ".config", "kimchi", "config.json"), `{"apiKey":"kimchi"}`)
	env["KIMCHI_CUSTOM_KEY"] = "custom"
	want := []string{"custom", "kimchi-dev", "kimchi-dev/anthropic", "openai-codex"}
	if got := kimchiConfiguredProviders("", env); !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}

	// KIMCHI_API_KEY alone is enough too.
	if err := os.Remove(filepath.Join(home, ".config", "kimchi", "config.json")); err != nil {
		t.Fatal(err)
	}
	env["KIMCHI_API_KEY"] = "env-key"
	if got := kimchiConfiguredProviders("", env); !reflect.DeepEqual(got, want) {
		t.Fatalf("providers with KIMCHI_API_KEY = %v, want %v", got, want)
	}
}
