package copilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnsureWorkspaceTrustedCreatesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COPILOT_HOME", "")
	workspace := t.TempDir()

	if err := EnsureWorkspaceTrusted(context.Background(), workspace); err != nil {
		t.Fatalf("EnsureWorkspaceTrusted: %v", err)
	}
	want, _ := filepath.EvalSymlinks(workspace)
	if got := readTrustedFolders(t, filepath.Join(home, ".copilot", "config.json")); !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("trustedFolders = %#v, want [%q]", got, want)
	}
}

func TestEnsureCopilotFolderTrustedPreservesHeaderAndKeysIdempotently(t *testing.T) {
	workspace := t.TempDir()
	want, _ := filepath.EvalSymlinks(workspace)
	configPath := filepath.Join(t.TempDir(), "config.json")
	original := "// User settings belong in settings.json.\n// This file is managed automatically.\n{\n  \"firstLaunchAt\": \"2026-06-25T06:59:05.530Z\",\n  \"trustedFolders\": [\"/existing\"]\n}\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := ensureCopilotFolderTrusted(configPath, workspace); err != nil {
			t.Fatalf("ensureCopilotFolderTrusted #%d: %v", i, err)
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "// User settings belong in settings.json.\n// This file is managed automatically.\n{") {
		t.Fatalf("config lost its comment header:\n%s", data)
	}
	if !strings.Contains(string(data), `"firstLaunchAt": "2026-06-25T06:59:05.530Z"`) {
		t.Fatalf("config lost existing keys:\n%s", data)
	}
	if got := readTrustedFolders(t, configPath); !reflect.DeepEqual(got, []string{"/existing", want}) {
		t.Fatalf("trustedFolders = %#v, want existing entry plus %q once", got, want)
	}
}

func TestEnsureCopilotFolderTrustedLeavesUnsupportedConfigAlone(t *testing.T) {
	for name, original := range map[string]string{
		"unparseable":         "{not json",
		"null":                "null",
		"null after header":   "// This file is managed automatically.\nnull\n",
		"array":               "[]",
		"non-array trust key": `{"trustedFolders": "/existing"}`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ensureCopilotFolderTrusted(configPath, t.TempDir()); err != nil {
				t.Fatalf("ensureCopilotFolderTrusted: %v", err)
			}
			if data, _ := os.ReadFile(configPath); string(data) != original {
				t.Fatalf("config rewritten to %q, want untouched", data)
			}
		})
	}
}

func readTrustedFolders(t *testing.T, configPath string) []string {
	t.Helper()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	_, body := splitCopilotConfigHeader(data)
	var config struct {
		TrustedFolders []string `json:"trustedFolders"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatalf("decode config %s: %v", data, err)
	}
	return config.TrustedFolders
}
