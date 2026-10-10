package neovate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLaunchPreservesTaskAndSeparatesInstructions(t *testing.T) {
	workspace := t.TempDir()
	for _, tc := range []struct {
		mode ports.PermissionMode
		want string
	}{
		{ports.PermissionModeDefault, "default"}, {ports.PermissionModeAcceptEdits, "autoEdit"},
		{ports.PermissionModeAuto, "autoEdit"}, {ports.PermissionModeBypassPermissions, "yolo"},
	} {
		cmd, err := (&Plugin{resolvedBinary: "neovate"}).GetLaunchCommand(context.Background(), ports.LaunchConfig{
			WorkspacePath: workspace, SessionID: "ao-session", Permissions: tc.mode,
			Prompt: "--help\n$(touch nope) 'quoted'", SystemPrompt: "hidden-standing-instructions", Config: ports.AgentConfig{Model: "local/custom"},
		})
		want := []string{"neovate", "--cwd", workspace, "--approval-mode", tc.want, "--plugin", pluginPath(workspace, "ao-session"), "--model", "local/custom", "--", "--help\n$(touch nope) 'quoted'"}
		if err != nil || !reflect.DeepEqual(cmd, want) {
			t.Fatalf("command = %#v, %v; want %#v", cmd, err, want)
		}
	}
}

func seedTranscript(t *testing.T, workspace, session, id string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), id+".jsonl")
	writeTestFile(t, path, `{"type":"message","role":"user","uuid":"message-1","sessionId":"`+id+`","content":"original task"}`+"\n")
	marker, _ := json.Marshal(sessionMarker{NativeID: id, Workspace: workspace, Transcript: path})
	writeTestFile(t, pluginPath(workspace, session)+".session.json", string(marker))
	return path
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRequiresExactWorkspaceAndIntactNativeHistory(t *testing.T) {
	workspace := t.TempDir()
	path := seedTranscript(t, workspace, "ao-session", "a1b2c3d4")
	cfg := ports.RestoreConfig{Session: ports.SessionRef{ID: "ao-session", WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "a1b2c3d4"}}, Prompt: "follow up"}
	plugin := &Plugin{resolvedBinary: "neovate"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok || !strings.Contains(strings.Join(cmd, " "), "--resume a1b2c3d4 -- follow up") {
		t.Fatalf("restore = %#v, %v, %v", cmd, ok, err)
	}
	for _, body := range []string{"", "{bad json", `{"type":"config","config":{}}`, `{"type":"message","role":"user","uuid":"m","sessionId":"ffffffff"}`} {
		writeTestFile(t, path, body)
		if _, ok, err := plugin.GetRestoreCommand(context.Background(), cfg); err == nil || ok {
			t.Fatalf("restore accepted invalid transcript %q", body)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := plugin.GetRestoreCommand(context.Background(), cfg); err == nil || ok {
		t.Fatal("missing transcript fell back to fresh launch")
	}
	cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] = "--latest"
	if _, ok, err := plugin.GetRestoreCommand(context.Background(), cfg); err == nil || ok {
		t.Fatal("invalid native ID accepted")
	}
}

func TestRestoreRejectsWorkspaceMismatch(t *testing.T) {
	workspace := t.TempDir()
	seedTranscript(t, workspace, "ao-session", "a1b2c3d4")
	marker, _ := json.Marshal(sessionMarker{NativeID: "a1b2c3d4", Workspace: t.TempDir(), Transcript: "/unrelated"})
	writeTestFile(t, pluginPath(workspace, "ao-session")+".session.json", string(marker))
	if err := validateRestore(workspace, "ao-session", "a1b2c3d4", ports.PermissionModeDefault); err == nil {
		t.Fatal("foreign workspace accepted")
	}
}

func TestHooksAreIdempotentAndPreserveUserConfiguration(t *testing.T) {
	workspace := t.TempDir()
	userConfig := filepath.Join(workspace, ".neovate", "config.json")
	writeTestFile(t, userConfig, `{"plugins":["my-plugin"]}`)
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, SessionID: "ao-session", SystemPrompt: "hidden token\n\"quotes\""}
	p := New()
	for range 2 {
		if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(pluginPath(workspace, cfg.SessionID))
	if err != nil || !strings.Contains(string(data), "hidden token") || strings.Contains(string(data), "__AO_CONFIG__") {
		t.Fatalf("plugin = %s, %v", data, err)
	}
	if ok, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || !ok {
		t.Fatalf("hooks = %v, %v", ok, err)
	}
	if err := p.UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(userConfig)
	if err != nil || string(data) != `{"plugins":["my-plugin"]}` {
		t.Fatalf("user config changed: %s, %v", data, err)
	}
}

func TestRefuseToOverwriteForeignPlugin(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, pluginPath(workspace, "session"), "// user code")
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, SessionID: "session"}); err == nil {
		t.Fatal("overwrote user plugin")
	}
}

