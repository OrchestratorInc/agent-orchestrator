package freebuff

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func pluginWithBinary(t *testing.T) *Plugin {
	t.Helper()
	p := New()
	p.resolvedBinary = "/opt/bin/freebuff"
	return p
}

func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(configDirEnv, dir)
	t.Setenv("CODEBUFF_API_KEY", "")
	return dir
}

func TestManifestIdentifiesFreebuff(t *testing.T) {
	m := New().Manifest()
	if m.ID != "freebuff" || m.Name != "Freebuff" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestLaunchCommandKeepsTaskOutOfArgv(t *testing.T) {
	isolateConfig(t)
	workspace := t.TempDir()
	cmd, err := pluginWithBinary(t).GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: workspace,
		Prompt:        "--not-a-flag",
		Permissions:   ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/bin/freebuff", "--cwd", workspace}
	if strings.Join(cmd, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("cmd = %q, want %q", cmd, want)
	}
}

func TestLaunchCommandRejectsRelativeWorkspace(t *testing.T) {
	isolateConfig(t)
	if _, err := pluginWithBinary(t).GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: "relative"}); err == nil {
		t.Fatal("expected relative workspace to be rejected")
	}
}

func TestLaunchAppliesModelAndEffortToSettings(t *testing.T) {
	dir := isolateConfig(t)
	existing := `{"mode":"DEFAULT","adsEnabled":false,"freebuffModelKey":"stale-row","freebuffReasoningEfforts":{"openai/gpt-6-luna":"low"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := pluginWithBinary(t).GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: t.TempDir(),
		Config:        ports.AgentConfig{Model: "z-ai/glm-5.3-flash", Effort: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["freebuffModel"] != "z-ai/glm-5.3-flash" {
		t.Fatalf("freebuffModel = %v", settings["freebuffModel"])
	}
	if _, ok := settings["freebuffModelKey"]; ok {
		t.Fatal("stale catalog key must be cleared so the model id wins")
	}
	if settings["adsEnabled"] != false {
		t.Fatal("unrelated user settings must be preserved")
	}
	efforts := settings["freebuffReasoningEfforts"].(map[string]any)
	if efforts["z-ai/glm-5.3-flash"] != "high" || efforts["openai/gpt-6-luna"] != "low" {
		t.Fatalf("efforts = %v", efforts)
	}
}

func TestLaunchWithoutModelLeavesSettingsUntouched(t *testing.T) {
	dir := isolateConfig(t)
	if _, err := pluginWithBinary(t).GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json should not be created, stat err = %v", err)
	}
}

func TestBuildAfterStartPromptCarriesStandingInstructions(t *testing.T) {
	p := New()
	got, err := p.BuildAfterStartPrompt(context.Background(), ports.LaunchConfig{SystemPrompt: "Be careful.", Prompt: "Fix the bug."})
	if err != nil {
		t.Fatal(err)
	}
	if got != "## AO Standing Instructions\n\nBe careful.\n\n## AO Task\n\nFix the bug." {
		t.Fatalf("prompt = %q", got)
	}
	got, _ = p.BuildAfterStartPrompt(context.Background(), ports.LaunchConfig{Prompt: "Fix the bug."})
	if got != "Fix the bug." {
		t.Fatalf("prompt without instructions = %q", got)
	}
	file := filepath.Join(t.TempDir(), "standing.md")
	if err := os.WriteFile(file, []byte("From file.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = p.BuildAfterStartPrompt(context.Background(), ports.LaunchConfig{SystemPromptFile: file})
	if !strings.HasPrefix(got, "## AO Standing Instructions\n\nFrom file.") || strings.Contains(got, "## AO Task") {
		t.Fatalf("promptless prompt = %q", got)
	}
}

func writeChat(t *testing.T, dir, workspace, id, projectRoot string, mod time.Time) {
	t.Helper()
	chat := filepath.Join(chatsDir(dir, workspace), id)
	if err := os.MkdirAll(chat, 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"sessionState":{"fileContext":{"projectRoot":` + jsonString(projectRoot) + `}}}`
	if err := os.WriteFile(filepath.Join(chat, "run-state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(chat, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestRestoreContinuesNewestChatOfThisWorkspace(t *testing.T) {
	dir := isolateConfig(t)
	workspace := filepath.Join(t.TempDir(), "proj")
	other := filepath.Join(t.TempDir(), "proj")
	for _, ws := range []string{workspace, other} {
		if err := os.MkdirAll(ws, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	writeChat(t, dir, workspace, "2026-10-20T10-00-00.000Z", workspace, now.Add(-2*time.Hour))
	writeChat(t, dir, workspace, "2026-10-20T11-00-00.000Z", workspace, now.Add(-time.Hour))
	// Same basename, different project: newest but must not be chosen.
	writeChat(t, dir, other, "2026-10-20T12-00-00.000Z", other, now)

	cmd, ok, err := pluginWithBinary(t).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{WorkspacePath: workspace},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	want := []string{"/opt/bin/freebuff", "--cwd", workspace, "--continue", "2026-10-20T11-00-00.000Z"}
	if strings.Join(cmd, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("cmd = %q, want %q", cmd, want)
	}
}

func TestRestorePrefersRecordedChatID(t *testing.T) {
	dir := isolateConfig(t)
	workspace := t.TempDir()
	now := time.Now()
	writeChat(t, dir, workspace, "older", workspace, now.Add(-time.Hour))
	writeChat(t, dir, workspace, "newer", workspace, now)
	cmd, ok, err := pluginWithBinary(t).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "older"}},
	})
	if err != nil || !ok || cmd[len(cmd)-1] != "older" {
		t.Fatalf("cmd=%q ok=%v err=%v", cmd, ok, err)
	}
}

