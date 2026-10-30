package modelcatalog

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestKimiConfiguredProvidersReadsProvidersAndManagedLogin(t *testing.T) {
	home := t.TempDir()
	codeHome := filepath.Join(home, ".kimi-code")
	writeProviderTestFile(t, filepath.Join(codeHome, "config.toml"), `
[providers.zai-coding-plan]
type = "openai"
api_key = "zai"

[providers."managed:kimi-code"]
type = "kimi"
api_key = ""

[providers."managed:kimi-code".oauth]
storage = "file"
key = "oauth/kimi-code"

[providers.moonshot]
type = "kimi"
env = { MOONSHOT_API_KEY = "m" }

[providers.unset]
type = "openai"
api_key = ""
`)
	env := map[string]string{"HOME": home, "KIMI_SHARE_DIR": "", "KIMI_CODE_HOME": ""}

	if got, want := kimiConfiguredProviders("", env), []string{"moonshot", "zai-coding-plan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers without login = %v, want %v", got, want)
	}

	writeProviderTestFile(t, filepath.Join(codeHome, "credentials", "kimi-code.json"), `{"refresh_token":"r"}`)
	if got, want := kimiConfiguredProviders("", env), []string{"managed:kimi-code", "moonshot", "zai-coding-plan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}

	// KIMI_SHARE_DIR replaces ~/.kimi; its config.json is read as well.
	share := filepath.Join(home, "share")
	writeProviderTestFile(t, filepath.Join(share, "config.json"), `{"providers":{"kimi-cli":{"api_key":"k"}}}`)
	env["KIMI_SHARE_DIR"] = share
	if got, want := kimiConfiguredProviders("", env), []string{"kimi-cli", "managed:kimi-code", "moonshot", "zai-coding-plan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers with share dir = %v, want %v", got, want)
	}
}
