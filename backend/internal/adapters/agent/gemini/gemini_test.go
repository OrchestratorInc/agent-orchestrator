package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeHookActivity(t *testing.T) {
	for _, tc := range []struct {
		event, payload string
		want           domain.ActivityState
		ok             bool
	}{
		{"session-start", `{}`, domain.ActivityActive, true},
		{"user-prompt-submit", `{}`, domain.ActivityActive, true},
		{"notification", `{"notification_type":"ToolPermission"}`, domain.ActivityWaitingInput, true},
		{"notification", `{"notification_type":"Other"}`, "", false},
		{"notification", `invalid`, "", false},
		{"after-tool", `{}`, domain.ActivityActive, true},
		{"stop", `{}`, domain.ActivityIdle, true},
	} {
		t.Run(tc.event+tc.payload, func(t *testing.T) {
			got, ok := DeriveActivityState(tc.event, []byte(tc.payload))
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got %q,%v", got, ok)
			}
		})
	}
}

func TestAuthStatusUsesCachedAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gemini-key")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_CLI_HOME", t.TempDir())

	status, err := (&Plugin{resolvedBinary: "gemini"}).AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("AuthStatus = (%q, %v), want configured", status, err)
	}
}

func TestAuthStatusUsesCachedOAuthCredentials(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	cliHome := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", cliHome)
	if err := os.Mkdir(filepath.Join(cliHome, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliHome, ".gemini", "oauth_creds.json"), []byte(`{"refresh_token":"cached"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := (&Plugin{resolvedBinary: "gemini"}).AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("AuthStatus = (%q, %v), want configured", status, err)
	}
}

func TestAuthStatusUsesSelectedAPIKeyAuth(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	cliHome := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", cliHome)
	configDir := filepath.Join(cliHome, ".gemini")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := (&Plugin{resolvedBinary: "gemini"}).AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("AuthStatus = (%q, %v), want configured", status, err)
	}
}

func TestAuthStatusPropagatesInvalidSettings(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	cliHome := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", cliHome)
	configDir := filepath.Join(cliHome, ".gemini")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := (&Plugin{resolvedBinary: "gemini"}).AuthStatus(context.Background())
	if status != ports.AgentAuthStatusUnknown || err == nil {
		t.Fatalf("AuthStatus = (%q, %v), want unknown/error", status, err)
	}
}

func TestAuthStatusUnknownWithoutCachedCredentials(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_CLI_HOME", t.TempDir())

	status, err := (&Plugin{resolvedBinary: "gemini"}).AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusUnknown {
		t.Fatalf("AuthStatus = (%q, %v), want unknown", status, err)
	}
}

func TestAuthStatusPropagatesCredentialStatError(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_CLI_HOME", t.TempDir())
	wantErr := errors.New("credential stat failed")
	old := statGeminiCredentialFile
	t.Cleanup(func() { statGeminiCredentialFile = old })
	statGeminiCredentialFile = func(string) (os.FileInfo, error) { return nil, wantErr }

	status, err := (&Plugin{resolvedBinary: "gemini"}).AuthStatus(context.Background())
	if status != ports.AgentAuthStatusUnknown || !errors.Is(err, wantErr) {
		t.Fatalf("AuthStatus = (%q, %v), want unknown/%v", status, err, wantErr)
	}
}

func TestAuthStatusPropagatesHomeResolutionError(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_CLI_HOME", "")
	wantErr := errors.New("home resolution failed")
	old := geminiUserHomeDir
	t.Cleanup(func() { geminiUserHomeDir = old })
	geminiUserHomeDir = func() (string, error) { return "", wantErr }

	status, err := (&Plugin{resolvedBinary: "gemini"}).AuthStatus(context.Background())
	if status != ports.AgentAuthStatusUnknown || !errors.Is(err, wantErr) {
		t.Fatalf("AuthStatus = (%q, %v), want unknown/%v", status, err, wantErr)
	}
}

func TestLaunchAndRestore(t *testing.T) {
	p := &Plugin{resolvedBinary: "gemini"}
	ctx := context.Background()
	cmd, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{Prompt: "-fix this", NativeSessionID: "native-123", Config: ports.AgentConfig{Model: " model "}, Permissions: ports.PermissionModeAcceptEdits})
	want := []string{"gemini", "--skip-trust", "--approval-mode", "auto_edit", "--model", "model", "--session-id", "native-123", "--prompt-interactive", "-fix this"}
	if err != nil || !reflect.DeepEqual(cmd, want) {
		t.Fatalf("launch = %q, %v; want %q", cmd, err, want)
	}
	cmd, ok, err := p.GetRestoreCommand(ctx, ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-123"}}, Prompt: "continue"})
	want = []string{"gemini", "--skip-trust", "--resume", "native-123", "--prompt-interactive", "continue"}
	if err != nil || !ok || !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore = %q, %v, %v", cmd, ok, err)
	}
	if _, ok, err := p.GetRestoreCommand(ctx, ports.RestoreConfig{}); ok || err != nil {
		t.Fatalf("missing identity: %v, %v", ok, err)
	}
}

func TestPermissions(t *testing.T) {
	for _, tc := range []struct {
		mode ports.PermissionMode
		want string
	}{{ports.PermissionModeDefault, ""}, {ports.PermissionModeAcceptEdits, "auto_edit"}, {ports.PermissionModeAuto, "auto_edit"}, {ports.PermissionModeBypassPermissions, "yolo"}, {"unknown", ""}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			cmd, err := (&Plugin{resolvedBinary: "gemini"}).GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: tc.mode})
			want := []string{"gemini", "--skip-trust"}
			if tc.want != "" {
				want = append(want, "--approval-mode", tc.want)
			}
			if err != nil || !reflect.DeepEqual(cmd, want) {
				t.Fatalf("got %q, %v; want %q", cmd, err, want)
			}
		})
	}
}

func TestRejectUnsupportedToolRestrictions(t *testing.T) {
	p := &Plugin{resolvedBinary: "gemini"}
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{DisallowedTools: []string{"shell"}}); err == nil {
		t.Fatal("silently ignored restricted tools")
	}
}

func TestCancelledLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Plugin{resolvedBinary: "gemini"}).GetLaunchCommand(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestHooksPreserveSettingsAndReconcile(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".gemini")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"user","hooks":{"BeforeAgent":[{"hooks":[{"type":"command","command":"user-hook"}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	p := New()
	for range 2 {
		if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: ws}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Theme string
		Hooks map[string][]hooksjson.MatcherGroup
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Theme != "user" || len(got.Hooks["BeforeAgent"]) != 1 || len(got.Hooks["BeforeAgent"][0].Hooks) != 2 || got.Hooks["BeforeAgent"][0].Hooks[0].Command != "user-hook" {
		t.Fatalf("settings = %s", data)
	}
	for _, event := range []string{"SessionStart", "BeforeAgent", "AfterAgent", "Notification", "AfterTool"} {
		if len(got.Hooks[event]) == 0 {
			t.Errorf("missing %s", event)
		}
	}
	if err := p.UninstallHooks(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	if ok, err := p.AreHooksInstalled(context.Background(), ws); err != nil || ok {
		t.Fatalf("uninstall = %v, %v", ok, err)
	}
}
