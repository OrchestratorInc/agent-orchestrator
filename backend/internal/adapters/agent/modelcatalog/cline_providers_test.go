package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"
)

func TestClineConfiguredProvidersReadsStoredCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"CLINE_API_KEY", "CLINE_DIR", "CLINE_DATA_DIR", "CLINE_PROVIDER_SETTINGS_PATH"} {
		t.Setenv(name, "")
	}
	dir := filepath.Join(home, ".cline", "data", "settings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	expired := strconv.FormatInt(time.Now().Add(-time.Hour).UnixMilli(), 10)
	// An account login with a refresh token and a stored API key count; an
	// expired access token alone, an empty key and a provider with only a
	// model do not.
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(`{
		"lastUsedProvider":"zai-coding-plan",
		"providers":{
			"cline":{"settings":{"provider":"cline","auth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}},
			"zai-coding-plan":{"settings":{"provider":"zai-coding-plan","apiKey":"k","model":"glm-5.2"}},
			"openai-codex":{"settings":{"provider":"openai-codex","auth":{"accessToken":"a","expiresAt":`+expired+`}}},
			"anthropic":{"settings":{"provider":"anthropic","apiKey":"  "}},
			"openrouter":{"settings":{"provider":"openrouter","model":"x"}}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := clineConfiguredProviders("", nil)
	sort.Strings(got)
	if want := []string{"cline", "zai-coding-plan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured providers = %v, want %v", got, want)
	}
}

func TestClineConfiguredProvidersHonorsOverridesAndAPIKeyEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	path := filepath.Join(root, "custom", "providers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"providers":{"deepseek":{"settings":{"apiKey":"k"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLINE_PROVIDER_SETTINGS_PATH", path)
	t.Setenv("CLINE_API_KEY", "from-env")

	got := clineConfiguredProviders("", nil)
	sort.Strings(got)
	if want := []string{"cline", "deepseek"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured providers = %v, want %v", got, want)
	}
	// The discovery env takes precedence over the daemon's own environment.
	if got := clineConfiguredProviders("", map[string]string{"CLINE_API_KEY": ""}); !reflect.DeepEqual(got, []string{"deepseek"}) {
		t.Fatalf("configured providers with env override = %v", got)
	}

	// CLINE_DATA_DIR replaces the data directory under CLINE_DIR.
	t.Setenv("CLINE_PROVIDER_SETTINGS_PATH", "")
	t.Setenv("CLINE_API_KEY", "")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "settings", "providers.json"), []byte(`{"providers":{"groq":{"settings":{"apikey":"k"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLINE_DATA_DIR", dataDir)
	if got := clineConfiguredProviders("", nil); !reflect.DeepEqual(got, []string{"groq"}) {
		t.Fatalf("configured providers from CLINE_DATA_DIR = %v", got)
	}
}
