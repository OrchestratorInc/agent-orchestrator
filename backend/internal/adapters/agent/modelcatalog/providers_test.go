package modelcatalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func providerTestEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"HOME": t.TempDir(), "PI_CODING_AGENT_DIR": t.TempDir(),
		"OPENCODE_DATA_DIR": t.TempDir(), "XDG_DATA_HOME": t.TempDir(),
		"OPENCODE_API_KEY": "", "COHERE_API_KEY": "",
		"ANTHROPIC_API_KEY": "", "OPENAI_API_KEY": "", "GEMINI_API_KEY": "", "GOOGLE_API_KEY": "",
		"GROQ_API_KEY": "", "XAI_API_KEY": "", "OPENROUTER_API_KEY": "", "MISTRAL_API_KEY": "", "DEEPSEEK_API_KEY": "",
	}
}

func TestConfiguredProvidersPiEvidence(t *testing.T) {
	env := providerTestEnv(t)
	env["OPENAI_API_KEY"] = "present"
	env["CATALOG_TEST_KEY"] = "present"
	env["CATALOG_TEST_MISSING"] = ""
	raw := `{
		"literal":{"type":"api_key","key":"present"},
		"expanded":{"type":"api_key","key":"${CATALOG_TEST_KEY}"},
		"unresolved":{"type":"api_key","key":"${CATALOG_TEST_MISSING}"},
		"command":{"type":"api_key","key":"!touch should-never-exist"},
		"oauth":{"type":"oauth","access":"stored","expires":1},
		"empty":{"type":"api_key","key":""},
		"unsupported":{"type":"unknown","key":"present"}
	}`
	if err := os.WriteFile(filepath.Join(env["PI_CODING_AGENT_DIR"], "auth.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got := configuredProviders(context.Background(), "pi", env)
	want := []string{"expanded", "literal", "oauth", "openai"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
}

func TestConfiguredProvidersOpenCodeScopeAndMalformedStore(t *testing.T) {
	env := providerTestEnv(t)
	path := filepath.Join(env["OPENCODE_DATA_DIR"], "auth.json")
	if err := os.WriteFile(path, []byte(`{"custom":{"type":"api","key":"present"},"empty":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := configuredProviders(context.Background(), "opencode", env); !reflect.DeepEqual(got, []string{"custom"}) {
		t.Fatalf("providers = %v", got)
	}
	// OpenCode 2 must not reuse potentially stale OpenCode 1 credentials.
	if got := configuredProviders(context.Background(), "opencode-v2", env); len(got) != 0 {
		t.Fatalf("v2 providers = %v", got)
	}
	if err := os.WriteFile(path, []byte(`invalid JSON`), 0o600); err != nil {
		t.Fatal(err)
	}
	env["ANTHROPIC_API_KEY"] = "present"
	if got := configuredProviders(context.Background(), "opencode", env); !reflect.DeepEqual(got, []string{"anthropic"}) {
		t.Fatalf("providers = %v", got)
	}
	if got := configuredProviders(context.Background(), "unsupported", env); len(got) != 0 {
		t.Fatalf("unsupported providers = %v", got)
	}
}

func TestConfiguredProvidersOMPDisabledCredentialsDoNotFallBack(t *testing.T) {
	env := providerTestEnv(t)
	dir := env["PI_CODING_AGENT_DIR"]
	db, err := sql.Open("sqlite", filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE auth_credentials (provider TEXT, data TEXT, disabled_cause TEXT);
		INSERT INTO auth_credentials VALUES ('active', '{"key":"present"}', NULL), ('disabled', '{"key":"old"}', 'revoked'), ('empty', '', NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"disabled":{"type":"api_key","key":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := configuredProviders(context.Background(), "omp", env); !reflect.DeepEqual(got, []string{"active"}) {
		t.Fatalf("providers = %v", got)
	}
	if _, err := db.Exec(`DELETE FROM auth_credentials`); err != nil {
		t.Fatal(err)
	}
	if got := configuredProviders(context.Background(), "omp", env); len(got) != 0 {
		t.Fatalf("empty authoritative database providers = %v", got)
	}
}

func TestProviderSelectorsPreserveNativeIDs(t *testing.T) {
	for _, agentID := range []string{"opencode", "opencode-v2", "kilocode", "crush"} {
		models, err := commandSpecs[agentID].parser([]byte("openrouter/meta-llama/llama-4\nanthropic/claude-sonnet\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(models) != 2 {
			t.Fatalf("models = %#v", models)
		}
		for _, model := range models {
			if model.ID == "openrouter/meta-llama/llama-4" && (model.Provider != "openrouter" || model.Label != "meta-llama/llama-4") {
				t.Fatalf("model = %#v", model)
			}
			if model.Provider == "" {
				t.Fatalf("missing provider: %#v", model)
			}
		}
	}
}

func TestJSONModelsInheritProviderWithoutOverwritingExplicitProvider(t *testing.T) {
	models, err := parseJSONModels([]byte(`{"providers":[{"id":"outer","models":[{"id":"native-model"},{"id":"custom-model","provider":"custom"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %#v", models)
	}
	for _, model := range models {
		want := "outer"
		if model.ID == "custom-model" {
			want = "custom"
		}
		if model.Provider != want {
			t.Fatalf("model = %#v, want provider %s", model, want)
		}
	}
}

func TestProviderFingerprintChangesWithCredentialPresence(t *testing.T) {
	env := providerTestEnv(t)
	before := CatalogFingerprint(context.Background(), "pi", "", t.TempDir(), env)
	env["OPENAI_API_KEY"] = "present"
	after := CatalogFingerprint(context.Background(), "pi", "", t.TempDir(), env)
	if before == after {
		t.Fatal("credential presence did not invalidate catalog")
	}
}
