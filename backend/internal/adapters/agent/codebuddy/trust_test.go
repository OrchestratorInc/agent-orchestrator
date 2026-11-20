package codebuddy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The login workspace joins CodeBuddy's trusted directories once, other
// settings survive, and settings AO cannot parse are never rewritten.
func TestEnsureWorkspaceTrustedRecordsTheLoginWorkspace(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CODEBUDDY_CONFIG_DIR", cfg)
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(cfg, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model":"gpt-4.1-mini","trustedDirectories":["/elsewhere"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := EnsureWorkspaceTrusted(context.Background(), ws); err != nil {
			t.Fatal(err)
		}
	}
	var got struct {
		Model   string   `json:"model"`
		Trusted []string `json:"trustedDirectories"`
	}
	data, _ := os.ReadFile(settings)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-4.1-mini" || len(got.Trusted) != 2 || got.Trusted[0] != "/elsewhere" || got.Trusted[1] != ws {
		t.Fatalf("settings = %s", data)
	}

	if err := os.WriteFile(settings, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkspaceTrusted(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(settings); string(data) != `{not json` {
		t.Fatalf("unparseable settings were rewritten: %s", data)
	}
}
