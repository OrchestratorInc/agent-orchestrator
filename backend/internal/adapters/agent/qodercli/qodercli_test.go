package qodercli

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

func newTestPlugin() *Plugin { return &Plugin{resolvedBinary: "/opt/qodercli"} }

func TestLaunchCommand(t *testing.T) {
	p := newTestPlugin()
	dataDir := t.TempDir()
	workspace := t.TempDir()

	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		DataDir:       dataDir,
		SessionID:     "ao-session-1",
		WorkspacePath: workspace,
		Prompt:        "fix the failing test",
		Config:        ports.AgentConfig{Model: "ultimate"},
		Permissions:   ports.PermissionModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(dataDir, "agent-runtime", "qodercli", "ao-session-1", "settings.json")
	want := []string{
		"/opt/qodercli",
		"--session-id", SessionUUID("ao-session-1"),
		"--permission-mode", "auto",
		"--model", "ultimate",
		"--settings", settingsPath,
		"-i", "fix the failing test",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("command = %#v\nwant %#v", cmd, want)
	}
}

func TestLaunchCommandDefaultEmitsNoPermissionFlag(t *testing.T) {
	cmd, err := newTestPlugin().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID: "s1",
		DataDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(cmd, " "), "--permission-mode") {
		t.Fatalf("default mode must not pin a permission flag: %#v", cmd)
	}
}

func TestLaunchUsesReservedNativeSessionID(t *testing.T) {
	reserved := "8f14e45f-6b3c-4a1e-9d1f-2a7c9b4d5e60"
	cmd, err := newTestPlugin().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:       "s1",
		NativeSessionID: reserved,
		DataDir:         t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(cmd, "--session-id", reserved) {
		t.Fatalf("launch did not pin the reserved native id: %#v", cmd)
	}
}

func TestLaunchRejectsMalformedReservedNativeSessionID(t *testing.T) {
	_, err := newTestPlugin().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:       "s1",
		NativeSessionID: "not-a-uuid",
	})
	if err == nil {
		t.Fatal("malformed native id must be rejected rather than passed to the CLI")
	}
}

func TestToolFlagsAreOnePerRule(t *testing.T) {
	cmd, err := newTestPlugin().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:       "s1",
		DataDir:         t.TempDir(),
		AllowedTools:    []string{"Read", "Grep"},
		DisallowedTools: []string{"Write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--allowed-tools", "Read", "--allowed-tools", "Grep", "--disallowed-tools", "Write"}
	if !containsSeq(cmd, want) {
		t.Fatalf("tool flags missing or malformed; want sequence %#v in %#v", want, cmd)
	}
}

func TestSystemPromptFilePreferredOverInline(t *testing.T) {
	promptFile := filepath.Join(t.TempDir(), "role.md")
	if err := os.WriteFile(promptFile, []byte("role"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, err := newTestPlugin().GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SessionID:        "s1",
		DataDir:          t.TempDir(),
		SystemPrompt:     "inline role",
		SystemPromptFile: promptFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(cmd, "--append-system-prompt-file", promptFile) {
		t.Fatalf("prompt file flag missing: %#v", cmd)
	}
	if strings.Contains(strings.Join(cmd, " "), "--append-system-prompt "+"inline role") {
		t.Fatalf("inline prompt must not also be sent: %#v", cmd)
	}
}

func TestRestoreCommandResumesExactNativeID(t *testing.T) {
	p := newTestPlugin()
	nativeID := SessionUUID("ao-session-9")
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: t.TempDir(),
		Session: ports.SessionRef{
			ID:            "ao-session-9",
			WorkspacePath: t.TempDir(),
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: nativeID},
		},
		Permissions: ports.PermissionModeAcceptEdits,
		Prompt:      "continue the task",
	})
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if !containsPair(cmd, "--resume", nativeID) {
		t.Fatalf("restore did not resume the captured id: %#v", cmd)
	}
	if strings.Contains(strings.Join(cmd, " "), "continue the task") {
		t.Fatalf("the new turn must not ride on the resume argv: %#v", cmd)
	}
}

