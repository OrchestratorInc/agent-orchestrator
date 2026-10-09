package letta

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

func TestLaunchSeparatesPromptAndUsesSafePermissions(t *testing.T) {
	for _, tt := range []struct {
		mode   ports.PermissionMode
		native string
	}{
		{"", "standard"}, {ports.PermissionModeDefault, "standard"}, {ports.PermissionModeAcceptEdits, "acceptEdits"}, {ports.PermissionModeBypassPermissions, "unrestricted"},
	} {
		t.Run(string(tt.mode), func(t *testing.T) {
			p := &Plugin{resolvedBinary: "/bin/letta"}
			cfg := ports.LaunchConfig{Permissions: tt.mode, Prompt: "--help; never parse this as an option", SystemPrompt: "private standing token", Config: ports.AgentConfig{Model: "anthropic/test"}}
			got, err := p.GetLaunchCommand(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"/bin/letta", "--permission-mode", tt.native, "--model", "anthropic/test", "--new-agent", "--new"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("argv = %#v, want %#v", got, want)
			}
			delivery, err := p.GetPromptDeliveryStrategy(context.Background(), cfg)
			if err != nil || delivery != ports.PromptDeliveryAfterStart {
				t.Fatalf("delivery = %q, %v", delivery, err)
			}
		})
	}
}

func TestUnsupportedOverridesFailBeforeBinaryResolution(t *testing.T) {
	for _, cfg := range []ports.LaunchConfig{
		{Permissions: ports.PermissionModeAuto}, {Permissions: "invalid"}, {AllowedTools: []string{"Read"}}, {DisallowedTools: []string{"Bash"}}, {Config: ports.AgentConfig{Effort: "high"}},
	} {
		if _, err := New().GetLaunchCommand(context.Background(), cfg); err == nil {
			t.Fatalf("accepted unsupported config %#v", cfg)
		}
	}
}

func TestRestoreUsesOnlyExactConversation(t *testing.T) {
	p := &Plugin{resolvedBinary: "/bin/letta"}
	for _, id := range []string{"", "default", "latest", "agent-123", "--new", "conv-foo bar", " conv-123", "conv-123\n"} {
		_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}})
		if ok || err == nil {
			t.Fatalf("restore accepted %q", id)
		}
	}
	got, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "conv-1234-abcd"}}, SystemPrompt: "hidden"})
	if err != nil || !ok {
		t.Fatalf("restore = %v, %v", ok, err)
	}
	want := []string{"/bin/letta", "--permission-mode", "standard", "--conversation", "conv-1234-abcd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restore argv = %#v", got)
	}
}

func TestNativeIdentityAndActivity(t *testing.T) {
	payload := []byte(`{"session_id":"transient-1","agent_id":"agent-2","conversation_id":"conv-3","tool_call_id":"call-4"}`)
	if got := NativeConversationID(payload); got != "conv-3" {
		t.Fatalf("native ID = %q", got)
	}
	if got := NativeToolCallID(payload); got != "call-4" {
		t.Fatalf("tool ID = %q", got)
	}
	for _, raw := range []string{`{}`, `{"conversation_id":"default"}`, `{"session_id":"conv-wrong"}`, `not json`} {
		if got := NativeConversationID([]byte(raw)); got != "" {
			t.Fatalf("accepted %s as %q", raw, got)
		}
	}
	for event, want := range map[string]domain.ActivityState{"pre-tool-use": domain.ActivityActive, "post-tool-use": domain.ActivityActive, "post-tool-use-failure": domain.ActivityActive} {
		got, ok := DeriveActivityState(event, payload)
		if !ok || got != want {
			t.Fatalf("%s = %q, %v", event, got, ok)
		}
	}
	for _, event := range []string{"session-start", "user-prompt-submit", "permission-request", "stop"} {
		if _, ok := DeriveActivityState(event, payload); ok {
			t.Fatalf("%s manufactured settled activity", event)
		}
	}
	if _, ok := any(New()).(ports.SemanticMessageAcceptanceSignaler); ok {
		t.Fatal("pre-submit hook cannot establish semantic acceptance")
	}
}

func TestPrivateHooksPreserveUserSettingsAndAreIgnored(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".letta", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	seed := `{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-owned","custom":true}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	for i := 0; i < 2; i++ {
		if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Theme string `json:"theme"`
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Quiet   bool   `json:"quiet"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Theme != "dark" {
		t.Fatal("overwrote user settings")
	}
	count := 0
	for _, groups := range settings.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if strings.HasPrefix(hook.Command, hookPrefix) {
					count++
					if !hook.Quiet || hook.Timeout != 10000 {
						t.Fatalf("unsafe hook %#v", hook)
					}
				}
			}
		}
	}
	if count != 7 {
		t.Fatalf("managed hook count = %d", count)
	}
	ignored, err := os.ReadFile(filepath.Join(workspace, ".letta", ".gitignore"))
	if err != nil || !strings.Contains(string(ignored), "/settings.local.json") {
		t.Fatalf("hook file is not ignored: %s, %v", ignored, err)
	}
	if err := p.UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "echo user-owned") || !strings.Contains(string(data), "custom") || strings.Contains(string(data), hookPrefix) {
		t.Fatalf("uninstall damaged ownership: %s", data)
	}
}

func TestAuthPresenceIsNotAuthorization(t *testing.T) {
	for _, key := range []string{"LETTA_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"} {
		t.Setenv(key, "")
	}
	p := &Plugin{resolvedBinary: "/bin/letta"}
	got, err := p.AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusUnknown {
		t.Fatalf("without probe = %q,%v", got, err)
	}
	t.Setenv("LETTA_API_KEY", "test-only-not-a-real-key")
	got, err = p.AuthStatus(context.Background())
	if err != nil || got != ports.AgentAuthStatusConfigured {
		t.Fatalf("credential presence = %q,%v", got, err)
	}
}

func TestReadinessRequiresComposer(t *testing.T) {
	hints, err := New().PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil || !hints.RequireReady || hints.Timeout <= 0 {
		t.Fatalf("readiness = %#v, %v", hints, err)
	}
	for _, pattern := range hints.Patterns {
		if pattern == "Letta Code" || pattern == ">" {
			t.Fatalf("generic banner/prompt marker %q", pattern)
		}
	}
}

func TestNativeCancellationUsesEscapeWithoutSubmit(t *testing.T) {
	if got := New().InterruptInput(); got != "\x1b" {
		t.Fatalf("cancel input = %q", got)
	}
}
