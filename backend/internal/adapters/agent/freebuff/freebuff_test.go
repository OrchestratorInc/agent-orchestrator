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

func readSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func hookConfig(t *testing.T, session string, model, effort string, env map[string]string) ports.WorkspaceHookConfig {
	t.Helper()
	return ports.WorkspaceHookConfig{
		SessionID: session, DataDir: t.TempDir(), WorkspacePath: t.TempDir(), Env: env,
		Config: ports.AgentConfig{Model: model, Effort: effort},
	}
}

func TestHooksApplyModelAndEffortToSettings(t *testing.T) {
	dir := isolateConfig(t)
	existing := `{"mode":"DEFAULT","adsEnabled":false,"freebuffModelKey":"stale-row","freebuffReasoningEfforts":{"openai/gpt-6-luna":"low"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().GetAgentHooks(context.Background(), hookConfig(t, "s1", "z-ai/glm-5.3-flash", "high", nil)); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, dir)
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

func TestHooksStampDefaultMigrationSoExplicitPickSurvives(t *testing.T) {
	dir := isolateConfig(t)
	// Freebuff migrates an unstamped previous default (DeepSeek V4.1 Flash) to
	// its new default on startup; the stamp keeps AO's explicit choice.
	if err := New().GetAgentHooks(context.Background(), hookConfig(t, "s1", "deepseek/deepseek-v4-flash", "", nil)); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, dir)
	if settings["freebuffModelDefaultMigration"] != defaultModelMigrationID || settings["freebuffModel"] != "deepseek/deepseek-v4-flash" {
		t.Fatalf("settings = %v", settings)
	}
	// An existing stamp, possibly from a newer Freebuff, is left alone.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"freebuffModelDefaultMigration":"newer"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().GetAgentHooks(context.Background(), hookConfig(t, "s2", "deepseek/deepseek-v4-flash", "", nil)); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(t, dir)["freebuffModelDefaultMigration"]; got != "newer" {
		t.Fatalf("stamp = %v", got)
	}
}

func TestHooksTreatNonObjectSettingsAsEmpty(t *testing.T) {
	dir := isolateConfig(t)
	for _, content := range []string{"null", "[]", "3"} {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := New().GetAgentHooks(context.Background(), hookConfig(t, "s1", "z-ai/glm-5.3-flash", "", nil)); err != nil {
			t.Fatalf("%s: %v", content, err)
		}
		if got := readSettings(t, dir)["freebuffModel"]; got != "z-ai/glm-5.3-flash" {
			t.Fatalf("%s: freebuffModel = %v", content, got)
		}
	}
}

func TestHooksClearSavedEffortWhenNoneRequested(t *testing.T) {
	dir := isolateConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"freebuffReasoningEfforts":{"z-ai/glm-5.3-flash":"low"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().GetAgentHooks(context.Background(), hookConfig(t, "s1", "z-ai/glm-5.3-flash", "", nil)); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSettings(t, dir)["freebuffReasoningEfforts"]; ok {
		t.Fatal("a stale saved effort must not override the model default")
	}
}

func TestHooksUseSessionConfigDirAndRecordItForRestore(t *testing.T) {
	daemonDir := isolateConfig(t)
	projectDir := t.TempDir()
	cfg := hookConfig(t, "s1", "z-ai/glm-5.3-flash", "", map[string]string{configDirEnv: projectDir})
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(t, projectDir)["freebuffModel"]; got != "z-ai/glm-5.3-flash" {
		t.Fatalf("project settings model = %v", got)
	}
	if _, err := os.Stat(filepath.Join(daemonDir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("daemon-level settings must stay untouched, stat err = %v", err)
	}
	// Restore receives no env; the recorded launch directory still finds the chat.
	workspace := t.TempDir()
	writeChat(t, projectDir, workspace, "chat-1", workspace, time.Now())
	cmd, ok, err := pluginWithBinary(t).GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: cfg.DataDir, Session: ports.SessionRef{ID: "s1", WorkspacePath: workspace},
	})
	if err != nil || !ok || cmd[len(cmd)-1] != "chat-1" {
		t.Fatalf("cmd=%q ok=%v err=%v", cmd, ok, err)
	}
}

func TestLaunchAndRestoreCommandsHaveNoSideEffects(t *testing.T) {
	dir := isolateConfig(t)
	p := pluginWithBinary(t)
	cfg := ports.AgentConfig{Model: "z-ai/glm-5.3-flash", Effort: "high"}
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: t.TempDir(), Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: t.TempDir()}, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json should not be written by command builders, stat err = %v", err)
	}
}

func TestModelLeaseKeepsConcurrentLaunchesOnTheirOwnModel(t *testing.T) {
	dir := isolateConfig(t)
	p := New()
	p.leases.poll = 5 * time.Millisecond
	ctx := context.Background()
	if err := p.GetAgentHooks(ctx, hookConfig(t, "a", "z-ai/glm-5.3-flash", "", nil)); err != nil {
		t.Fatal(err)
	}
	// Same selection shares the lease without waiting.
	if err := p.GetAgentHooks(ctx, hookConfig(t, "b", "z-ai/glm-5.3-flash", "", nil)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	waiting := hookConfig(t, "c", "openai/gpt-6-luna", "", nil)
	go func() { done <- p.GetAgentHooks(ctx, waiting) }()
	select {
	case err := <-done:
		t.Fatalf("different model replaced a starting session's selection: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := readSettings(t, dir)["freebuffModel"]; got != "z-ai/glm-5.3-flash" {
		t.Fatalf("model changed while a launch was starting: %v", got)
	}
	p.AgentStartupReady("a")
	p.AgentStartupReady("b")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := readSettings(t, dir)["freebuffModel"]; got != "openai/gpt-6-luna" {
		t.Fatalf("waiting launch did not apply its model: %v", got)
	}
}

func TestModelLeaseExpires(t *testing.T) {
	isolateConfig(t)
	p := New()
	clock := time.Now()
	p.leases.now = func() time.Time { return clock }
	p.leases.poll = time.Millisecond
	if err := p.GetAgentHooks(context.Background(), hookConfig(t, "a", "z-ai/glm-5.3-flash", "", nil)); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(modelLeaseTTL + time.Second)
	if err := p.GetAgentHooks(context.Background(), hookConfig(t, "b", "openai/gpt-6-luna", "", nil)); err != nil {
		t.Fatalf("expired lease still blocked a launch: %v", err)
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
		"token":    {`{"default":{"email":"a@b.c","authToken":"tok"}}`, ports.AgentAuthStatusConfigured},
		"keychain": {`{"default":{"email":"a@b.c","tokenStore":"keychain"}}`, ports.AgentAuthStatusConfigured},
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

func TestAPIKeyEnvironmentIsConfiguredNotVerified(t *testing.T) {
	dir := isolateConfig(t)
	t.Setenv("CODEBUFF_API_KEY", "key")
	if got, err := localAuthStatus(context.Background(), dir); err != nil || got != ports.AgentAuthStatusConfigured {
		t.Fatalf("status=%v err=%v", got, err)
	}
}

func TestTerminalActivity(t *testing.T) {
	p := New()
	idle := "Freebuff\n\n│ > Enter a coding task or / for commands │\n\nunlimited\n"
	if state, ok := p.DetectTerminalActivity(idle); !ok || state != domain.ActivityIdle {
		t.Fatalf("idle = %v %v", state, ok)
	}
	if !p.ComposerIsEmpty(idle) {
		t.Fatal("idle composer should be empty")
	}
	active := "> Add to the current task (/ for commands)\nworking...   12s  ■ Esc\n"
	if state, ok := p.DetectTerminalActivity(active); !ok || state != domain.ActivityActive {
		t.Fatalf("active = %v %v", state, ok)
	}
	running := "my draft text\n⠋ thinking...   3s  ■ Esc\n"
	if state, ok := p.DetectTerminalActivity(running); !ok || state != domain.ActivityActive {
		t.Fatalf("status-bar running = %v %v", state, ok)
	}
	if _, ok := p.DetectTerminalActivity("loading\n"); ok {
		t.Fatal("unknown screen should not report a state")
	}
}

func TestTerminalActivityIgnoresMarkersInTranscriptAndDraft(t *testing.T) {
	p := New()
	// A finished answer quoting the status text, above the real idle composer.
	transcript := "The bar said working... and thinking... then ■ Esc appeared.\nworking...\nAdd to the current task was the hint.\n│ > Enter a coding task or / for commands │\n"
	if state, ok := p.DetectTerminalActivity(transcript); !ok || state != domain.ActivityIdle {
		t.Fatalf("transcript markers = %v %v; want idle", state, ok)
	}
	// A human draft that mentions a marker is neither idle nor active.
	draft := "answer\n│ > please mention working... in the docs │\n"
	if _, ok := p.DetectTerminalActivity(draft); ok {
		t.Fatal("draft text must not become activity")
	}
	if p.ComposerIsEmpty(draft) {
		t.Fatal("a composer holding a draft is not empty")
	}
}

func TestAfterStartSubmitState(t *testing.T) {
	p := New()
	prompt := "## AO Standing Instructions\n\nBe careful.\n\n## AO Task\n\nFix the flaky login redirect test"
	draft := "Freebuff\n│ > ## AO Standing Instructions Be careful. ## AO Task Fix the flaky │\n│ login redirect test                                               │\nunlimited\n"
	if got := p.AfterStartSubmitState(draft, prompt); got != ports.AfterStartDraftPending {
		t.Fatalf("draft = %v", got)
	}
	running := "Fix the flaky login redirect test\n│ > Add to the current task │\nworking... ■ Esc\n"
	if got := p.AfterStartSubmitState(running, prompt); got != ports.AfterStartSubmitted {
		t.Fatalf("running = %v", got)
	}
	done := "Fix the flaky login redirect test\nDone.\n│ > Enter a coding task │\n"
	if got := p.AfterStartSubmitState(done, prompt); got != ports.AfterStartSubmitted {
		t.Fatalf("finished = %v", got)
	}
	dialog := "Which file?\n  1. a.go\n  2. b.go\n"
	if got := p.AfterStartSubmitState(dialog, prompt); got != ports.AfterStartSubmitUnknown {
		t.Fatalf("dialog = %v", got)
	}
	if !p.PrimesAfterStartSubmit() {
		t.Fatal("freebuff needs the priming Enter")
	}
}
