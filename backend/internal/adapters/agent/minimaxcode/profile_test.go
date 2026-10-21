package minimaxcode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// MiniMax honors explicit deny/ask rules even under bypassPermissions, so the
// private profile must keep the source's global policy.
func TestTUIProfileKeepsGlobalPermissionPolicy(t *testing.T) {
	source, data := t.TempDir(), t.TempDir()
	writeRefreshFixture(t, filepath.Join(source, "permission.json"), `{"deny":["bash"]}`)
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		DataDir: data, SessionID: "policy-1", Env: map[string]string{"MINIMAX_DATA_DIR": source},
		Config: ports.AgentConfig{Permissions: ports.PermissionModeBypassPermissions},
	}); err != nil {
		t.Fatal(err)
	}
	profile, _ := profilePath(data, "policy-1")
	if got, err := os.ReadFile(filepath.Join(profile, "permission.json")); err != nil || string(got) != `{"deny":["bash"]}` {
		t.Fatalf("private profile dropped the global policy: %q, %v", got, err)
	}
}

// With auth state for both Global and China accounts, the saved region is what
// keeps MiniMax on the signed-in namespace.
func TestProfilesKeepAndRefreshNativeRegion(t *testing.T) {
	for _, mode := range []string{"tui", "chat"} {
		t.Run(mode, func(t *testing.T) {
			source, data := t.TempDir(), t.TempDir()
			writeRefreshFixture(t, filepath.Join(source, "auth", "prod", "en", "mcode-public", "auth-state.json"), `{"status":"authenticated"}`)
			writeRefreshFixture(t, filepath.Join(source, "auth", "prod", "cn", "mcode-public", "auth-state.json"), `{"status":"expired"}`)
			region := filepath.Join(source, "preferences", "mcode-region.json")
			writeRefreshFixture(t, region, `{"prod":"en"}`)
			plugin := New()
			env := map[string]string{"MINIMAX_DATA_DIR": source}
			var profile string
			prepare := func() {
				t.Helper()
				if mode == "chat" {
					path, err := plugin.PrepareChatProfile(context.Background(), data, "region-1", env)
					if err != nil {
						t.Fatal(err)
					}
					profile = path
					return
				}
				profile, _ = profilePath(data, "region-1")
				if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{DataDir: data, SessionID: "region-1", Env: env}); err != nil {
					t.Fatal(err)
				}
			}
			assertRegion := func(want string) {
				t.Helper()
				got, err := os.ReadFile(filepath.Join(profile, "preferences", "mcode-region.json"))
				if err != nil || string(got) != want {
					t.Fatalf("private region = %q, %v; want %q", got, err, want)
				}
			}
			prepare()
			assertRegion(`{"prod":"en"}`)
			writeRefreshFixture(t, region, `{"prod":"cn"}`)
			prepare()
			assertRegion(`{"prod":"cn"}`)
		})
	}
}

// The official installer's launcher runs a private Node by absolute path, so
// hooks must name that runtime rather than rely on PATH.
func TestOfficialLauncherNodePinsHookRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the official launcher layout is a POSIX shell script")
	}
	root := filepath.Join(t.TempDir(), "minimax code's install")
	node := filepath.Join(root, "runtime", "node")
	writeRefreshFixture(t, node, "")
	if err := os.Chmod(node, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bin", "mcode")
	writeRefreshFixture(t, binary, "#!/bin/sh\n")
	writeRefreshFixture(t, filepath.Join(root, "current"), "0.6.12\n")
	launcher := filepath.Join(root, "releases", "0.6.12", ".mcode-launcher")
	// Exact escaping from the official install.sh write_versioned_launchers.
	assignment := "node='" + strings.ReplaceAll(node, "'", `'"'"'`) + "'"
	writeRefreshFixture(t, launcher, "#!/bin/sh\nset -eu\n"+assignment+"\nexec \"$node\" cli.js \"$@\"\n")
	if out, err := exec.Command("/bin/sh", "-c", assignment+`; printf %s "$node"`).Output(); err != nil || string(out) != node {
		t.Fatalf("fixture assignment resolves to %q, %v; want %q", out, err, node)
	}
	if got := officialLauncherNode(binary); got != node {
		t.Fatalf("launcher node = %q, want %q", got, node)
	}
	plugin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(plugin, "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeHookConfig(plugin, node, "/launch"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	// The pinned runtime is spawned as argv, so no shell has to parse its path.
	handler := config.Hooks["UserPromptSubmit"][0].Hooks[0]
	launch := base64.RawURLEncoding.EncodeToString([]byte("/launch"))
	if handler.Command != node || len(handler.Args) != 2 || handler.Args[0] != "${CLAUDE_PLUGIN_ROOT}/hook.cjs" || handler.Args[1] != launch {
		t.Fatalf("hook handler = %+v; want command %q with hook and launch args", handler, node)
	}
	writeRefreshFixture(t, filepath.Join(root, "current"), "../escape\n")
	if got := officialLauncherNode(binary); got != "" {
		t.Fatalf("accepted an invalid release pointer: %q", got)
	}
}

// The official Windows installer puts mcode.cmd at the install root and its
// release launcher invokes the private node.exe directly.
func TestOfficialWindowsLauncherNode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "minimax code")
	node := filepath.Join(root, "runtime", "node.exe")
	writeRefreshFixture(t, node, "")
	binary := filepath.Join(root, "mcode.cmd")
	writeRefreshFixture(t, binary, "@ECHO off\r\n")
	writeRefreshFixture(t, filepath.Join(root, "current"), "0.6.12\r\n")
	launcher := filepath.Join(root, "releases", "0.6.12", ".mcode-launcher.cmd")
	writeRefreshFixture(t, launcher, "@ECHO off\r\n\""+node+"\" \"%~dp0node_modules\\@minimax-ai\\code\\cli.js\" %*\r\nEXIT /B %ERRORLEVEL%\r\n")
	if got := officialLauncherNode(binary); got != node {
		t.Fatalf("launcher node = %q, want %q", got, node)
	}
	writeRefreshFixture(t, launcher, "@ECHO off\r\n\"%NODE%\" cli.js %*\r\n")
	if got := officialLauncherNode(binary); got != "" {
		t.Fatalf("resolved a cmd variable: %q", got)
	}
}

