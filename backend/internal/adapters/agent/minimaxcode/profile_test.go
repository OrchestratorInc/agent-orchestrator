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

func TestProfileRefreshPreservesPrivateAuthWhenSourceAuthIsMissing(t *testing.T) {
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
			writeRefreshFixture(t, filepath.Join(profile, "v2", "history-proof"), "native-history")
			if err := os.Remove(filepath.Join(source, "auth", "token")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(source, "auth")); err != nil {
				t.Fatal(err)
			}
			writeRefreshFixture(t, filepath.Join(source, "config.yaml"), "defaultModel: p/replacement\n")
			if err := prepare(); err != nil {
				t.Fatalf("missing source auth prevented profile reuse: %v", err)
			}
			config, err := os.ReadFile(filepath.Join(profile, "config.yaml"))
			if err != nil || !strings.Contains(string(config), "defaultModel: p/replacement") {
				t.Fatalf("config-only provider settings were not refreshed: %q, %v", config, err)
			}
			if mode == "tui" && !strings.Contains(string(config), "permissionMode: auto") {
				t.Fatalf("config refresh lost AO permission selection: %s", config)
			}
			for name, want := range map[string]string{
				"auth/token": "working-token", "v2/history-proof": "native-history",
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
	// Native MiniMax creates this private directory even if the source uses
	// only config.yaml credentials. A replacement process must still launch.
	writeRefreshFixture(t, filepath.Join(target, "auth", "native-state"), "private-native-state")
	updated := strings.ReplaceAll(config, "inline-key", "rotated-inline-key")
	writeRefreshFixture(t, filepath.Join(source, "config.yaml"), updated)
	if err := refreshProfileCredentials(context.Background(), target, source); err != nil {
		t.Fatalf("native-created auth blocked config-only restore: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(target, "config.yaml"):          updated,
		filepath.Join(target, "auth", "native-state"): "private-native-state",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("config-only restore changed %s: %q, %v", path, got, err)
		}
	}
}

func TestRuntimeLaunchPrunesOldSnapshotsAfterPublication(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	cfg := ports.WorkspaceHookConfig{
		DataDir: data, SessionID: "prune-1",
		Env: map[string]string{
			"MINIMAX_DATA_DIR": source, "AO_DATA_DIR": data,
			"AO_SESSION_ID": "prune-1", "AO_RUNTIME_LAUNCH_ID": "first",
		},
	}
	plugin := New()
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	profile, _ := profilePath(data, cfg.SessionID)
	launches := filepath.Join(profile, "launches")
	writeRefreshFixture(t, filepath.Join(profile, "standing.txt"), "first-context")
	if err := plugin.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	writeRefreshFixture(t, filepath.Join(launches, "abandoned", "tmp", "old-tool-cache"), "old-cache")
	history := filepath.Join(profile, "v2", "history-proof")
	neighbor := filepath.Join(data, "agents", adapterID, "other-session", "launches", "other", "standing.txt")
	writeRefreshFixture(t, history, "native-history")
	writeRefreshFixture(t, neighbor, "other-session-context")

	// A failed hook-config publication must not prune the previous snapshot.
	hookConfig := filepath.Join(profile, "plugins", "ao-activity", "hooks", "hooks.json")
	if err := os.Remove(hookConfig); err != nil {
		t.Fatal(err)
	}
	writeRefreshFixture(t, filepath.Join(hookConfig, "block-rename"), "blocked")
	cfg.Env["AO_RUNTIME_LAUNCH_ID"] = "failed"
	if err := plugin.PrepareRuntimeLaunch(context.Background(), cfg); err == nil {
		t.Fatal("hook config publication unexpectedly succeeded")
	}
	got, err := os.ReadFile(filepath.Join(launches, "first", "standing.txt"))
	if err != nil || string(got) != "first-context" {
		t.Fatalf("failed launch pruned previous snapshot: %q, %v", got, err)
	}
	if err := os.Remove(filepath.Join(hookConfig, "block-rename")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(hookConfig); err != nil {
		t.Fatal(err)
	}

	cfg.Env["AO_RUNTIME_LAUNCH_ID"] = "current"
	writeRefreshFixture(t, filepath.Join(profile, "standing.txt"), "current-context")
	if err := plugin.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(launches)
	if err != nil || len(entries) != 1 || entries[0].Name() != "current" {
		t.Fatalf("old or failed launch snapshots remain: %v, %v", entries, err)
	}
	for path, want := range map[string]string{
		filepath.Join(launches, "current", "standing.txt"): "current-context",
		history: "native-history", neighbor: "other-session-context",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("pruning changed retained state at %s: %q, %v", path, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(launches, "current", "routing.json")); err != nil {
		t.Fatalf("current routing snapshot was pruned: %v", err)
	}
}