func TestRestoreWithoutChatFallsBackToFreshLaunch(t *testing.T) {
	isolateConfig(t)
	_, ok, err := pluginWithBinary(t).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{WorkspacePath: t.TempDir()},
	})
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v; want fresh-launch fallback", ok, err)
	}
}

func TestSessionInfoReportsChatID(t *testing.T) {
	dir := isolateConfig(t)
	workspace := t.TempDir()
	writeChat(t, dir, workspace, "chat-1", workspace, time.Now())
	info, ok, err := New().SessionInfo(context.Background(), ports.SessionRef{WorkspacePath: workspace})
	if err != nil || !ok || info.AgentSessionID != "chat-1" {
		t.Fatalf("info=%+v ok=%v err=%v", info, ok, err)
	}
}

func TestCredentialsAuthStatus(t *testing.T) {
	cases := map[string]struct {
		file string
		want ports.AgentAuthStatus
	}{
		"missing":  {"", ports.AgentAuthStatusUnauthorized},
		"token":    {`{"default":{"email":"a@b.c","authToken":"tok"}}`, ports.AgentAuthStatusAuthorized},
		"keychain": {`{"default":{"email":"a@b.c","tokenStore":"keychain"}}`, ports.AgentAuthStatusAuthorized},
		"empty":    {`{"default":{"email":"a@b.c"}}`, ports.AgentAuthStatusUnauthorized},
		"garbage":  {`not json`, ports.AgentAuthStatusUnauthorized},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := isolateConfig(t)
			if tc.file != "" {
				if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := localAuthStatus(context.Background(), dir)
			if err != nil || got != tc.want {
				t.Fatalf("status=%v err=%v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestAPIKeyEnvironmentAuthorizes(t *testing.T) {
	dir := isolateConfig(t)
	t.Setenv("CODEBUFF_API_KEY", "key")
	if got, err := localAuthStatus(context.Background(), dir); err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status=%v err=%v", got, err)
	}
}

func TestTerminalActivity(t *testing.T) {
	p := New()
	idle := "Freebuff\n\n> Enter a coding task or / for commands\n\nunlimited\n"
	if state, ok := p.DetectTerminalActivity(idle); !ok || state != domain.ActivityIdle {
		t.Fatalf("idle = %v %v", state, ok)
	}
	if !p.ComposerIsEmpty(idle) {
		t.Fatal("idle composer should be empty")
	}
	active := "transcript mentions Enter a coding task\n> Add to the current task (/ for commands)\nworking...   12s  ■ Esc\n"
	if state, ok := p.DetectTerminalActivity(active); !ok || state != domain.ActivityActive {
		t.Fatalf("active = %v %v", state, ok)
	}
	if _, ok := p.DetectTerminalActivity("loading\n"); ok {
		t.Fatal("unknown screen should not report a state")
	}
}
