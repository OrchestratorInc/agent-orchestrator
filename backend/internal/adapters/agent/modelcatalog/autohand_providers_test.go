package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAutohandConfiguredProvidersReadKeyedSections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AZURE_OPENAI_KEY", "")
	dir := filepath.Join(home, ".autohand")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// api holds Autohand's own company secret, not a model provider; ollama
	// needs no key; openrouter's key is blank.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{
		"provider":"zai",
		"api":{"baseUrl":"https://api.autohand.ai","companySecret":"secret"},
		"zai":{"apiKey":"zai-test","model":"glm-5.1"},
		"deepseek":{"apiKey":"ds-test"},
		"openrouter":{"apiKey":"  "},
		"ollama":{"model":"qwen3"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := autohandConfiguredProviders("", nil)
	if want := []string{"zai", "deepseek"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}
	got = autohandConfiguredProviders("", map[string]string{"AZURE_OPENAI_KEY": "azure-test"})
	if want := []string{"zai", "deepseek", "azure"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}
}
