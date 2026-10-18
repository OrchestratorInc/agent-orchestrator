package agy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	plugin := New()
	manifest := plugin.Manifest()
	if manifest.ID != "agy" {
		t.Fatalf("manifest id = %q, want agy", manifest.ID)
	}
}

func TestGetLaunchCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "agy"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Permissions:   ports.PermissionModeBypassPermissions,
		Prompt:        "fix this",
		WorkspacePath: "/tmp/ws",
		Config:        ports.AgentConfig{Model: "gemini-3-pro"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"agy",
		"--add-dir", "/tmp/ws",
		"--dangerously-skip-permissions",
		"--model", "gemini-3-pro",
		"--prompt-interactive", "fix this",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandNoModel(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "agy"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Permissions:   ports.PermissionModeBypassPermissions,
		Prompt:        "fix this",
		WorkspacePath: "/tmp/ws",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"agy",
		"--add-dir", "/tmp/ws",
		"--dangerously-skip-permissions",
		"--prompt-interactive", "fix this",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	plugin := &Plugin{}
	got, err := plugin.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.PromptDeliveryInCommand {
		t.Fatalf("strategy = %q, want in_command", got)
	}
}

func TestGetRestoreCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "agy"}

	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Config:      ports.AgentConfig{Model: "gemini-3-flash"},
		Session: ports.SessionRef{
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "native-id-123"},
			WorkspacePath: "/tmp/ws",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}

	want := []string{
		"agy",
		"--add-dir", "/tmp/ws",
		"--dangerously-skip-permissions",
		"--model", "gemini-3-flash",
		"--conversation", "native-id-123",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetRestoreCommandNoSessionID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "agy"}
	_, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			Metadata: map[string]string{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false when agentSessionId is missing")
	}
}

func TestNativeConversationIDUsesTheSharedAntigravityID(t *testing.T) {
	plugin := New()
	for _, tc := range []struct {
		name       string
		mode       domain.SessionMode
		providerID string
		agentID    string
		want       string
	}{
		{name: "ACP to CLI", mode: domain.SessionModeChat, providerID: " shared-id ", agentID: "stale-cli-id", want: "shared-id"},
		{name: "CLI to ACP", mode: domain.SessionModeTUI, providerID: "stale-acp-id", agentID: " shared-id ", want: "shared-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := plugin.NativeConversationID(context.Background(), ports.SessionRef{
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: tc.agentID},
			}, tc.mode, tc.providerID)
			if err != nil || !ok || got != tc.want {
				t.Fatalf("NativeConversationID = (%q, %v, %v), want (%q, true, nil)", got, ok, err, tc.want)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := plugin.NativeConversationID(ctx, ports.SessionRef{}, domain.SessionModeChat, "shared-id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled NativeConversationID error = %v, want context.Canceled", err)
	}
}

func TestSessionInfo(t *testing.T) {
	plugin := &Plugin{}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "native-id-123",
			"title":                         "My Title",
			"summary":                       "My Summary",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if info.AgentSessionID != "native-id-123" || info.Title != "My Title" || info.Summary != "My Summary" {
		t.Fatalf("unexpected SessionInfo: %#v", info)
	}
}

func TestActivitySignalCapabilities(t *testing.T) {
	p := &Plugin{}
	if !p.EmitsSubmitActivity() {
		t.Fatal("EmitsSubmitActivity = false, want true")
	}
	if p.EmitsBlockedActivity() {
		t.Fatal("EmitsBlockedActivity = true, want false")
	}
}

func TestHooksLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	plugin := &Plugin{}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: tmpDir}

	installed, err := plugin.AreHooksInstalled(context.Background(), tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("expected hooks to not be installed initially")
	}

	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	installed, err = plugin.AreHooksInstalled(context.Background(), tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("expected hooks to be installed after GetAgentHooks")
	}

	hooksJSONPath := filepath.Join(tmpDir, ".agents", "hooks.json")
	data, err := os.ReadFile(hooksJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".gemini", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy .gemini/hooks.json exists or stat failed: %v", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	aoRaw, ok := top[agyManagedHookName]
	if !ok {
		t.Fatalf("missing named hook %q", agyManagedHookName)
	}
	var ao agyNamedHook
	if err := json.Unmarshal(aoRaw, &ao); err != nil {
		t.Fatal(err)
	}
	if len(ao.PreInvocation) != 1 || ao.PreInvocation[0].Command != "ao hooks agy pre-invocation" || ao.PreInvocation[0].Timeout != 30 {
		t.Fatalf("unexpected PreInvocation hooks: %#v", ao.PreInvocation)
	}
	if len(ao.PostToolUse) != 1 || ao.PostToolUse[0].Matcher == nil || *ao.PostToolUse[0].Matcher != "*" || len(ao.PostToolUse[0].Hooks) != 1 || ao.PostToolUse[0].Hooks[0].Command != "ao hooks agy post-tool-use" || ao.PostToolUse[0].Hooks[0].Timeout != 30 {
		t.Fatalf("unexpected PostToolUse hooks: %#v", ao.PostToolUse)
	}
	if len(ao.Stop) != 1 || ao.Stop[0].Command != "ao hooks agy stop" || ao.Stop[0].Timeout != 30 {
		t.Fatalf("unexpected Stop hooks: %#v", ao.Stop)
	}

	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	secondData, err := os.ReadFile(hooksJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	var secondTop map[string]json.RawMessage
	if err := json.Unmarshal(secondData, &secondTop); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(top[agyManagedHookName], secondTop[agyManagedHookName]) {
		t.Fatal("reinstall changed the managed hook entry")
	}

	if err := plugin.UninstallHooks(context.Background(), tmpDir); err != nil {
		t.Fatal(err)
	}
	installed, err = plugin.AreHooksInstalled(context.Background(), tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("expected hooks to be uninstalled after UninstallHooks")
	}
}

func TestHooksPreserveUserNamedHooks(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, ".agents", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		t.Fatal(err)
	}
	seed := []byte(`{
  "user-linter": {
    "enabled": false,
    "PostToolUse": [{"matcher":"run_command","hooks":[{"command":"./lint.sh"}]}]
  },
  "future-field": {"value": 7}
}`)
	if err := os.WriteFile(hooksPath, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	var before map[string]json.RawMessage
	if err := json.Unmarshal(seed, &before); err != nil {
		t.Fatal(err)
	}

	plugin := &Plugin{}
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: tmpDir}); err != nil {
		t.Fatal(err)
	}
	afterInstall := readHookRawMap(t, hooksPath)
	if _, ok := afterInstall[agyManagedHookName]; !ok {
		t.Fatalf("missing named hook %q", agyManagedHookName)
	}
	assertRawJSONEqual(t, before["user-linter"], afterInstall["user-linter"])
	assertRawJSONEqual(t, before["future-field"], afterInstall["future-field"])

	if err := plugin.UninstallHooks(context.Background(), tmpDir); err != nil {
		t.Fatal(err)
	}
	afterUninstall := readHookRawMap(t, hooksPath)
	if _, ok := afterUninstall[agyManagedHookName]; ok {
		t.Fatalf("named hook %q remains after uninstall", agyManagedHookName)
	}
	assertRawJSONEqual(t, before["user-linter"], afterUninstall["user-linter"])
	assertRawJSONEqual(t, before["future-field"], afterUninstall["future-field"])
}

func TestHooksRejectMalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, ".agents", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{not-json`)
	if err := os.WriteFile(hooksPath, want, 0o600); err != nil {
		t.Fatal(err)
	}

	err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: tmpDir})
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("GetAgentHooks error = %v, want parse error", err)
	}
	got, readErr := os.ReadFile(hooksPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("malformed file changed: got %q, want %q", got, want)
	}
}

func TestAreHooksInstalledRejectsDifferentEntryWithManagedName(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, ".agents", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"agent-orchestrator":{"Stop":[{"command":"user-command"}]}}`)
	if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	installed, err := (&Plugin{}).AreHooksInstalled(context.Background(), tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("different named entry reported as AO-managed hooks")
	}
}

func readHookRawMap(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertRawJSONEqual(t *testing.T, want, got json.RawMessage) {
	t.Helper()
	var wantValue, gotValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON values differ\nwant: %s\n got: %s", want, got)
	}
}

func TestGetConfigSpecReportsModelField(t *testing.T) {
	plugin := &Plugin{}

	spec, err := plugin.GetConfigSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.ConfigField{
		{
			Key:         "model",
			Type:        ports.ConfigFieldString,
			Description: "Model override passed to `agy --model` (e.g. gemini-3-pro).",
		},
	}
	if !reflect.DeepEqual(spec.Fields, want) {
		t.Fatalf("config fields\nwant: %#v\n got: %#v", want, spec.Fields)
	}
}