func TestRestoreFallsBackToDerivedUUID(t *testing.T) {
	cmd, ok, err := newTestPlugin().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		DataDir: t.TempDir(),
		Session: ports.SessionRef{ID: "ao-session-2", WorkspacePath: t.TempDir()},
	})
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if !containsPair(cmd, "--resume", SessionUUID("ao-session-2")) {
		t.Fatalf("restore did not fall back to the derived id: %#v", cmd)
	}
}

func TestRestoreRefusesNonUUIDResumeTarget(t *testing.T) {
	_, ok, err := newTestPlugin().GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			ID:       "ao-session-3",
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a session-list index must never be resumed as a conversation")
	}
}

func TestSettingsPayloadCarriesTrustAndHooks(t *testing.T) {
	workspace := t.TempDir()
	raw, err := SettingsJSON(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Permissions struct {
			TrustDirectories []string `json:"trustDirectories"`
		} `json:"permissions"`
		General struct {
			EnableAutoUpdate bool `json:"enableAutoUpdate"`
		} `json:"general"`
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("settings is not valid JSON: %v", err)
	}
	if payload.General.EnableAutoUpdate {
		t.Fatal("auto-update must be disabled for AO sessions")
	}
	if !reflect.DeepEqual(payload.Permissions.TrustDirectories, []string{workspace}) {
		t.Fatalf("trust dirs = %#v, want the workspace", payload.Permissions.TrustDirectories)
	}
	if _, ok := payload.Hooks["SubagentStop"]; ok {
		t.Fatal("SubagentStop must not be installed")
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd", "Notification"} {
		groups, ok := payload.Hooks[event]
		if !ok || len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("hook %s missing or malformed: %#v", event, groups)
		}
		entry := groups[0].Hooks[0]
		if entry.Timeout != hookTimeoutSeconds {
			t.Fatalf("hook %s timeout = %d, want %d", event, entry.Timeout, hookTimeoutSeconds)
		}
		if !strings.HasPrefix(entry.Command, hookCommandPrefix) {
			t.Fatalf("hook %s command = %q", event, entry.Command)
		}
	}
	if groups := payload.Hooks["SessionStart"]; groups[0].Matcher == nil || *groups[0].Matcher != sessionStartMatcher {
		t.Fatalf("SessionStart matcher = %v", groups[0].Matcher)
	}
}

func TestSettingsArgWritesUnderDataDir(t *testing.T) {
	dataDir := t.TempDir()
	got, err := SettingsArg(dataDir, "ao/session/1", "/work/tree")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, "agent-runtime", "qodercli", "ao_session_1", "settings.json")
	if got != want {
		t.Fatalf("settings arg = %q, want %q", got, want)
	}
}

func TestSettingsArgFallsBackToInlineJSONWithoutDataDir(t *testing.T) {
	got, err := SettingsArg("", "s1", "/work/tree")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("expected inline JSON, got %q", got)
	}
}

func TestSessionUUIDIsStableAndDistinct(t *testing.T) {
	first := SessionUUID("abc")
	again := SessionUUID("abc")
	if first != again {
		t.Fatal("session uuid must be deterministic")
	}
	if SessionUUID("abc") == SessionUUID("abd") {
		t.Fatal("distinct sessions must not share a native id")
	}
	if !isUUID(first) {
		t.Fatal("derived id must be a UUID")
	}
}

func TestActivityMarksElicitationFormAsWaitingForInput(t *testing.T) {
	payload := []byte(`{"notification_type":"elicitation_dialog"}`)
	state, ok := DeriveActivityState("notification", payload)
	if !ok || state != domain.ActivityWaitingInput {
		t.Fatalf("state = %q, ok = %v", state, ok)
	}
}

func TestRestoreIsNotVersionRefreshCapable(t *testing.T) {
	if newTestPlugin().SupportsTerminalVersionRefresh() {
		t.Fatal("qodercli must not claim unattended terminal version refresh")
	}
}

func containsPair(cmd []string, flag, value string) bool {
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] == flag && cmd[i+1] == value {
			return true
		}
	}
	return false
}

// containsSeq reports whether want appears as a contiguous run inside cmd.
func containsSeq(cmd, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i+len(want) <= len(cmd); i++ {
		if reflect.DeepEqual(cmd[i:i+len(want)], want) {
			return true
		}
	}
	return false
}
