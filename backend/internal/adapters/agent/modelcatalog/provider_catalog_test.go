package modelcatalog

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProviderSnapshotIncludesUnconfiguredAndEmptyProviders(t *testing.T) {
	catalog := parseProviderSnapshot([]byte(`{
  "configured":{"models":{"native/model":{"id":"native/model","name":"Model","limit":{"context":200000}}}},
  "unconfigured":{"models":[{"id":"same","input":["text","image"]}]},
  "local":{"models":{}},
  "custom":{"apiKey":"never-export","models":[{"id":"same"}]}
 }`), false)
	if !reflect.DeepEqual(catalog.Providers, []string{"configured", "custom", "local", "unconfigured"}) {
		t.Fatalf("providers = %v", catalog.Providers)
	}
	models := normalize(catalog.Models)
	if len(models) != 3 {
		t.Fatalf("models = %#v", models)
	}
	for _, m := range models {
		if m.Provider == "configured" && (m.ID != "configured/native/model" || m.ContextWindow != 200000) {
			t.Fatalf("native selector/metadata changed: %#v", m)
		}
	}
	if len(catalog.ConfiguredProviders) != 0 {
		t.Fatal("catalog presence must not imply credentials")
	}
}

func TestLocalProviderCatalogReadsEachHarnessOwnCache(t *testing.T) {
	for _, agentID := range []string{"opencode", "kilocode", "mimo-code"} {
		t.Run(agentID, func(t *testing.T) {
			env := providerTestEnv(t)
			env["XDG_CACHE_HOME"] = t.TempDir()
			path := providerCachePath(agentID, env)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			before := providerCatalogFingerprint(agentID, env)
			raw := `{"unc":{"env":["TEST_PROVIDER_KEY"],"models":{"org/model":{"name":"Model"}}},"empty":{"models":{}}}`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			env["TEST_PROVIDER_KEY"] = ""
			catalog := localProviderCatalog(context.Background(), agentID, "", "", env)
			if len(catalog.Models) != 1 || catalog.Models[0].ID != "unc/org/model" || len(catalog.Providers) != 2 {
				t.Fatalf("catalog = %#v", catalog)
			}
			if len(configuredProviders(context.Background(), agentID, env)) != 0 {
				t.Fatal("unconfigured provider marked configured")
			}
			env["TEST_PROVIDER_KEY"] = "present"
			if got := configuredProviders(context.Background(), agentID, env); !reflect.DeepEqual(got, []string{"unc"}) {
				t.Fatalf("configured = %v", got)
			}
			if before == providerCatalogFingerprint(agentID, env) {
				t.Fatal("cache change did not invalidate catalog")
			}
		})
	}
}

func TestPiCatalogFingerprintIgnoresRewritesWithSameContents(t *testing.T) {
	for _, agentID := range []string{"pi", "kimchi"} {
		t.Run(agentID, func(t *testing.T) {
			env := providerTestEnv(t)
			dir := piCatalogDir(agentID, env)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "models.json")
			write := func(raw string, at time.Time) {
				t.Helper()
				if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, at, at); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now().Add(-time.Hour)
			write(`{"providers":{}}`, start)
			before := providerCatalogFingerprint(agentID, env)
			// --list-models rewrites the file with the same contents.
			write(`{"providers":{}}`, start.Add(time.Minute))
			if got := providerCatalogFingerprint(agentID, env); got != before {
				t.Fatalf("same contents changed the fingerprint: %q -> %q", before, got)
			}
			write(`{"providers":{"added":{}}}`, start.Add(2*time.Minute))
			if providerCatalogFingerprint(agentID, env) == before {
				t.Fatal("changed contents did not change the fingerprint")
			}
		})
	}
}