func TestUnquoteShellWord(t *testing.T) {
	for word, want := range map[string]string{
		`'/a/b'`:             "/a/b",
		`'/a/it'"'"'s/node'`: "/a/it's/node",
		`"/a/\$x"`:           "/a/$x",
		`/a\ b`:              "/a b",
	} {
		if got, ok := unquoteShellWord(word); !ok || got != want {
			t.Errorf("unquoteShellWord(%s) = %q, %v; want %q", word, got, ok, want)
		}
	}
	for _, word := range []string{`"$HOME/node"`, "`which node`", `~/node`, `'/open`, `/a b`, `/a;rm`} {
		if got, ok := unquoteShellWord(word); ok {
			t.Errorf("unquoteShellWord(%s) = %q; want rejected", word, got)
		}
	}
}

// A refused credential refresh must not switch the account namespace: the
// region preference is published only with its auth.
func TestRejectedRefreshKeepsRegionPreference(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	oldPreference := `{"version":1,"regions":{"prod":"en"}}`
	writeRefreshFixture(t, filepath.Join(source, "config.yaml"), "defaultModel: minimax/model\n")
	writeRefreshFixture(t, filepath.Join(source, nativeRegionPreference), `{"version":1,"regions":{"prod":"cn"}}`)
	writeRefreshFixture(t, filepath.Join(target, nativeRegionPreference), oldPreference)
	writeRefreshFixture(t, filepath.Join(target, "auth", "prod", "en", "mcode-public", "auth-state.json"), `{"status":"authenticated"}`)
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "auth")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := refreshProfileCredentials(context.Background(), target, source); err == nil {
		t.Fatal("refresh accepted symlinked source auth")
	}
	if got, err := os.ReadFile(filepath.Join(target, nativeRegionPreference)); err != nil || string(got) != oldPreference {
		t.Fatalf("region preference = %s, %v; want %s", got, err, oldPreference)
	}
	if _, err := os.Stat(filepath.Join(target, "auth", "prod", "en", "mcode-public", "auth-state.json")); err != nil {
		t.Fatalf("previous auth lost: %v", err)
	}
}

// A lock held while AO snapshots the source has no owner in the copy; native
// OAuth would wait for it to go stale, so lock directories are not copied.
func TestSnapshotSkipsNativeLockDirectories(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	namespace := filepath.Join("prod", "en", "mcode-public")
	writeRefreshFixture(t, filepath.Join(source, "auth", namespace, "auth-state.json"), `{"status":"authenticated"}`)
	if err := os.MkdirAll(filepath.Join(source, "auth", namespace, "auth.lock.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyProfileTree(context.Background(), filepath.Join(source, "auth"), filepath.Join(target, "auth")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "auth", namespace, "auth-state.json")); err != nil {
		t.Fatalf("auth state not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "auth", namespace, "auth.lock.lock")); !os.IsNotExist(err) {
		t.Fatalf("copied a native lock directory: %v", err)
	}
}

// install.ps1 escapes a literal percent in the runtime path as %%.
func TestOfficialWindowsLauncherNodeDecodesPercent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "100% minimax")
	node := filepath.Join(root, "runtime", "node.exe")
	writeRefreshFixture(t, node, "")
	binary := filepath.Join(root, "mcode.cmd")
	writeRefreshFixture(t, binary, "@ECHO off\r\n")
	writeRefreshFixture(t, filepath.Join(root, "current"), "0.6.12\r\n")
	launcher := filepath.Join(root, "releases", "0.6.12", ".mcode-launcher.cmd")
	escaped := strings.ReplaceAll(node, "%", "%%")
	writeRefreshFixture(t, launcher, "@ECHO off\r\n\""+escaped+"\" \"%~dp0node_modules\\@minimax-ai\\code\\cli.js\" %*\r\n")
	if got := officialLauncherNode(binary); got != node {
		t.Fatalf("launcher node = %q, want %q", got, node)
	}
}