func TestGetRestoreCommandDoesNotBridgeDuringPreflight(t *testing.T) {
	geminiHome := t.TempDir()
	t.Setenv("GEMINI_HOME", geminiHome)

	acpDB := filepath.Join(geminiHome, "antigravity-acp", "conversations", "native-id-456.db")
	if err := os.MkdirAll(filepath.Dir(acpDB), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(acpDB, []byte("acp-db-payload"), 0600); err != nil {
		t.Fatal(err)
	}

	plugin := &Plugin{resolvedBinary: "agy"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-id-456"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("GetRestoreCommand: ok=%v, err=%v", ok, err)
	}
	if len(cmd) == 0 || cmd[len(cmd)-1] != "native-id-456" {
		t.Fatalf("unexpected cmd: %v", cmd)
	}

	cliDB := filepath.Join(geminiHome, "antigravity-cli", "conversations", "native-id-456.db")
	if _, err := os.Stat(cliDB); !os.IsNotExist(err) {
		t.Fatalf("preflight unexpectedly bridged source storage: stat err = %v", err)
	}
}

func TestNativeConversationExistsRequiresPersistedSteps(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "antigravity-acp", "conversations", "native-id.db")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE steps (id INTEGER PRIMARY KEY)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	plugin := New()
	exists, err := plugin.NativeConversationExists(context.Background(), ports.SessionRef{}, "native-id", map[string]string{"GEMINI_HOME": home})
	if err != nil || exists {
		t.Fatalf("empty conversation exists = %v, err = %v; want false, nil", exists, err)
	}
	db, err = sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO steps DEFAULT VALUES"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	exists, err = plugin.NativeConversationExists(context.Background(), ports.SessionRef{}, "native-id", map[string]string{"GEMINI_HOME": home})
	if err != nil || !exists {
		t.Fatalf("persisted conversation exists = %v, err = %v; want true, nil", exists, err)
	}
}

func TestNativeConversationExistsModeScoped(t *testing.T) {
	home := t.TempDir()
	plugin := New()
	env := map[string]string{"GEMINI_HOME": home}

	// Create a DB only in antigravity-acp.
	acpPath := filepath.Join(home, "antigravity-acp", "conversations", "conv-1.db")
	if err := os.MkdirAll(filepath.Dir(acpPath), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(acpPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE steps (id INTEGER PRIMARY KEY); INSERT INTO steps DEFAULT VALUES"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	// TUI probe must NOT find the ACP-only database.
	tuiRef := ports.SessionRef{Metadata: map[string]string{ports.MetadataKeySessionMode: "tui"}}
	exists, err := plugin.NativeConversationExists(context.Background(), tuiRef, "conv-1", env)
	if err != nil || exists {
		t.Fatalf("tui probe found ACP database: exists = %v, err = %v; want false, nil", exists, err)
	}

	// Chat probe MUST find the ACP database.
	chatRef := ports.SessionRef{Metadata: map[string]string{ports.MetadataKeySessionMode: "chat"}}
	exists, err = plugin.NativeConversationExists(context.Background(), chatRef, "conv-1", env)
	if err != nil || !exists {
		t.Fatalf("chat probe failed to find ACP database: exists = %v, err = %v; want true, nil", exists, err)
	}

	// Create a DB only in antigravity-cli.
	cliPath := filepath.Join(home, "antigravity-cli", "conversations", "conv-2.db")
	if err := os.MkdirAll(filepath.Dir(cliPath), 0700); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", "file:"+filepath.ToSlash(cliPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE steps (id INTEGER PRIMARY KEY); INSERT INTO steps DEFAULT VALUES"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	// Chat probe must NOT find the CLI-only database.
	exists, err = plugin.NativeConversationExists(context.Background(), chatRef, "conv-2", env)
	if err != nil || exists {
		t.Fatalf("chat probe found CLI database: exists = %v, err = %v; want false, nil", exists, err)
	}

	// TUI probe MUST find the CLI database.
	exists, err = plugin.NativeConversationExists(context.Background(), tuiRef, "conv-2", env)
	if err != nil || !exists {
		t.Fatalf("tui probe failed to find CLI database: exists = %v, err = %v; want true, nil", exists, err)
	}
}
