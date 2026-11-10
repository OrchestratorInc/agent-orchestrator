package modelcatalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// writeOMPScript writes a stand-in `omp` binary printing contents as its
// `models --json` output.
func writeOMPScript(t *testing.T, root, contents string) string {
	t.Helper()
	binary := filepath.Join(root, "omp")
	script := "#!/bin/sh\ncat \"$(dirname \"$0\")/catalog.json\"\n"
	if runtime.GOOS == "windows" {
		binary += ".cmd"
		script = "@echo off\r\ntype \"%~dp0catalog.json\"\r\n"
	}
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return binary
}

func writeOMPModelCache(t *testing.T, dir string, providers map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "models.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE model_cache (provider_id TEXT, models TEXT)`); err != nil {
		t.Fatal(err)
	}
	for provider, models := range providers {
		if _, err := db.Exec(`INSERT INTO model_cache VALUES (?, ?)`, provider, models); err != nil {
			t.Fatal(err)
		}
	}
}

func writeOMPAuthDatabase(t *testing.T, dir string, rows string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statement := `CREATE TABLE auth_credentials (provider TEXT, data TEXT, disabled_cause TEXT);` + rows
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

// TestOMPCatalogListsOnlyConfiguredProviders 验证 OMP 只列出本机已启用的供应商：models.yml
// 声明的 API-key 供应商、agent.db 中有效的登录，以及原生命令当前能运行的供应商；models.dev
// 全量缓存中的其余供应商不进入浏览目录。
func TestOMPCatalogListsOnlyConfiguredProviders(t *testing.T) {
	env := providerTestEnv(t)
	dir := env["PI_CODING_AGENT_DIR"]
	writeOMPModelCache(t, dir, map[string]string{
		// OMP's build ships this snapshot for every provider it knows.
		"codegpt": `[{"id":"cn:deepseek-v4.1-flash","provider":"codegpt","name":"DeepSeek V4.1 Flash"}]`,
		"vst110":  `[{"id":"gpt-6-astra","provider":"vst110","name":"GPT-6 Astra"}]`,
		// Cached for a provider this machine only has a database login for, and
		// one it has nothing configured for.
		"anthropic": `[{"id":"claude-sonnet","provider":"anthropic","name":"Claude Sonnet"}]`,
		"nanogpt":   `[{"id":"org/cached-only","provider":"nanogpt","name":"Cached only"}]`,
	})
	writeOMPAuthDatabase(t, dir, `INSERT INTO auth_credentials VALUES
		('anthropic', '{"key":"present"}', NULL),
		('disabled', '{"key":"old"}', 'revoked');`)
	if err := os.WriteFile(filepath.Join(dir, "models.yml"), []byte("providers:\n  codegpt:\n    baseUrl: https://example.test\n    apiKey: secret\n  vst110:\n    baseUrl: https://example.test\n    apiKey: secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := writeOMPScript(t, root, `{"models":[{
"provider":"codegpt","id":"cn:deepseek-v4.1-flash",
"selector":"codegpt/cn:deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash"}]}`)

	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "omp", Binary: binary, WorkingDir: root, Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	providers := map[string]bool{}
	for _, model := range catalog.Models {
		providers[model.Provider] = true
	}
	if !reflect.DeepEqual(providers, map[string]bool{"anthropic": true, "codegpt": true, "vst110": true}) {
		t.Fatalf("providers = %v, want the configured and native providers only", providers)
	}
	// The configured set is what marks providers in Settings; a disabled login
	// is not one of them.
	if want := []string{"anthropic", "codegpt", "vst110"}; !reflect.DeepEqual(catalog.ConfiguredProviders, want) {
		t.Fatalf("configured = %v, want %v", catalog.ConfiguredProviders, want)
	}
	// Only the native command's output is runnable; the cache is browse-only.
	if want := []string{"codegpt/cn:deepseek-v4.1-flash"}; !reflect.DeepEqual(catalog.RunnableModels, want) {
		t.Fatalf("runnable models = %v, want %v", catalog.RunnableModels, want)
	}
}

func TestOMPCatalogKeepsEveryProviderWhenNothingIsConfigured(t *testing.T) {
	env := providerTestEnv(t)
	dir := env["PI_CODING_AGENT_DIR"]
	writeOMPModelCache(t, dir, map[string]string{
		"cached": `[{"id":"org/model","provider":"cached","name":"Cached"}]`,
	})
	root := t.TempDir()
	binary := writeOMPScript(t, root, `{"models":[]}`)

	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "omp", Binary: binary, WorkingDir: root, Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].Provider != "cached" {
		t.Fatalf("catalog = %#v, want the cached provider when no configuration exists", catalog.Models)
	}
}

func TestOMPDeclaredProvidersReadsNamesOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yml")
	if err := os.WriteFile(path, []byte(`
providers:
  vst110:
    baseUrl: https://example.test
    apiKey: secret
    modelOverrides:
      gpt-6-astra:
        reasoning: true
  codegpt:
    apiKey: "!secret-command"
  " ":
    apiKey: ignored
`), 0o600); err != nil {
		t.Fatal(err)
	}
	providers, ok := ompDeclaredProviders(path)
	if !ok || !reflect.DeepEqual(providers, []string{"codegpt", "vst110"}) {
		t.Fatalf("declared = %v, %t; want the two named providers", providers, ok)
	}
	if _, ok := ompDeclaredProviders(filepath.Join(dir, "missing.yml")); ok {
		t.Fatal("a missing models.yml reported a provider set")
	}
}

func TestOMPFingerprintTracksEnabledProviderEdits(t *testing.T) {
	env := providerTestEnv(t)
	dir := env["PI_CODING_AGENT_DIR"]
	path := filepath.Join(dir, "models.yml")
	if err := os.WriteFile(path, []byte("providers:\n  codegpt:\n    apiKey: one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := providerCatalogFingerprint("omp", env)
	// OMP rewrites models.db on its own schedule; only a configuration edit may
	// change which providers the catalog lists.
	if err := os.WriteFile(path, []byte("providers:\n  codegpt:\n    apiKey: two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if providerCatalogFingerprint("omp", env) == before {
		t.Fatal("models.yml edit did not change the fingerprint")
	}
}
