package minimaxcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeRefreshFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileRefreshPreservesSnapshotWhenSourceAuthIsMissing(t *testing.T) {
	for _, mode := range []string{"tui", "chat"} {
		t.Run(mode, func(t *testing.T) {
			source, data := t.TempDir(), t.TempDir()
			writeRefreshFixture(t, filepath.Join(source, "config.yaml"), "defaultModel: p/original\n")
			writeRefreshFixture(t, filepath.Join(source, "auth", "token"), "working-token")
			plugin := New()
			env := map[string]string{"MINIMAX_DATA_DIR": source}
			var profile string
			prepare := func() error {
				if mode == "chat" {
					path, err := plugin.PrepareChatProfile(context.Background(), data, "refresh-1", env)
					if err == nil {
						profile = path
					}
					return err
				}
				profile, _ = profilePath(data, "refresh-1")
				return plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
					DataDir: data, SessionID: "refresh-1", Env: env,
					Config: ports.AgentConfig{Permissions: ports.PermissionModeAuto},
				})
			}
			if err := prepare(); err != nil {
				t.Fatal(err)
			}
			previousConfig, err := os.ReadFile(filepath.Join(profile, "config.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			writeRefreshFixture(t, filepath.Join(profile, "v2", "history-proof"), "native-history")
			if err := os.Remove(filepath.Join(source, "auth", "token")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(source, "auth")); err != nil {
				t.Fatal(err)
			}
			writeRefreshFixture(t, filepath.Join(source, "config.yaml"), "defaultModel: p/replacement\n")
			if err := prepare(); err == nil || !strings.Contains(err.Error(), "source auth is missing") {
				t.Fatalf("missing source auth refresh = %v, want preservation error", err)
			}
			for name, want := range map[string]string{
				"config.yaml": string(previousConfig), "auth/token": "working-token", "v2/history-proof": "native-history",
			} {
				got, err := os.ReadFile(filepath.Join(profile, name))
				if err != nil || string(got) != want {
					t.Fatalf("missing source auth changed %s: %q, %v", name, got, err)
				}
			}
		})
	}
}

func TestProfileRefreshRollsBackAuthWhenConfigWriteFails(t *testing.T) {
	for _, hadAuth := range []bool{false, true} {
		name := "without-previous-auth"
		if hadAuth {
			name = "with-previous-auth"
		}
		t.Run(name, func(t *testing.T) {
			source, target := t.TempDir(), t.TempDir()
			writeRefreshFixture(t, filepath.Join(source, "config.yaml"), "defaultModel: p/replacement\n")
			writeRefreshFixture(t, filepath.Join(source, "auth", "token"), "replacement-token")
			if hadAuth {
				writeRefreshFixture(t, filepath.Join(target, "auth", "token"), "working-token")
			}
			// A nonempty directory forces the final atomic config rename to fail
			// on every platform without permission assumptions or fault injection.
			writeRefreshFixture(t, filepath.Join(target, "config.yaml", "previous"), "unchanged-config-path")
			if err := refreshProfileCredentials(context.Background(), target, source); err == nil {
				t.Fatal("config write unexpectedly succeeded")
			}
			if hadAuth {
				got, err := os.ReadFile(filepath.Join(target, "auth", "token"))
				if err != nil || string(got) != "working-token" {
					t.Fatalf("working credentials not restored: %q, %v", got, err)
				}
			} else if _, err := os.Lstat(filepath.Join(target, "auth")); !os.IsNotExist(err) {
				t.Fatalf("failed config write left replacement auth behind: %v", err)
			}
			for path, want := range map[string]string{
				filepath.Join(target, "config.yaml", "previous"): "unchanged-config-path",
				filepath.Join(source, "auth", "token"):           "replacement-token",
			} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("rollback changed %s: %q, %v", path, got, err)
				}
			}
			entries, err := os.ReadDir(target)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".ao-auth-refresh-") {
					t.Fatal("successful rollback left a credential backup behind")
				}
			}
		})
	}
}

func TestProfileRefreshSupportsConfigOnlyCredentials(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	config := "custom_provider:\n  configured:\n    apiKey: inline-key\n"
	writeRefreshFixture(t, filepath.Join(source, "config.yaml"), config)
	if err := refreshProfileCredentials(context.Background(), target, source); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "config.yaml"))
	if err != nil || string(got) != config {
		t.Fatalf("config-only credentials not refreshed: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(target, "auth")); !os.IsNotExist(err) {
		t.Fatalf("config-only refresh invented auth directory: %v", err)
	}
}