func TestOMPProviderCacheUsesModelProviderNotAccountCacheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE model_cache (provider_id TEXT, models TEXT);
 INSERT INTO model_cache VALUES ('router:account-private', '[{"id":"org/model","provider":"router","name":"Model"}]');`)
	if err != nil {
		t.Fatal(err)
	}
	catalog := ompProviderCatalog(context.Background(), path)
	if len(catalog.Models) != 1 || catalog.Models[0].Provider != "router" || catalog.Models[0].ID != "router/org/model" {
		t.Fatalf("catalog = %#v", catalog)
	}
	if len(catalog.ConfiguredProviders) != 0 {
		t.Fatal("cached catalog isn't credential evidence")
	}
}

// TestOMPProviderCacheReadsThinkingEfforts uses the record shape OMP writes to
// models.db: reasoning models list their levels under thinking.efforts.
func TestOMPProviderCacheReadsThinkingEfforts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE model_cache (provider_id TEXT, models TEXT);
 INSERT INTO model_cache VALUES ('anthropic', '[
  {"id":"claude-opus-5-5","provider":"anthropic","reasoning":true,"thinking":{"mode":"anthropic-adaptive","efforts":["low","medium","high","xhigh","max"]}},
  {"id":"gpt-6.1-sol","provider":"openai","reasoning":true,"thinking":{"mode":"effort","efforts":["minimal","low","auto","high","high"]}},
  {"id":"plain","provider":"openai","reasoning":false,"thinking":{"efforts":["low"]}},
  {"id":"unlisted","provider":"openai","reasoning":true}
 ]');`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"anthropic/claude-opus-5-5": {"low", "medium", "high", "xhigh", "max"},
		"openai/gpt-6.1-sol":        {"minimal", "low", "high"},
		"openai/plain":              nil,
		"openai/unlisted":           nil,
	}
	catalog := ompProviderCatalog(context.Background(), path)
	if len(catalog.Models) != len(want) {
		t.Fatalf("catalog = %#v", catalog)
	}
	for _, model := range catalog.Models {
		if !reflect.DeepEqual(model.Efforts, want[model.ID]) {
			t.Errorf("%s efforts = %v, want %v", model.ID, model.Efforts, want[model.ID])
		}
	}
}

// TestOMPCatalogKeyReplacesSnapshotsWithoutThinkingEfforts keeps catalogs saved
// before OMP's efforts were read from serving levelless models.
func TestOMPCatalogKeyReplacesSnapshotsWithoutThinkingEfforts(t *testing.T) {
	home := t.TempDir()
	key := (Discoverer{}).CatalogFingerprint(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "omp", WorkingDir: home, Env: map[string]string{"HOME": home},
	})
	if !strings.HasSuffix(key, ":thinking-v1") {
		t.Errorf("omp catalog key = %q, want a thinking-v1 suffix", key)
	}
}

func TestProviderCatalogMissingOrInvalidCacheKeepsDiscoveryUsable(t *testing.T) {
	env := providerTestEnv(t)
	env["XDG_CACHE_HOME"] = t.TempDir()
	for _, agentID := range []string{"opencode", "kilocode", "mimo-code", "omp", "pi", "kimchi", "opencode-v2", "crush"} {
		got := localProviderCatalog(context.Background(), agentID, "", "", env)
		if len(got.Models) != 0 || len(got.Providers) != 0 {
			t.Fatalf("%s: %#v", agentID, got)
		}
	}
	if got := parseProviderSnapshot([]byte(`not json`), false); len(got.Models) != 0 || len(got.Providers) != 0 {
		t.Fatalf("invalid cache: %#v", got)
	}
}

