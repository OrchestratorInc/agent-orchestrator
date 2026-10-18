package codebuddy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLaunchKeepsTaskSeparateFromAdditiveInstructions(t *testing.T) {
	p := &Plugin{binary: "/native/codebuddy"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt: "--resume malicious; $(touch nope)", SystemPrompt: "private\nunchanged\n", Config: ports.AgentConfig{Model: "glm-5.3-flash"},
	})
	want := []string{"/native/codebuddy", "--model", "glm-5.3-flash", "--append-system-prompt", "private\nunchanged\n", "--", "--resume malicious; $(touch nope)"}
	if err != nil || !reflect.DeepEqual(cmd, want) {
		t.Fatalf("argv=%q err=%v", cmd, err)
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: mode}); err == nil {
			t.Fatalf("unqualified permission mode %s accepted", mode)
		}
	}
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "bad\x1b[2J"}); err == nil {
		t.Fatal("terminal controls accepted")
	}
}

func TestInstructionFileIsRequiredBoundedAndExact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.md")
	if err := os.WriteFile(path, []byte("private\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := instructionText("", path)
	if err != nil || s != "private\n\n" {
		t.Fatalf("instructions=%q err=%v", s, err)
	}
	if _, err := instructionText("", path+"-missing"); err == nil {
		t.Fatal("missing private instructions silently ignored")
	}
	for _, text := range []string{strings.Repeat("x", maxInstructions+1), "bad\x00", string([]byte{0xff})} {
		if _, err := instructionText(text, ""); err == nil {
			t.Fatal("invalid/oversized instructions accepted")
		}
	}
}

func historyFixture(t *testing.T, id, cwd string) string {
	t.Helper()
	p := filepath.Join(os.Getenv("CODEBUDDY_CONFIG_DIR"), "projects", "compressed", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, role := range []string{"user", "assistant"} {
		b, err := json.Marshal(map[string]any{"type": "message", "role": role, "sessionId": id, "cwd": cwd, "content": []any{map[string]any{"type": "text", "text": "native history"}}})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExactRestoreRejectsLostCorruptOrWrongWorkspaceHistory(t *testing.T) {
	t.Setenv("CODEBUDDY_CONFIG_DIR", t.TempDir())
	ws := t.TempDir()
	id := "native-id"
	p := &Plugin{binary: "/native/codebuddy"}
	cfg := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: ws, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}, SystemPrompt: "fresh"}
	if _, ok, err := p.GetRestoreCommand(context.Background(), cfg); ok || err == nil {
		t.Fatal("missing history allowed fresh fallback")
	}
	path := historyFixture(t, id, ws)
	cmd, ok, err := p.GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok || !reflect.DeepEqual(cmd, []string{"/native/codebuddy", "--append-system-prompt", "fresh", "--resume", id}) {
		t.Fatalf("restore=%q ok=%v err=%v", cmd, ok, err)
	}
	override := cfg
	override.Env = map[string]string{"CODEBUDDY_CONFIG_DIR": t.TempDir()}
	if _, _, err := p.GetRestoreCommand(context.Background(), override); err == nil {
		t.Fatal("runtime profile override fell back to daemon history")
	}
	wrong := cfg
	wrong.Session.WorkspacePath = t.TempDir()
	if _, _, err := p.GetRestoreCommand(context.Background(), wrong); err == nil {
		t.Fatal("cross-workspace restore accepted")
	}
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.GetRestoreCommand(context.Background(), cfg); err == nil {
		t.Fatal("corrupt history accepted")
	}
	for _, bad := range []string{"", "../native-id", "--continue", "native/id"} {
		cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] = bad
		if _, _, err := p.GetRestoreCommand(context.Background(), cfg); err == nil {
			t.Fatalf("unsafe native id %q accepted", bad)
		}
	}
}

func TestHooksPreserveUserSettingsAndDoNotModifyAgents(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".codebuddy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.local.json")
	original := `{"theme":"dark","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"user-owned","custom":true}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(ws, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	for range 2 {
		if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: ws}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "ao hooks codebuddy session-start") != 1 || !strings.Contains(string(b), "user-owned") || !strings.Contains(string(b), `"custom": true`) {
		t.Fatalf("hook composition lost user data: %s", b)
	}
	if err := p.UninstallHooks(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ao hooks codebuddy") || !strings.Contains(string(b), "user-owned") {
		t.Fatalf("uninstall removed user hooks: %s", b)
	}
	b, err = os.ReadFile(agents)
	if err != nil || string(b) != "unchanged" {
		t.Fatal("AGENTS.md changed")
	}
}

func TestCurrentSurfaceProtectsDecisionsAndDrafts(t *testing.T) {
	p := New()
	rule := strings.Repeat("─", 40)
	frame := func(status, draft string) string {
		return status + "\n" + rule + "\n> " + draft + "\n" + rule + "\n? for shortcuts  ← for agents\n\n\n"
	}
	for _, tc := range []struct {
		name, output string
		state        domain.ActivityState
		ready        bool
	}{
		{"empty", frame("● Finished.", ""), domain.ActivityIdle, true},
		{"completed question", frame("Do you want to continue?\nEnter to confirm was quoted in the answer.\n● Finished.", ""), domain.ActivityIdle, true},
		{"draft", frame("● Finished.", "human draft"), domain.ActivityIdle, false},
		{"wrapped", frame("● Finished.", "\n human continuation"), domain.ActivityIdle, false},
		{"active", frame("✹ Waking… (1s · waiting for model)", ""), domain.ActivityActive, false},
		{"active tip", frame("✸ Waking… (1s · streaming)\n  └ Tip: try a small task", ""), domain.ActivityActive, false},
		{"permission", rule + "\nDo you want to create proof.json?\n> 1. Yes\n2. No", domain.ActivityWaitingInput, false},
		{"trust", "Do you trust the files in this folder?\nEnter to confirm", domain.ActivityWaitingInput, false},
		{"login", "Select login method:\nEnter to login", domain.ActivityWaitingInput, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, ok := p.DetectTerminalActivity(tc.output)
			if !ok || state != tc.state {
				t.Fatalf("activity=%s %v", state, ok)
			}
			err := p.ValidateTerminalMessage(tc.output, "next task")
			if tc.ready != (err == nil) {
				t.Fatalf("ready=%v err=%v", tc.ready, err)
			}
			if err != nil && !errors.Is(err, ports.ErrTerminalMessageNotReady) {
				t.Fatalf("not a refusal: %v", err)
			}
		})
	}
	if p.ComposerIsEmpty("old >\nno current chrome") {
		t.Fatal("missing current composer authorized input")
	}
}

func TestConfiguredCredentialIsNeverReportedAuthorized(t *testing.T) {
	t.Setenv("CODEBUDDY_API_KEY", "configured-only")
	t.Setenv("CODEBUDDY_AUTH_TOKEN", "")
	status, err := New().AuthStatus(context.Background())
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("auth=%s err=%v", status, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().AuthStatus(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}
