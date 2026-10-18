package codebuddyacp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigurePreservesNativeDefaultsAndPrivateAppend(t *testing.T) {
	prompt := "Private standing instructions\nwith independent project rules intact."
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{SystemPrompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--acp", "--permission-mode", "default", "--append-system-prompt", prompt}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
	for _, key := range []string{"CODEBUDDY_CODE_DISABLE_BACKGROUND_TASKS", "CODEBUDDY_DISABLE_CRON", "DISABLE_AUTOUPDATER"} {
		if env[key] != "1" {
			t.Errorf("missing native control %s", key)
		}
	}
	args, _, err = configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil || len(args) != 3 {
		t.Fatalf("native default prompt altered: %q, %v", args, err)
	}
}

func TestConfigureRejectsInvalidPrivateInstructions(t *testing.T) {
	for _, prompt := range []string{strings.Repeat("x", (64<<10)+1), "bad\x00text", string([]byte{0xff})} {
		if _, _, err := configure(context.Background(), acpdriver.LaunchConfig{SystemPrompt: prompt}); err == nil {
			t.Fatal("invalid private instructions admitted")
		}
	}
	if _, _, err := configure(context.Background(), acpdriver.LaunchConfig{SystemPrompt: strings.Repeat("x", 64<<10)}); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateRejectsUnqualifiedSettings(t *testing.T) {
	for _, settings := range []ports.ChatTurnSettings{
		{Model: "unqualified"}, {Effort: "high"}, {Approval: ports.PermissionModeAuto}, {Approval: ports.PermissionModeAcceptEdits}, {Approval: ports.PermissionModeBypassPermissions}, {Approval: "unknown"},
	} {
		if err := validateTurnSettings(ports.PermissionModeDefault, settings); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
			t.Fatalf("settings %+v: %v", settings, err)
		}
	}
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault} {
		if err := validateTurnSettings(mode, ports.ChatTurnSettings{Approval: mode}); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateTurnSettings(ports.PermissionModeAuto, ports.ChatTurnSettings{}); err == nil {
		t.Fatal("unsafe launch posture admitted")
	}
	for _, cfg := range []acpdriver.LaunchConfig{{Model: "unqualified"}, {Permissions: ports.PermissionModeAuto}} {
		if _, _, err := configure(context.Background(), cfg); err == nil {
			t.Fatal("invalid launch settings admitted")
		}
	}
}

func TestExactCandidateVersion(t *testing.T) {
	for _, output := range []string{"2.164.0", "2.164.0\n"} {
		if err := validateVersionOutput(output); err != nil {
			t.Fatal(err)
		}
	}
	for _, output := range []string{"", "2.163.0", "2.165.0", "2.164.0-patched", "error: wanted 2.164.0"} {
		if err := validateVersionOutput(output); err == nil {
			t.Fatalf("unqualified version admitted: %q", output)
		}
	}
}
