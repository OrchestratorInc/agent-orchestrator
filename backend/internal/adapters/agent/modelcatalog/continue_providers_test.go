package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestContinueConfiguredProvidersResolvesModelKeys(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	for _, name := range []string{"MISTRAL_KEY", "GEMINI_KEY", "DEEPSEEK_KEY", "MISSING_KEY"} {
		t.Setenv(name, "")
	}
	dir := filepath.Join(home, ".continue")
	if err := os.MkdirAll(filepath.Join(workspace, ".continue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Literal keys, secrets from the environment and from Continue's home and
	// workspace .env files count; an unresolved secret, a keyless local model
	// and an empty key do not.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
name: test
models:
  - name: GLM
    provider: openai
    model: glm-5.2
    apiKey: literal-key
  - name: Sonnet
    provider: anthropic
    model: claude-sonnet-4-6
    apiKey: ${{ secrets.ANTHROPIC_KEY }}
  - name: Codestral
    provider: mistral
    model: codestral
    apiKey: ${{ secrets.acme/assistant/MISTRAL_KEY }}
  - name: Gemini
    provider: gemini
    model: gemini-3-pro
    apiKey: ${{ secrets.GEMINI_KEY }}
  - name: DeepSeek
    provider: deepseek
    model: deepseek-chat
    apiKey: ${{ secrets.MISSING_KEY }}
  - name: Llama
    provider: ollama
    model: llama3
  - name: Empty
    provider: groq
    model: llama-4
    apiKey: ""
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("ANTHROPIC_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".continue", ".env"), []byte("GEMINI_KEY=\"g-test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := continueConfiguredProviders(workspace, map[string]string{"MISTRAL_KEY": "m-test"})
	if want := []string{"openai", "anthropic", "mistral", "gemini"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured providers = %v, want %v", got, want)
	}
	// Without the workspace, its .env is not consulted.
	got = continueConfiguredProviders("", nil)
	if want := []string{"openai", "anthropic"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured providers without workspace = %v, want %v", got, want)
	}
}

func TestContinueConfiguredProvidersHonorsGlobalDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	dir := filepath.Join(root, "continue-home")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`
models:
  - provider: anthropic
    model: claude-opus-4-6
    apiKey: k
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTINUE_GLOBAL_DIR", dir)
	if got := continueConfiguredProviders("", nil); !reflect.DeepEqual(got, []string{"anthropic"}) {
		t.Fatalf("configured providers = %v", got)
	}
}