func TestPiCustomProvidersVisibleWithoutCredentials(t *testing.T) {
	env := providerTestEnv(t)
	raw := `{"providers":{"local":{"models":[{"id":"org/model","name":"Local"}]},"empty":{"models":[]}}}`
	// Kimchi keeps its Pi runtime in its own harness directory, whatever
	// PI_CODING_AGENT_DIR says.
	kimchiDir := filepath.Join(env["HOME"], ".config", "kimchi", "harness")
	if err := os.MkdirAll(kimchiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{env["PI_CODING_AGENT_DIR"], kimchiDir} {
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := localProviderCatalog(context.Background(), "kimchi", "", "", map[string]string{"HOME": t.TempDir(), "PI_CODING_AGENT_DIR": env["PI_CODING_AGENT_DIR"]}); len(got.Providers) != 0 {
		t.Fatalf("kimchi read Pi's directory: %#v", got)
	}
	for _, agentID := range []string{"pi", "kimchi"} {
		got := localProviderCatalog(context.Background(), agentID, "", "", env)
		if len(got.Providers) != 2 || len(got.Models) != 1 || got.Models[0].ID != "local/org/model" {
			t.Fatalf("%s: %#v", agentID, got)
		}
	}
}

func TestCatalogPlaceholderNeverMarksProviderConfigured(t *testing.T) {
	env := providerTestEnv(t)
	env["XDG_CACHE_HOME"] = t.TempDir()
	env["ANTHROPIC_API_KEY"] = modelDiscoveryPresenceValue
	if got := configuredProviders(context.Background(), "opencode", env); len(got) != 0 {
		t.Fatalf("placeholder configured: %v", got)
	}
}

func TestReportedJSONProvidersKeepsEmptyContainers(t *testing.T) {
	got := reportedJSONProviders([]byte(`{"providers":[{"id":"empty","models":[]},{"id":"reported","models":[{"id":"native-model"}]}]}`))
	if !reflect.DeepEqual(got, []string{"empty", "reported"}) {
		t.Fatalf("providers = %v", got)
	}
}

func TestPiInstalledCatalogUsesItsOwnPackageWithoutCredentials(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	root := t.TempDir()
	files := map[string]string{
		"pi/package.json": `{"name":"@mariozechner/pi-coding-agent","dependencies":{"@mariozechner/pi-ai":"1"}}`,
		"pi/dist/cli.js":  "",
		"pi/node_modules/@mariozechner/pi-ai/package.json":             `{"name":"@mariozechner/pi-ai","type":"module","main":"dist/index.js"}`,
		"pi/node_modules/@mariozechner/pi-ai/dist/index.js":            "throw new Error('do not initialize the SDK');",
		"pi/node_modules/@mariozechner/pi-ai/dist/models.generated.js": `export const MODELS = {custom:{model:{id:"org/model",name:"Native",provider:"custom",input:["text"]}}};`,
		// An unrelated newer global package must not take precedence over Pi's own dependency.
		"pi/node_modules/@earendil-works/pi-ai/package.json":             `{"name":"@earendil-works/pi-ai","type":"module","main":"dist/index.js"}`,
		"pi/node_modules/@earendil-works/pi-ai/dist/index.js":            "",
		"pi/node_modules/@earendil-works/pi-ai/dist/models.generated.js": `export const MODELS = {wrong:{model:{id:"wrong",provider:"wrong"}}};`,
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	models := piInstalledModels(context.Background(), filepath.Join(root, "pi/dist/cli.js"), root, nil)
	if len(models) != 1 || models[0].ID != "custom/org/model" {
		t.Fatalf("models = %#v", models)
	}
}

func TestSharedCatalogIncludesNativeCacheButCredentialScopeDoesNot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a shell script")
	}
	env := providerTestEnv(t)
	env["ANTHROPIC_API_KEY"] = "test-key"
	env["XDG_CACHE_HOME"] = t.TempDir()
	path := providerCachePath("opencode", env)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"unc":{"models":{"org/model":{"name":"Model"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'anthropic/claude-sonnet\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := ports.AgentModelDiscoveryRequest{AgentID: "opencode", Binary: binary, WorkingDir: t.TempDir(), Env: env}
	task, err := (Discoverer{}).Discover(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Models) != 2 {
		t.Fatalf("shared catalog = %#v, want native output and the cached provider", task.Models)
	}
	if !reflect.DeepEqual(task.ConfiguredProviders, []string{"anthropic"}) {
		t.Fatalf("shared catalog configured providers = %v", task.ConfiguredProviders)
	}
	// OpenCode lists what it can run; the cached provider is there to browse.
	if !reflect.DeepEqual(task.RunnableModels, []string{"anthropic/claude-sonnet"}) {
		t.Fatalf("shared catalog runnable models = %v, want only what OpenCode listed", task.RunnableModels)
	}
	sharedFingerprint := (Discoverer{}).CatalogFingerprint(context.Background(), request)
	request.CredentialType = "anthropic_api_key"
	scoped, err := (Discoverer{}).Discover(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Models) != 1 || scoped.Models[0].ID != "anthropic/claude-sonnet" {
		t.Fatalf("credential catalog = %#v, want no local-cache expansion", scoped.Models)
	}
	if sharedFingerprint == (Discoverer{}).CatalogFingerprint(context.Background(), request) {
		t.Fatal("credential and shared catalogs have the same fingerprint")
	}
}

func TestPiInstalledCatalogReadsImportOnlyPiAIAndPrimeAgentFork(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	// Current pi-ai exports only an "import" condition, which require-style
	// resolution rejects; Prime Agent ships its fork as "prime-agent-ai".
	importOnly := `"type":"module","exports":{".":{"types":"./dist/index.d.ts","import":"./dist/index.js"}}`
	for owner, aiName := range map[string]string{
		"@earendil-works/pi-coding-agent": "@earendil-works/pi-ai",
		"prime-agent":                     "prime-agent-ai",
	} {
		root := t.TempDir()
		files := map[string]string{
			"pkg/package.json":       `{"name":"` + owner + `","dependencies":{"@earendil-works/pi-ai":"1"}}`,
			"pkg/dist/bundle/cli.js": "",
			"pkg/node_modules/@earendil-works/pi-ai/package.json":             `{"name":"` + aiName + `",` + importOnly + `}`,
			"pkg/node_modules/@earendil-works/pi-ai/dist/index.js":            "throw new Error('do not initialize the SDK');",
			"pkg/node_modules/@earendil-works/pi-ai/dist/models.generated.js": `export const MODELS = {zai:{m:{id:"glm-5",name:"GLM-5",provider:"zai"}},radius:{}};`,
		}
		for name, contents := range files {
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		models := piInstalledModels(context.Background(), filepath.Join(root, "pkg/dist/bundle/cli.js"), root, nil)
		if len(models) != 1 || models[0].ID != "zai/glm-5" {
			t.Fatalf("%s models = %#v", owner, models)
		}
	}
}

// TestPiModelRecordsCarryTheirThinkingLevels checks the levels match pi-ai's
// getSupportedThinkingLevels: null drops a level, xhigh needs naming, and a
// reasoning model without a map gets minimal-high only under Pi's rules.
func TestPiModelRecordsCarryTheirThinkingLevels(t *testing.T) {
	raw := []byte(`[
		{"id":"gpt-6.1-sol","reasoning":true,"thinkingLevelMap":{"off":null,"minimal":"low","max":"max"}},
		{"id":"spark","reasoning":true,"thinkingLevelMap":{"xhigh":"xhigh","low":null}},
		{"id":"plain","reasoning":false,"thinkingLevelMap":{}},
		{"id":"unmapped","reasoning":true,"thinking":{"efforts":["max"]}}
	]`)
	for _, piRules := range []bool{true, false} {
		unmapped := []string(nil)
		if piRules {
			unmapped = []string{"minimal", "low", "medium", "high"}
		}
		want := map[string][]string{
			"openai-codex/gpt-6.1-sol": {"minimal", "low", "medium", "high"},
			"openai-codex/spark":       {"minimal", "medium", "high", "xhigh"},
			"openai-codex/plain":       nil,
			"openai-codex/unmapped":    unmapped,
		}
		models := parseProviderModelRecords(raw, "openai-codex", func(record map[string]any) []string {
			return piThinkingLevels(record, piRules)
		})
		if len(models) != len(want) {
			t.Fatalf("piRules=%v models = %#v", piRules, models)
		}
		for _, model := range models {
			if !reflect.DeepEqual(model.Efforts, want[model.ID]) {
				t.Errorf("piRules=%v %s efforts = %v, want %v", piRules, model.ID, model.Efforts, want[model.ID])
			}
		}
	}
}

// TestPiCatalogKeysReplaceSnapshotsWithoutThinkingLevels keeps catalogs saved
// before discovery kept Pi's thinking metadata from serving levelless models.
func TestPiCatalogKeysReplaceSnapshotsWithoutThinkingLevels(t *testing.T) {
	for _, agentID := range []string{"pi", "prime-agent"} {
		home := t.TempDir()
		key := (Discoverer{}).CatalogFingerprint(context.Background(), ports.AgentModelDiscoveryRequest{
			AgentID: agentID, WorkingDir: home, Env: map[string]string{"HOME": home},
		})
		if !strings.HasSuffix(key, ":thinking-v3") {
			t.Errorf("%s catalog key = %q, want a thinking-v3 suffix", agentID, key)
		}
	}
}
