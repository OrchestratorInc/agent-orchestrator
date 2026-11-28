package droid

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "droid" {
		t.Fatalf("ID = %q, want droid", m.ID)
	}
	if m.Name != "Droid" {
		t.Fatalf("Name = %q", m.Name)
	}
	hasAgent := false
	for _, c := range m.Capabilities {
		if c == adapters.CapabilityAgent {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Fatal("missing CapabilityAgent")
	}
}

func TestGetConfigSpecHasModelField(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(spec.Fields) != 1 || spec.Fields[0].Key != "model" {
		t.Fatalf("Fields = %#v, want single %q field", spec.Fields, "model")
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	s, err := (&Plugin{}).GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != ports.PromptDeliveryInCommand {
		t.Fatalf("strategy = %q, want in_command", s)
	}
}

func TestGetLaunchCommandDefaultPerms(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID: "mer-1",
		Prompt:    "do the thing",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"droid", "do the thing"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	if strings.Contains(strings.Join(cmd, " "), "--settings") {
		t.Fatal("default perms should not emit --settings")
	}
}

func TestGetLaunchCommandBypassWritesSettings(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	dataDir := t.TempDir()
	settingsPath := runtimeSettingsPath(dataDir, "mer-2")

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:   "mer-2",
		Prompt:      "refactor auth",
		Permissions: ports.PermissionModeBypassPermissions,
		DataDir:     dataDir,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"droid", "--settings", settingsPath, "refactor auth"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	var parsed struct {
		SessionDefaultSettings struct {
			AutonomyLevel string `json:"autonomyLevel"`
		} `json:"sessionDefaultSettings"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse settings file: %v", err)
	}
	if parsed.SessionDefaultSettings.AutonomyLevel != "high" {
		t.Fatalf("autonomyLevel = %q, want high", parsed.SessionDefaultSettings.AutonomyLevel)
	}
}

func TestGetLaunchCommandModelOnlyDefaultPermissions(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	dataDir := t.TempDir()
	settingsPath := runtimeSettingsPath(dataDir, "mer-model-1")

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID: "mer-model-1",
		Prompt:    "use opus",
		Config:    ports.AgentConfig{Model: "claude-opus-4-5"},
		DataDir:   dataDir,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"droid", "--settings", settingsPath, "use opus"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse settings file: %v", err)
	}
	var model string
	if err := json.Unmarshal(parsed["model"], &model); err != nil {
		t.Fatalf("model field: %v", err)
	}
	if model != "claude-opus-4-5" {
		t.Fatalf("model = %q, want claude-opus-4-5", model)
	}
	if _, ok := parsed["sessionDefaultSettings"]; ok {
		t.Fatalf("default permissions should not write sessionDefaultSettings, got %s", data)
	}
}

func TestGetLaunchCommandModelAndNonDefaultPermission(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	dataDir := t.TempDir()
	settingsPath := runtimeSettingsPath(dataDir, "mer-model-2")

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:   "mer-model-2",
		Prompt:      "refactor with opus",
		Permissions: ports.PermissionModeBypassPermissions,
		Config:      ports.AgentConfig{Model: "claude-opus-4-5"},
		DataDir:     dataDir,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"droid", "--settings", settingsPath, "refactor with opus"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	var parsed struct {
		Model                  string `json:"model"`
		SessionDefaultSettings struct {
			AutonomyLevel string `json:"autonomyLevel"`
		} `json:"sessionDefaultSettings"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse settings file: %v", err)
	}
	if parsed.Model != "claude-opus-4-5" {
		t.Fatalf("model = %q, want claude-opus-4-5", parsed.Model)
	}
	if parsed.SessionDefaultSettings.AutonomyLevel != "high" {
		t.Fatalf("autonomyLevel = %q, want high", parsed.SessionDefaultSettings.AutonomyLevel)
	}
}

func TestGetRestoreCommandForwardsModel(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	dataDir := t.TempDir()
	settingsPath := runtimeSettingsPath(dataDir, "mer-model-3")

	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Config:  ports.AgentConfig{Model: "claude-opus-4-5"},
		DataDir: dataDir,
		Session: ports.SessionRef{
			ID: "mer-model-3",
			Metadata: map[string]string{
				ports.MetadataKeyAgentSessionID: "droid-ses-model",
			},
		},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := []string{"droid", "--settings", settingsPath, "-r", "droid-ses-model"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings file: %v", err)
	}
	var parsed struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse settings file: %v", err)
	}
	if parsed.Model != "claude-opus-4-5" {
		t.Fatalf("model = %q, want claude-opus-4-5", parsed.Model)
	}
}

func TestGetLaunchCommandAutonomyLevels(t *testing.T) {
	for _, tc := range []struct {
		mode  ports.PermissionMode
		level string
	}{
		{ports.PermissionModeAcceptEdits, "low"},
		{ports.PermissionModeAuto, "medium"},
		{ports.PermissionModeBypassPermissions, "high"},
	} {
		if got := droidAutonomyLevel(tc.mode); got != tc.level {
			t.Fatalf("droidAutonomyLevel(%q) = %q, want %q", tc.mode, got, tc.level)
		}
	}
	if got := droidAutonomyLevel(ports.PermissionModeDefault); got != "" {
		t.Fatalf("default autonomy = %q, want empty", got)
	}
}

