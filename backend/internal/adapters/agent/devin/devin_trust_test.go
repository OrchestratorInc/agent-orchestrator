package devin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Existing empty or malformed files can be another writer's incomplete config.
// Neither trust updater may replace one with a document containing only AO trust.
func TestDevinTrustRefusesInvalidExistingConfig(t *testing.T) {
	for _, updater := range []struct {
		name  string
		file  string
		trust func(string, string) error
	}{
		{"claude", ".claude.json", ensureDevinWorkspaceTrusted},
		{"native", "trusted_workspaces.json", ensureDevinNativeWorkspaceTrusted},
	} {
		t.Run(updater.name, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				content   string
				wantError string
			}{
				{"zero-byte", "", "is empty; refusing to overwrite"},
				{"whitespace-only", " \t\r\n", "is empty; refusing to overwrite"},
				{"malformed", `{"unfinished":`, "devin: parse "},
			} {
				t.Run(tc.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), updater.file)
					if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
						t.Fatal(err)
					}

					err := updater.trust(path, "/new/workspace")
					if err == nil {
						t.Error("expected refusal of invalid existing config, got nil")
					} else if !strings.Contains(err.Error(), tc.wantError) || !strings.Contains(err.Error(), path) {
						t.Errorf("error = %q, want %q and config path %q", err, tc.wantError, path)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if string(data) != tc.content {
						t.Errorf("existing config overwritten: got %q, want %q", data, tc.content)
					}
				})
			}
		})
	}
}

func TestPreLaunchPropagatesEmptyTrustConfigError(t *testing.T) {
	for _, emptyFile := range []string{"native", "claude"} {
		t.Run(emptyFile, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			nativePath := filepath.Join(home, ".local", "share", "devin", "cli", "trusted_workspaces.json")
			claudePath := filepath.Join(home, ".claude.json")
			nativeSeed := `{"trusted_paths":["/existing/workspace"]}`
			claudeSeed := `{"oauthAccount":{"accountUuid":"account"},"projects":{"/existing/workspace":{"hasTrustDialogAccepted":true}}}`
			emptyPath := nativePath
			if emptyFile == "native" {
				nativeSeed = ""
			} else {
				claudeSeed = ""
				emptyPath = claudePath
			}
			if err := os.MkdirAll(filepath.Dir(nativePath), 0o700); err != nil {
				t.Fatal(err)
			}
			for path, seed := range map[string]string{nativePath: nativeSeed, claudePath: claudeSeed} {
				if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			err := (&Plugin{}).PreLaunch(context.Background(), ports.LaunchConfig{WorkspacePath: "/new/workspace"})
			if err == nil || !strings.Contains(err.Error(), emptyPath) || !strings.Contains(err.Error(), "is empty; refusing to overwrite") {
				t.Errorf("PreLaunch error = %v, want empty-config refusal for %s", err, emptyPath)
			}
			data, err := os.ReadFile(emptyPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) != 0 {
				t.Errorf("empty config overwritten: %s", data)
			}
			if emptyFile == "native" {
				data, err := os.ReadFile(claudePath)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != claudeSeed {
					t.Errorf("Claude config changed after native trust refusal: %s", data)
				}
			}
		})
	}
}
