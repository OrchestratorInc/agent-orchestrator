package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestCrushConfiguredProvidersReadsConfigAndProviderKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range crushProviderKeyEnv {
		t.Setenv(name, "")
	}
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "CRUSH_GLOBAL_CONFIG", "CRUSH_GLOBAL_DATA",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GROQ_API_KEY", "MY_ZAI_KEY", "AWS_PROFILE", "AWS_DEFAULT_PROFILE",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "VERTEXAI_PROJECT", "VERTEXAI_LOCATION"} {
		t.Setenv(name, "")
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(home, ".local", "share", "crush")
	write(filepath.Join(data, "providers.json"), `[
		{"id":"anthropic","type":"anthropic","api_key":"$ANTHROPIC_API_KEY"},
		{"id":"openai","type":"openai","api_key":"$OPENAI_API_KEY"},
		{"id":"groq","type":"openai-compat","api_key":"${GROQ_API_KEY}"},
		{"id":"bedrock","type":"bedrock"}
	]`)
	// A literal key, a key read from another variable, a command that is not
	// run, a stored OAuth token, and a disabled provider whose key is set.
	write(filepath.Join(home, ".config", "crush", "crush.json"), `{"providers":{
		"custom":{"api_key":"sk-literal"},
		"zai":{"api_key":"$MY_ZAI_KEY"},
		"cmd":{"api_key":"$(security find-generic-password -w)"},
		"groq":{"disable":true}
	}}`)
	write(filepath.Join(data, "crush.json"), `{"providers":{"copilot":{"oauth":{"access_token":"x"}}}}`)
	env := map[string]string{"OPENAI_API_KEY": "sk-test", "MY_ZAI_KEY": "zai-test", "GROQ_API_KEY": "gsk-test"}

	got := crushConfiguredProviders("", env)
	sort.Strings(got)
	if want := []string{"copilot", "custom", "openai", "zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}

	// Bedrock needs the AWS credentials Crush itself looks for.
	env["AWS_PROFILE"] = "work"
	got = crushConfiguredProviders("", env)
	sort.Strings(got)
	if want := []string{"bedrock", "copilot", "custom", "openai", "zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}
}

func TestCrushConfiguredProvidersFallsBackToBundledKeysAndHonorsDirOverrides(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	for _, name := range crushProviderKeyEnv {
		t.Setenv(name, "")
	}
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "crush.json"), []byte(`{"providers":{"zai":{"disable":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"CRUSH_GLOBAL_CONFIG": configDir, "CRUSH_GLOBAL_DATA": filepath.Join(root, "data"),
		"ANTHROPIC_API_KEY": "sk-ant", "ZAI_API_KEY": "zai",
	}
	if got, want := crushConfiguredProviders("", env), []string{"anthropic"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("configured = %v, want %v", got, want)
	}
}