func TestGetLaunchCommandSystemPrompt(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:    "mer-3",
		Prompt:       "fix it",
		SystemPrompt: "follow AGENTS.md",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"droid", "--append-system-prompt", "follow AGENTS.md", "fix it"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		SystemPrompt:     "restore inline wins",
		SystemPromptFile: "/tmp/system.md",
		Session: ports.SessionRef{
			ID: "mer-4",
			Metadata: map[string]string{
				ports.MetadataKeyAgentSessionID: "droid-ses-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := []string{"droid", "--append-system-prompt", "restore inline wins", "-r", "droid-ses-1"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandNoID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	_, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{}},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("ok=true with no agentSessionId, want false")
	}
}

func TestSessionInfoReadsHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "droid-ses-1",
			ports.MetadataKeyTitle:          "Fix login redirect",
			ports.MetadataKeySummary:        "Updated the auth callback and tests.",
		},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if info.AgentSessionID != "droid-ses-1" {
		t.Fatalf("AgentSessionID = %q", info.AgentSessionID)
	}
	if info.Title != "Fix login redirect" {
		t.Fatalf("Title = %q", info.Title)
	}
	if info.Summary != "Updated the auth callback and tests." {
		t.Fatalf("Summary = %q", info.Summary)
	}
}

func TestSessionInfoFalseWhenNoHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("ok=true with empty metadata, want false")
	}
	if !reflect.DeepEqual(info, ports.SessionInfo{}) {
		t.Fatalf("info = %#v, want zero", info)
	}
}

func TestGetAgentHooksInstallsIntoFactoryHooksJSON(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	ws := t.TempDir()
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: ws,
		SessionID:     "mer-5",
	}); err != nil {
		t.Fatalf("GetAgentHooks: %v", err)
	}

	data, err := os.ReadFile(droidHooksPath(ws))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	body := string(data)
	for _, spec := range droidManagedHooks {
		if !strings.Contains(body, spec.Command) {
			t.Fatalf("hooks.json missing managed command %q:\n%s", spec.Command, body)
		}
	}
	if !strings.Contains(body, `"startup"`) {
		t.Fatalf("SessionStart hook missing startup matcher:\n%s", body)
	}
	// Droid's standalone hooks.json is keyed directly by event name; it ignores
	// events nested under a "hooks" object.
	var events map[string]json.RawMessage
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	if _, wrapped := events["hooks"]; wrapped {
		t.Fatalf("hooks.json nests events under \"hooks\":\n%s", body)
	}
	for _, spec := range droidManagedHooks {
		if _, ok := events[spec.Event]; !ok {
			t.Fatalf("hooks.json missing top-level %s event:\n%s", spec.Event, body)
		}
	}

	installed, err := plugin.AreHooksInstalled(context.Background(), ws)
	if err != nil {
		t.Fatalf("AreHooksInstalled: %v", err)
	}
	if !installed {
		t.Fatal("AreHooksInstalled=false after install, want true")
	}
}

func TestGetAgentHooksIdempotentAndPreservesUserHooks(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	ws := t.TempDir()
	// Seed a user-defined hook AO must preserve.
	if err := os.MkdirAll(droidHooksPath(ws)[:len(droidHooksPath(ws))-len(droidHooksFileName)], 0o750); err != nil {
		t.Fatal(err)
	}
	seed := `{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}`
	if err := os.WriteFile(droidHooksPath(ws), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: ws}); err != nil {
			t.Fatalf("GetAgentHooks #%d: %v", i, err)
		}
	}

	data, err := os.ReadFile(droidHooksPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "echo mine") {
		t.Fatalf("user hook dropped:\n%s", body)
	}
	// The AO stop command must appear exactly once despite two installs.
	if n := strings.Count(body, droidHookCommandPrefix+"stop"); n != 1 {
		t.Fatalf("AO stop command count = %d, want 1 (idempotent):\n%s", n, body)
	}
}

func TestUninstallHooksRemovesAOHooksLeavesUserHooks(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	ws := t.TempDir()
	dir := droidHooksPath(ws)[:len(droidHooksPath(ws))-len(droidHooksFileName)]
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	seed := `{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}`
	if err := os.WriteFile(droidHooksPath(ws), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: ws}); err != nil {
		t.Fatal(err)
	}
	if err := plugin.UninstallHooks(context.Background(), ws); err != nil {
		t.Fatalf("UninstallHooks: %v", err)
	}

	data, err := os.ReadFile(droidHooksPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.Contains(body, droidHookCommandPrefix) {
		t.Fatalf("AO hooks not removed:\n%s", body)
	}
	if !strings.Contains(body, "echo mine") {
		t.Fatalf("user hook dropped on uninstall:\n%s", body)
	}

	installed, err := plugin.AreHooksInstalled(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("AreHooksInstalled=true after uninstall, want false")
	}
}

func TestGetAgentHooksMigratesWrappedHooksFile(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "droid"}
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(droidHooksPath(ws)), 0o750); err != nil {
		t.Fatal(err)
	}
	// Earlier AO releases wrote events under "hooks", which Droid never ran.
	seed := `{
  "Stop": [{"hooks": [{"type": "command", "command": "echo top"}]}],
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "ao hooks droid stop", "timeout": 30}, {"type": "command", "command": "echo wrapped"}]}],
    "PreToolUse": [{"matcher": "Execute", "hooks": [{"type": "command", "command": "echo pre"}]}]
  }
}`
	if err := os.WriteFile(droidHooksPath(ws), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: ws}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(droidHooksPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	if _, wrapped := events["hooks"]; wrapped {
		t.Fatalf("legacy \"hooks\" wrapper kept:\n%s", data)
	}
	body := string(data)
	for _, want := range []string{"echo top", "echo wrapped", "echo pre"} {
		if !strings.Contains(body, want) {
			t.Fatalf("user hook %q dropped:\n%s", want, body)
		}
	}
	if n := strings.Count(body, droidHookCommandPrefix+"stop"); n != 1 {
		t.Fatalf("AO stop command count = %d, want 1:\n%s", n, body)
	}
	if _, ok := events["PreToolUse"]; !ok {
		t.Fatalf("wrapped PreToolUse not moved to the top level:\n%s", body)
	}
}
