package hermesacp

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"gopkg.in/yaml.v3"
)

func TestPrepareProfilePreservesNativeConfiguration(t *testing.T) {
	home := t.TempDir()
	config := "model: {default: native-default}\nagent: {system_prompt: user-safety}\nplugins: {enabled: [user-plugin], disabled: [other-plugin]}\n"
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareProfile(context.Background(), home, "private additive context"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["model"], map[string]any{"default": "native-default"}) || !reflect.DeepEqual(got["agent"], map[string]any{"system_prompt": "user-safety"}) {
		t.Fatal("native model/prompt changed")
	}
	plugins := got["plugins"].(map[string]any)
	if !reflect.DeepEqual(plugins["enabled"], []any{"user-plugin", pluginName}) || !reflect.DeepEqual(plugins["disabled"], []any{"other-plugin"}) {
		t.Fatal("user plugin configuration changed")
	}
	private := filepath.Join(home, "plugins", pluginName, "instructions.txt")
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("private context permissions")
	}
	if err := PrepareProfile(context.Background(), home, "replacement"); err == nil {
		t.Fatal("overwrote existing plugin")
	}
	data, err := os.ReadFile(private)
	if err != nil || string(data) != "private additive context" {
		t.Fatal("collision changed private context")
	}
}

func TestPrepareProfileRejectsUnsafeConfigurationBeforeWriting(t *testing.T) {
	for _, tc := range []struct{ name, config, prompt string }{
		{"missing allowlist", "plugins: {}", "context"},
		{"disabled", "plugins: {enabled: [], disabled: [ao-standing-context]}", "context"},
		{"malformed", "plugins: [wrong]", "context"},
		{"oversized", "plugins: {enabled: []}", strings.Repeat("x", 8193)},
		{"invalid utf8", "plugins: {enabled: []}", string([]byte{255})},
		{"malformed spill", "plugins: {enabled: []}\nhooks: {output_spill: {enabled: 'false', max_chars: 2}}", "context"},
		{"spill", "plugins: {enabled: []}\nhooks: {output_spill: {max_chars: 2}}", "context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.yaml")
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			if err := PrepareProfile(context.Background(), home, tc.prompt); err == nil {
				t.Fatal("accepted unsafe configuration")
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != tc.config {
				t.Fatal("changed rejected config")
			}
			if _, err := os.Stat(filepath.Join(home, "plugins", pluginName)); !os.IsNotExist(err) {
				t.Fatal("created rejected plugin")
			}
		})
	}
}

func TestValidateHistoryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, source, meta, cwd string
		messages, children      int
		want                    bool
	}{
		{"valid", "acp", "same", "same", 1, 0, true},
		{"empty", "acp", "same", "same", 0, 0, false},
		{"foreign", "cli", "same", "same", 1, 0, false},
		{"wrong cwd", "acp", "same", "other", 1, 0, false},
		{"wrong metadata cwd", "acp", "other", "same", 1, 0, false},
		{"malformed metadata", "acp", "bad", "same", 1, 0, false},
		{"compressed", "acp", "same", "same", 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace, other := t.TempDir(), t.TempDir(), t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			for _, q := range []string{
				`CREATE TABLE sessions(id TEXT PRIMARY KEY,source TEXT,cwd TEXT,model_config TEXT,parent_session_id TEXT)`,
				`CREATE TABLE messages(session_id TEXT,active INTEGER)`,
			} {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			cwd := workspace
			if tc.cwd == "other" {
				cwd = other
			}
			meta := `{"cwd":"` + filepath.ToSlash(workspace) + `"}`
			if tc.meta == "other" {
				meta = `{"cwd":"` + filepath.ToSlash(other) + `"}`
			} else if tc.meta == "bad" {
				meta = "not-json"
			}
			if _, err := db.Exec(`INSERT INTO sessions VALUES('native-id',?,?,?,NULL)`, tc.source, cwd, meta); err != nil {
				t.Fatal(err)
			}
			if tc.messages > 0 {
				if _, err := db.Exec(`INSERT INTO messages VALUES('native-id',1)`); err != nil {
					t.Fatal(err)
				}
			}
			if tc.children > 0 {
				if _, err := db.Exec(`INSERT INTO sessions VALUES('child','acp',?,?,'native-id')`, cwd, meta); err != nil {
					t.Fatal(err)
				}
			}
			err = ValidateHistory(context.Background(), home, "native-id", workspace)
			if (err == nil) != tc.want {
				t.Fatalf("error=%v, want success=%v", err, tc.want)
			}
			if err != nil && !errors.Is(err, ports.ErrChatResumeFailed) {
				t.Fatalf("missing typed error: %v", err)
			}
			for _, id := range []string{"missing", "latest", "../escape", ""} {
				if err := ValidateHistory(context.Background(), home, id, workspace); !errors.Is(err, ports.ErrChatResumeFailed) {
					t.Fatalf("id %q: %v", id, err)
				}
			}
		})
	}
}

func TestValidateMissingDatabaseDoesNotCreateIt(t *testing.T) {
	home := t.TempDir()
	if err := ValidateHistory(context.Background(), home, "native-id", t.TempDir()); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "state.db")); !os.IsNotExist(err) {
		t.Fatal("created missing native history")
	}
}
