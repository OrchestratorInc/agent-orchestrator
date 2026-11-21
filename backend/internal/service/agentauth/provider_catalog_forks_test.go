package agentauth

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestKiloCatalogKeyUsesKiloStore(t *testing.T) {
	cache, data := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("KILO_DATA_DIR", data)
	if err := os.MkdirAll(filepath.Join(cache, "kilo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "kilo", "models.json"), []byte(`{"zai":{"id":"zai","name":"Z.AI","env":["ZHIPU_API_KEY"],"models":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("kilo"), nil, nil, t.TempDir())
	if _, err := svc.StartLoginWithOptions(t.Context(), "kilocode", catalogKeyMethod, "kilo-secret", LoginOptions{Provider: "zai"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(data, "auth.json"))
	var auth map[string]struct{ Type, Key string }
	if err != nil || json.Unmarshal(raw, &auth) != nil || auth["zai"].Type != "api" || auth["zai"].Key != "kilo-secret" {
		t.Fatalf("Kilo auth.json = %s, %v", raw, err)
	}
}

func TestMiMoCatalogKeyUsesReportedDataDir(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	cache, data := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := os.MkdirAll(filepath.Join(cache, "mimocode"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "mimocode", "models.json"), []byte(`{"zai-coding-plan":{"id":"zai-coding-plan","name":"Z.AI Coding Plan","env":["ZHIPU_API_KEY"],"models":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("mimo"), nil, nil, t.TempDir())
	svc.commandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) != 2 || args[0] != "debug" || args[1] != "paths" {
			t.Fatalf("unexpected mimo args %v", args)
		}
		return exec.CommandContext(ctx, "/bin/sh", "-c", `printf 'config /x\ndata %s\ncache /y\n' "$0"`, data)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "mimo-code", catalogKeyMethod, "mimo-secret", LoginOptions{Provider: "zai-coding-plan"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(data, "auth.json"))
	var auth map[string]struct{ Type, Key string }
	if err != nil || json.Unmarshal(raw, &auth) != nil || auth["zai-coding-plan"].Key != "mimo-secret" {
		t.Fatalf("MiMo auth.json = %s, %v", raw, err)
	}
}

func TestCrushCatalogKeepsKeyOnlyProvidersAndOtherSettings(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CRUSH_GLOBAL_DATA", "")
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("LOCALAPPDATA", data)
	dir := filepath.Join(data, "crush")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := `[
		{"id":"openrouter","name":"OpenRouter","api_key":"$OPENROUTER_API_KEY","models":[{"id":"or-b"},{"id":"or-a"}]},
		{"id":"openai","name":"OpenAI","api_key":"$OPENAI_API_KEY","models":[{"id":"gpt-5"}]},
		{"id":"azure","name":"Azure OpenAI","api_key":"$AZURE_OPENAI_API_KEY","models":[]},
		{"id":"bedrock","name":"AWS Bedrock","api_key":"","models":[]}
	]`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crush.json"), []byte(`{"recent_models":{"large":"x"},"providers":{"openrouter":{"base_url":"https://example.test"},"openai":{"oauth":{"access_token":"t"},"chatgpt_models":["gpt-5"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("crush"), nil, nil, t.TempDir())
	providers, err := svc.CatalogProviders(t.Context(), "crush")
	var openrouter *CatalogProvider
	for i := range providers {
		if providers[i].ID == "openrouter" {
			openrouter = &providers[i]
		}
	}
	if err != nil || len(providers) != 2 || openrouter == nil || openrouter.Env != "OPENROUTER_API_KEY" || openrouter.Models != 2 {
		t.Fatalf("Crush providers = %+v, %v", providers, err)
	}
	if models, err := svc.CatalogModels(t.Context(), "crush", "openrouter"); err != nil || len(models) != 2 {
		t.Fatalf("Crush models = %v, %v", models, err)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "crush", catalogKeyMethod, "sk-or", LoginOptions{Provider: "openrouter"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "crush.json"))
	var config struct {
		RecentModels map[string]string         `json:"recent_models"`
		Providers    map[string]map[string]any `json:"providers"`
	}
	if err != nil || json.Unmarshal(raw, &config) != nil || config.RecentModels["large"] != "x" ||
		config.Providers["openrouter"]["api_key"] != "sk-or" || config.Providers["openrouter"]["base_url"] != "https://example.test" {
		t.Fatalf("crush.json = %s, %v", raw, err)
	}
	for _, key := range []string{"$(cat ~/.ssh/id_rsa)", "abc`id`def", `abc\def`} {
		if _, err := svc.StartLoginWithOptions(t.Context(), "crush", catalogKeyMethod, key, LoginOptions{Provider: "openrouter"}); err == nil {
			t.Fatalf("accepted %q, which Crush would expand", key)
		}
	}
	// As when Crush saves a key itself, a stored ChatGPT sign-in and its model
	// list go, or a token refresh would replace the key.
	if _, err := svc.StartLoginWithOptions(t.Context(), "crush", catalogKeyMethod, "sk-openai", LoginOptions{Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "crush.json"))
	var saved struct {
		Providers map[string]map[string]json.RawMessage `json:"providers"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.Providers["openai"]["oauth"] != nil || saved.Providers["openai"]["chatgpt_models"] != nil || string(saved.Providers["openai"]["api_key"]) != `"sk-openai"` {
		t.Fatalf("crush.json = %s", raw)
	}
}

func TestCrushFollowsCrushGlobalData(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CRUSH_GLOBAL_DATA", data)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if path, err := crushCatalogPath(); err != nil || path != filepath.Join(data, "providers.json") {
		t.Fatalf("catalog path = %q, %v", path, err)
	}
	if err := saveCrushProviderKey(t.Context(), catalogHarness{}, "openrouter", "sk-or"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "crush.json")); err != nil {
		t.Fatalf("key not saved where Crush reads it: %v", err)
	}
	t.Setenv("CRUSH_GLOBAL_DATA", "relative/crush")
	if _, err := crushDataDir(); err == nil {
		t.Fatal("accepted a relative Crush data directory")
	}
}
