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
	t.Setenv("XDG_DATA_HOME", data)
	dir := filepath.Join(data, "crush")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := `[
		{"id":"openrouter","name":"OpenRouter","api_key":"$OPENROUTER_API_KEY","models":[{},{}]},
		{"id":"azure","name":"Azure OpenAI","api_key":"$AZURE_OPENAI_API_KEY","models":[]},
		{"id":"bedrock","name":"AWS Bedrock","api_key":"","models":[]}
	]`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crush.json"), []byte(`{"recent_models":{"large":"x"},"providers":{"openrouter":{"base_url":"https://example.test"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewWithAgentResolver(foundExecutable("crush"), nil, nil, t.TempDir())
	providers, err := svc.CatalogProviders(t.Context(), "crush")
	if err != nil || len(providers) != 1 || providers[0].ID != "openrouter" || providers[0].Env != "OPENROUTER_API_KEY" || providers[0].Models != 2 {
		t.Fatalf("Crush providers = %+v, %v", providers, err)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "crush", catalogKeyMethod, "sk-or", LoginOptions{Provider: "openrouter"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "crush.json"))
	var config struct {
		RecentModels map[string]string            `json:"recent_models"`
		Providers    map[string]map[string]string `json:"providers"`
	}
	if err != nil || json.Unmarshal(raw, &config) != nil || config.RecentModels["large"] != "x" ||
		config.Providers["openrouter"]["api_key"] != "sk-or" || config.Providers["openrouter"]["base_url"] != "https://example.test" {
		t.Fatalf("crush.json = %s, %v", raw, err)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "crush", catalogKeyMethod, "$(cat ~/.ssh/id_rsa)", LoginOptions{Provider: "openrouter"}); err == nil {
		t.Fatal("accepted a key Crush would expand")
	}
}
