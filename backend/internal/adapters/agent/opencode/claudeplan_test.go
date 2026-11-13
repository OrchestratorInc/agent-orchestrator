package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func isolateClaudePlanEnv(t *testing.T) (dataHome string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("OPENCODE_DATA_DIR", "")
	t.Setenv(opencodeConfigEnvVar, "")
	return filepath.Join(root, "data")
}

func writeClaudePlanFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudePlanPluginsFollowStoredOAuthLoginInAuthJSON(t *testing.T) {
	dataHome := isolateClaudePlanEnv(t)
	authPath := filepath.Join(dataHome, "opencode", "auth.json")

	writeClaudePlanFile(t, authPath, `{"anthropic":{"type":"api","key":"sk-ant-api"}}`)
	if got := ClaudePlanPlugins(context.Background(), 1, ""); got != nil {
		t.Fatalf("API key login added %q, want no plugin", got)
	}

	writeClaudePlanFile(t, authPath, `{"anthropic":{"type":"oauth","access":"a","refresh":"r"}}`)
	if got := ClaudePlanPlugins(context.Background(), 1, ""); !slices.Equal(got, []string{ClaudePlanPluginV1}) {
		t.Fatalf("OpenCode 1 plugins = %q, want %q", got, ClaudePlanPluginV1)
	}
}

func TestClaudePlanPluginsReadOpenCode2CredentialTable(t *testing.T) {
	isolateClaudePlanEnv(t)
	v2Home := filepath.Join(t.TempDir(), "opencode-v2-home")
	dbPath := filepath.Join(v2Home, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE credential (id text PRIMARY KEY, integration_id text, label text NOT NULL, value text NOT NULL, connector_id text, method_id text, active integer, time_created integer NOT NULL, time_updated integer NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO credential VALUES ('c1', 'anthropic', 'Claude Max', '{"type":"key","key":"k"}', NULL, NULL, 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if got := ClaudePlanPlugins(context.Background(), 2, v2Home); got != nil {
		t.Fatalf("API key credential added %q, want no plugin", got)
	}
	if _, err := db.Exec(`INSERT INTO credential VALUES ('c2', 'anthropic', 'Claude Max', '{"type":"oauth","access":"a"}', NULL, NULL, 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if got := ClaudePlanPlugins(context.Background(), 2, v2Home); !slices.Equal(got, []string{ClaudePlanPluginV2}) {
		t.Fatalf("OpenCode 2 plugins = %q, want %q", got, ClaudePlanPluginV2)
	}
}

func TestClaudePlanPluginsDeferToUserConfiguredPlugin(t *testing.T) {
	dataHome := isolateClaudePlanEnv(t)
	writeClaudePlanFile(t, filepath.Join(dataHome, "opencode", "auth.json"), `{"anthropic":{"type":"oauth"}}`)
	writeClaudePlanFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "opencode", "opencode.json"), `{"plugin":["opencode-claude-auth@2.1.0"]}`)
	if got := ClaudePlanPlugins(context.Background(), 1, ""); got != nil {
		t.Fatalf("plugins = %q, want none when the user already loads one", got)
	}
}

func TestAddConfigPluginsKeepsExistingSettingsAndPlugins(t *testing.T) {
	got, err := AddConfigPlugins(`{"plugins":["user","opencode-claude-auth@3.0.0"],"default_agent":"build"}`, "plugins", []string{ClaudePlanPluginV2, "extra"})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(got), &config); err != nil {
		t.Fatal(err)
	}
	plugins, _ := config["plugins"].([]any)
	if len(plugins) != 3 || plugins[0] != "user" || plugins[1] != ClaudePlanPluginV2 || plugins[2] != "extra" || config["default_agent"] != "build" {
		t.Fatalf("config = %#v, want existing settings and de-duplicated plugins", config)
	}
	if unchanged, err := AddConfigPlugins(`{"a":1}`, "plugin", nil); err != nil || unchanged != `{"a":1}` {
		t.Fatalf("no plugins = %q, %v; want content unchanged", unchanged, err)
	}
	if _, err := AddConfigPlugins(`{"plugin":"not-a-list"}`, "plugin", []string{"x"}); err == nil {
		t.Fatal("non-list plugin value accepted, want an error")
	}
}

func TestOpenCodeConfigFileCarriesClaudePlanPlugin(t *testing.T) {
	promptFile := filepath.Join(t.TempDir(), "system-prompt.md")
	writeClaudePlanFile(t, promptFile, "be helpful")
	prefix, _, err := opencodeConfigEnvPrefix("", "", promptFile, "ao-1", []string{ClaudePlanPluginV1})
	if err != nil || len(prefix) != 2 {
		t.Fatalf("prefix = %q, %v", prefix, err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(promptFile), "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Plugin []string `json:"plugin"`
	}
	if err := json.Unmarshal(data, &config); err != nil || !slices.Equal(config.Plugin, []string{ClaudePlanPluginV1}) {
		t.Fatalf("AO config plugin = %q (%v), want %q", config.Plugin, err, ClaudePlanPluginV1)
	}
}
