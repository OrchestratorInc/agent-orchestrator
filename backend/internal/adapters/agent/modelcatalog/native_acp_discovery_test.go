package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeACPConfigurationContentChangesFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, agent := range []string{"copilot", "droid"} {
		t.Run(agent, func(t *testing.T) {
			root := filepath.Join(home, ".factory")
			if agent == "copilot" {
				root = filepath.Join(home, ".copilot")
			}
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "settings.json")
			if err := os.WriteFile(path, []byte(`{"model":"same","customModels":["one"]}`), 0600); err != nil {
				t.Fatal(err)
			}
			before := CatalogFingerprint(context.Background(), agent, "", "", nil)
			if err := os.WriteFile(path, []byte(`{"model":"same","customModels":["two"]}`), 0600); err != nil {
				t.Fatal(err)
			}
			if CatalogFingerprint(context.Background(), agent, "", "", nil) == before {
				t.Fatal("native config change ignored")
			}
		})
	}
}

func TestKimiNativeCredentialContentChangesFingerprint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	env := map[string]string{"KIMI_SHARE_DIR": dir}
	credentialDir := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(credentialDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(credentialDir, "kimi-code.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"one"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := CatalogFingerprint(context.Background(), "kimi", "", "", env)
	if err := os.WriteFile(path, []byte(`{"access_token":"two"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if CatalogFingerprint(context.Background(), "kimi", "", "", env) == before {
		t.Fatal("credential change ignored")
	}
}

func TestGeminiNativeSettingsInvalidateCatalog(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "GEMINI_CLI_SYSTEM_SETTINGS_PATH": filepath.Join(home, "system.json"), "GEMINI_CLI_SYSTEM_DEFAULTS_PATH": filepath.Join(home, "defaults.json")}
	requestDir := t.TempDir()
	for _, path := range modelConfigPaths("gemini", requestDir, env) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		before := CatalogFingerprint(context.Background(), "gemini", "", requestDir, env)
		if err := os.WriteFile(path, []byte(`{"changed":"one"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if before == CatalogFingerprint(context.Background(), "gemini", "", requestDir, env) {
			t.Fatalf("change ignored: %s", path)
		}
	}
}

func TestCopilotNativeProviderTypeChangesFingerprint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_PROVIDER_TYPE", "openai")
	before := CatalogFingerprint(context.Background(), "copilot", "", "", nil)
	t.Setenv("COPILOT_PROVIDER_TYPE", "anthropic")
	if CatalogFingerprint(context.Background(), "copilot", "", "", nil) == before {
		t.Fatal("provider type change ignored")
	}
	if CatalogFingerprint(context.Background(), "copilot", "", "", map[string]string{"COPILOT_PROVIDER_TYPE": "openai"}) != before {
		t.Fatal("explicit provider type did not override process")
	}
}