func TestAuthPresenceIsNotAuthorization(t *testing.T) {
	for _, key := range credentialEnv {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := New()
	if got, err := p.AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusUnknown {
		t.Fatalf("empty auth = %q, %v", got, err)
	}
	writeTestFile(t, filepath.Join(home, ".neovate", "config.json"), `{"provider":{"local":{"options":{"apiKey":"configured-only"}}}}`)
	if got, err := p.AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusConfigured {
		t.Fatalf("configured auth = %q, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.AuthStatus(ctx); err == nil {
		t.Fatal("canceled auth accepted")
	}
}

func TestActivityDoesNotInferPermissions(t *testing.T) {
	for _, event := range []string{"permission", "tool-start", "unknown"} {
		if _, ok := DeriveActivityState(event, nil); ok {
			t.Fatalf("inferred state for %q", event)
		}
	}
	if state, ok := DeriveActivityState("user-prompt-submit", nil); !ok || state != domain.ActivityActive {
		t.Fatal("missing native submit state")
	}
	if state, ok := DeriveActivityState("stop", nil); !ok || state != domain.ActivityIdle {
		t.Fatal("missing native stop state")
	}
}

func TestRejectsNativeCommandTasksAndToolRestrictions(t *testing.T) {
	p := &Plugin{resolvedBinary: "neovate"}
	for _, task := range []string{"server", "config", "!touch unintended", "/login"} {
		_, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: t.TempDir(), SessionID: "session", Prompt: task})
		if err == nil {
			t.Fatalf("accepted native command task %q", task)
		}
	}
	_, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{AllowedTools: []string{"read"}})
	if err == nil {
		t.Fatal("accepted unsupported reviewer tool restriction")
	}
}

func TestRestoreRejectsSavedApprovalsBroaderThanRequestedMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		saved     nativePermissionPolicy
		requested ports.PermissionMode
		wantError bool
	}{
		{"default", nativePermissionPolicy{ApprovalMode: "default"}, ports.PermissionModeDefault, false},
		{"saved autoEdit", nativePermissionPolicy{ApprovalMode: "autoEdit"}, ports.PermissionModeDefault, true},
		{"explicit edits", nativePermissionPolicy{ApprovalMode: "autoEdit"}, ports.PermissionModeAcceptEdits, false},
		{"saved tool default", nativePermissionPolicy{ApprovalTools: []string{"bash"}}, ports.PermissionModeDefault, true},
		{"saved tool edits", nativePermissionPolicy{ApprovalTools: []string{"bash"}}, ports.PermissionModeAcceptEdits, true},
		{"saved tool auto", nativePermissionPolicy{ApprovalTools: []string{"bash"}}, ports.PermissionModeAuto, true},
		{"explicit bypass", nativePermissionPolicy{ApprovalMode: "autoEdit", ApprovalTools: []string{"bash"}}, ports.PermissionModeBypassPermissions, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			path := seedTranscript(t, workspace, "ao-session", "a1b2c3d4")
			history, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			config, err := json.Marshal(map[string]any{"type": "config", "config": tc.saved})
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, path, string(config)+"\n"+string(history))
			_, ok, err := (&Plugin{resolvedBinary: "neovate"}).GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{ID: "ao-session", WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "a1b2c3d4"}}, Permissions: tc.requested})
			if (err != nil) != tc.wantError || ok == tc.wantError {
				t.Fatalf("restore = %v, %v", ok, err)
			}
		})
	}
}
