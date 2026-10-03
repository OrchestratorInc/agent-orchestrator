package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newWorkspace returns a workspace dir and pins HOME to an empty dir, so the
// tests never read the developer's real global settings.
func newWorkspace(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return t.TempDir()
}

func readWorkspaceSettings(t *testing.T, workspace string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(claudeSettingsPath(workspace))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return settings
}

func TestInstallStatusLineWhenNoneConfigured(t *testing.T) {
	workspace := newWorkspace(t)

	installed, err := InstallStatusLine(workspace)
	if err != nil || !installed {
		t.Fatalf("install = %v, err = %v; want installed", installed, err)
	}
	var setting statusLineSetting
	if err := json.Unmarshal(readWorkspaceSettings(t, workspace)["statusLine"], &setting); err != nil {
		t.Fatalf("parse statusLine: %v", err)
	}
	if setting.Type != "command" || setting.Command != statusLineCommand {
		t.Fatalf("statusLine = %+v", setting)
	}

	if err := UninstallStatusLine(workspace); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := readWorkspaceSettings(t, workspace)["statusLine"]; ok {
		t.Fatal("uninstall left AO's statusLine behind")
	}
}

func TestInstallStatusLineNeverReplacesTheUsersOwn(t *testing.T) {
	workspace := newWorkspace(t)
	settingsPath := claudeSettingsPath(workspace)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	theirs := `{"statusLine":{"type":"command","command":"my-own-statusline"},"model":"opus"}`
	if err := os.WriteFile(settingsPath, []byte(theirs), 0o600); err != nil {
		t.Fatal(err)
	}

	installed, err := InstallStatusLine(workspace)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if installed {
		t.Fatal("reported AO ownership of the user's statusLine")
	}

	settings := readWorkspaceSettings(t, workspace)
	var setting statusLineSetting
	if err := json.Unmarshal(settings["statusLine"], &setting); err != nil {
		t.Fatal(err)
	}
	if setting.Command != "my-own-statusline" {
		t.Fatalf("overwrote the user's statusLine: %+v", setting)
	}

	// Uninstall must leave a setting AO does not own alone.
	if err := UninstallStatusLine(workspace); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := readWorkspaceSettings(t, workspace)["statusLine"]; !ok {
		t.Fatal("uninstall deleted the user's own statusLine")
	}
}

func TestInstallStatusLineSkipsWhenGlobalStatusLineExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, claudeSettingsDirName)
	if err := os.MkdirAll(globalDir, 0o750); err != nil {
		t.Fatal(err)
	}
	global := `{"statusLine":{"type":"command","command":"their-global-line"}}`
	if err := os.WriteFile(filepath.Join(globalDir, "settings.json"), []byte(global), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	installed, err := InstallStatusLine(workspace)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if installed {
		t.Fatal("overrode a globally configured statusLine")
	}
	if _, err := os.Stat(claudeSettingsPath(workspace)); !os.IsNotExist(err) {
		t.Fatalf("wrote workspace settings anyway: %v", err)
	}
}

func TestInstallStatusLinePreservesUnrelatedSettings(t *testing.T) {
	workspace := newWorkspace(t)
	settingsPath := claudeSettingsPath(workspace)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		t.Fatal(err)
	}
	existing := `{"model":"opus","hooks":{"Stop":[]}}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallStatusLine(workspace); err != nil {
		t.Fatalf("install: %v", err)
	}
	settings := readWorkspaceSettings(t, workspace)
	for _, key := range []string{"model", "hooks", "statusLine"} {
		if _, ok := settings[key]; !ok {
			t.Errorf("key %q missing after install", key)
		}
	}
}
