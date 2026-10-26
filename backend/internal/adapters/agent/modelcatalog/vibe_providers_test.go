package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestVibeConfiguredProvidersReadKeyVariables(t *testing.T) {
	home := t.TempDir()
	vibeHome := filepath.Join(home, ".vibe")
	t.Setenv("HOME", home)
	t.Setenv("VIBE_HOME", "")
	t.Setenv("MISTRAL_API_KEY", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	if err := os.MkdirAll(vibeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	// llamacpp needs no key, so nothing proves it is ready; openrouter's
	// variable is unset.
	if err := os.WriteFile(filepath.Join(vibeHome, "config.toml"), []byte(`
[[providers]]
name = "mistral"
api_key_env_var = "MISTRAL_API_KEY"

[[providers]]
name = "llamacpp"
api_key_env_var = ""

[[providers]]
name = "zai"
api_key_env_var = "ZAI_API_KEY"

[[providers]]
name = "openrouter"
api_key_env_var = "OPENROUTER_API_KEY"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vibeHome, ".env"), []byte("ZAI_API_KEY='zai-test'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := vibeConfiguredProviders("", map[string]string{"MISTRAL_API_KEY": "mistral-test"})
	if want := []string{"mistral", "zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}
	// An empty value in the session environment hides the process one.
	t.Setenv("MISTRAL_API_KEY", "mistral-process")
	got = vibeConfiguredProviders("", map[string]string{"MISTRAL_API_KEY": ""})
	if want := []string{"zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}
}

func TestVibeDoesNotMarkEmptyDotenvCredentialsConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VIBE_HOME", "")
	t.Setenv("MISTRAL_API_KEY", "")
	vibeHome := filepath.Join(home, ".vibe")
	writeProviderTestFile(t, filepath.Join(vibeHome, "config.toml"), "[[providers]]\nname = \"mistral\"\napi_key_env_var = \"MISTRAL_API_KEY\"\n")
	for _, body := range []string{"MISTRAL_API_KEY= # paste your key", "MISTRAL_API_KEY='' # unset", "MISTRAL_API_KEY=old\nMISTRAL_API_KEY="} {
		writeProviderTestFile(t, filepath.Join(vibeHome, ".env"), body)
		if got := vibeConfiguredProviders("", nil); len(got) != 0 {
			t.Fatalf("configured = %v for an empty dotenv credential", got)
		}
	}
}
