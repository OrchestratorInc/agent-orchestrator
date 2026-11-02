package modelcatalog

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestPrimeAgentConfiguredProvidersReadsAuthStoreAndEnvironment(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "PRIME_AGENT_CODING_AGENT_DIR": "", "PI_CODING_AGENT_DIR": ""}
	for _, names := range primeAgentKeyEnv {
		for _, name := range names {
			env[name] = ""
		}
	}
	for _, name := range []string{"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "GOOGLE_APPLICATION_CREDENTIALS"} {
		env[name] = ""
	}
	dir := filepath.Join(home, ".prime", "agent")
	writeProviderTestFile(t, filepath.Join(dir, "auth.json"), `{"zai":{"type":"api_key","key":"k"},"anthropic":{"type":"oauth"}}`)
	writeProviderTestFile(t, filepath.Join(dir, "models.json"), `{"providers":{"local":{"apiKey":"literal"},"later":{"apiKey":"LATER_KEY"}}}`)
	env["OPENAI_API_KEY"] = "sk"
	env["LATER_KEY"] = ""

	if got, want := primeAgentConfiguredProviders("", env), []string{"local", "openai", "zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}

	// PRIME_AGENT_CODING_AGENT_DIR moves the store; Bedrock follows AWS credentials.
	other := filepath.Join(home, "other")
	writeProviderTestFile(t, filepath.Join(other, "auth.json"), `{"github-copilot":{"type":"oauth","refresh":"r"}}`)
	env["PRIME_AGENT_CODING_AGENT_DIR"] = other
	env["OPENAI_API_KEY"] = ""
	env["AWS_PROFILE"] = "dev"
	if got, want := primeAgentConfiguredProviders("", env), []string{"amazon-bedrock", "github-copilot"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers with agent dir = %v, want %v", got, want)
	}
}

func TestPrimeAgentCredentialsIgnorePiDirectoryAndExpandOwnOverride(t *testing.T) {
	for _, override := range []string{"", "~/profile", "~"} {
		t.Run("override="+override, func(t *testing.T) {
			home := t.TempDir()
			env := map[string]string{"HOME": home, "PRIME_AGENT_CODING_AGENT_DIR": override, "PI_CODING_AGENT_DIR": filepath.Join(home, "pi")}
			for _, names := range primeAgentKeyEnv {
				for _, name := range names {
					env[name] = ""
				}
			}
			for _, name := range []string{"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK",
				"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE",
				"GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "GOOGLE_APPLICATION_CREDENTIALS"} {
				env[name] = ""
			}
			dir := filepath.Join(home, ".prime", "agent")
			switch override {
			case "~/profile":
				dir = filepath.Join(home, "profile")
			case "~":
				dir = home
			}
			writeProviderTestFile(t, filepath.Join(dir, "auth.json"), `{"openai":{"type":"api_key","key":"prime-key"}}`)
			writeProviderTestFile(t, filepath.Join(home, "pi", "auth.json"), `{"anthropic":{"type":"api_key","key":"pi-key"}}`)
			if got, want := primeAgentConfiguredProviders("", env), []string{"openai"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("configured = %v, want %v", got, want)
			}
		})
	}
}
