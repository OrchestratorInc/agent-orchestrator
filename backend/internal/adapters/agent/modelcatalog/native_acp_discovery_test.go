package modelcatalog

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNativeACPConfigurationContentChangesFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for agent, folder := range map[string]string{"copilot": ".copilot", "droid": ".factory"} {
		t.Run(agent, func(t *testing.T) {
			path := filepath.Join(home, folder, "settings.json")
			writeConfig(t, path, `{"model":"same","customModels":["one"]}`)
			before := CatalogFingerprint(context.Background(), agent, "", "", nil)
			writeConfig(t, path, `{"model":"same","customModels":["two"]}`)
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
	path := filepath.Join(dir, "credentials", "kimi-code.json")
	writeConfig(t, path, `{"access_token":"one"}`)
	before := CatalogFingerprint(context.Background(), "kimi", "", "", env)
	writeConfig(t, path, `{"access_token":"two"}`)
	if CatalogFingerprint(context.Background(), "kimi", "", "", env) == before {
		t.Fatal("credential change ignored")
	}
}

func TestGeminiNativeSettingsInvalidateCatalog(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "GEMINI_CLI_SYSTEM_SETTINGS_PATH": filepath.Join(home, "system.json"), "GEMINI_CLI_SYSTEM_DEFAULTS_PATH": filepath.Join(home, "defaults.json")}
	requestDir := t.TempDir()
	for _, path := range modelConfigPaths("gemini", requestDir, env) {
		before := CatalogFingerprint(context.Background(), "gemini", "", requestDir, env)
		writeConfig(t, path, `{"changed":"one"}`)
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
