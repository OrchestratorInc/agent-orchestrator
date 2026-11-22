package grokacp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePlugin struct {
	status ports.AgentAuthStatus
}

func (fakePlugin) ResolveBinary(context.Context) (string, error) { return "/user/bin/grok", nil }
func (p fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return p.status, nil
}

func TestDriverCapabilitiesAndAuth(t *testing.T) {
	driver := New(fakePlugin{status: ports.AgentAuthStatusAuthorized}, nil)
	if driver.Harness() != domain.HarnessGrok {
		t.Fatalf("harness = %q, want grok", driver.Harness())
	}
	caps, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []ports.ChatCapability{
		ports.ChatCapabilityStreaming, ports.ChatCapabilityTools, ports.ChatCapabilityApprovals,
		ports.ChatCapabilityInterrupt, ports.ChatCapabilityResume, ports.ChatCapabilityHistory,
	} {
		if !caps.Has(capability) {
			t.Errorf("missing capability %q", capability)
		}
	}
	if caps.Has(ports.ChatCapabilityPlans) {
		t.Fatal("Grok has not advertised plans")
	}
	_, err = New(fakePlugin{status: ports.AgentAuthStatusUnauthorized}, nil).Probe(context.Background())
	if !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("unauthorized probe = %v, want ErrChatAuthRequired", err)
	}
}

func TestConfigureLaunchArguments(t *testing.T) {
	for _, tc := range []struct {
		mode ports.PermissionMode
		want []string
	}{
		{"", []string{"agent", "stdio"}},
		{ports.PermissionModeDefault, []string{"agent", "stdio"}},
		{ports.PermissionModeAuto, []string{"agent", "stdio"}},
		{ports.PermissionModeBypassPermissions, []string{"agent", "--always-approve", "stdio"}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			args, env, err := configure(context.Background(), acpdriver.LaunchConfig{
				Permissions: tc.mode, Model: "grok-4.7", SystemPrompt: "AO instructions",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(args, tc.want) || env != nil {
				t.Fatalf("launch = %v, %v; want %v with inherited environment", args, env, tc.want)
			}
		})
	}
	_, _, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: ports.PermissionModeAcceptEdits})
	if !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("accept-edits launch = %v, want unsupported", err)
	}
}

func TestToolDisabledStartsFailBeforeLaunchingGrok(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		args, env, err := configure(t.Context(), acpdriver.LaunchConfig{ReadOnly: readOnly, DisableTools: true})
		if !errors.Is(err, ports.ErrChatCapabilityUnavailable) || args != nil || env != nil {
			t.Fatalf("tool-disabled configuration = %v, %v, %v; want rejected launch", args, env, err)
		}
		driver := New(fakePlugin{status: ports.AgentAuthStatusAuthorized}, nil)
		conversation, err := driver.Start(t.Context(), ports.ChatStartConfig{
			SessionID: "background-grok-test", DataDir: t.TempDir(), Ephemeral: true,
			WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeDefault,
			ReadOnly: readOnly, DisableTools: true,
		})
		if conversation != nil {
			_ = conversation.Close()
			t.Fatal("tool-disabled launch started a native conversation")
		}
		if !errors.Is(err, ports.ErrChatCapabilityUnavailable) {
			t.Fatalf("tool-disabled launch (read-only=%v) = %v, want capability unavailable", readOnly, err)
		}
	}
}

func TestPermissionModesCannotChangeOnLiveProcess(t *testing.T) {
	for _, initial := range []ports.PermissionMode{
		ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions,
	} {
		for _, requested := range []ports.PermissionMode{
			"", ports.PermissionModeDefault, ports.PermissionModeAuto,
			ports.PermissionModeBypassPermissions, ports.PermissionModeAcceptEdits,
		} {
			err := validateTurnSettings(initial, ports.ChatTurnSettings{Approval: requested})
			wantOK := requested == "" || requested == initial
			if wantOK && err != nil {
				t.Errorf("%q -> %q: %v", initial, requested, err)
			}
			if !wantOK && !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
				t.Errorf("%q -> %q: %v, want unsupported", initial, requested, err)
			}
		}
	}
	checker := New(fakePlugin{}, nil).(ports.ChatPermissionModeChecker)
	if checker.SupportsPermissionMode(ports.PermissionModeAcceptEdits) ||
		!checker.SupportsPermissionMode(ports.PermissionModeAuto) ||
		!checker.SupportsPermissionMode(ports.PermissionModeDefault) ||
		!checker.SupportsPermissionMode(ports.PermissionModeBypassPermissions) {
		t.Fatal("incorrect permission-mode admission")
	}
}

func TestSessionOptions(t *testing.T) {
	if got := sessionOptions(ports.ChatTurnSettings{}); got != nil {
		t.Fatalf("empty settings = %v", got)
	}
	want := []acpdriver.SessionOption{
		{ID: "model", Value: "grok-4.7"},
		{ID: "reasoning_effort", Value: "xhigh"},
	}
	got := sessionOptions(ports.ChatTurnSettings{Model: " grok-4.7 ", Effort: " xhigh ", Approval: ports.PermissionModeAuto})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("options = %v, want %v", got, want)
	}
}

func TestSessionMetaPreservesNativeReviewAndSystemRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   ports.PermissionMode
		prompt string
		want   map[string]any
	}{
		{name: "default without rules"},
		{name: "whitespace rules", prompt: " \n "},
		{name: "auto", mode: ports.PermissionModeAuto, want: map[string]any{"autoMode": true}},
		{name: "default rules", mode: ports.PermissionModeDefault, prompt: " AO rules \n", want: map[string]any{"rules": "AO rules"}},
		{name: "auto rules", mode: ports.PermissionModeAuto, prompt: "AO rules", want: map[string]any{"autoMode": true, "rules": "AO rules"}},
		{name: "bypass", mode: ports.PermissionModeBypassPermissions},
		{name: "bypass rules", mode: ports.PermissionModeBypassPermissions, prompt: "AO rules", want: map[string]any{"rules": "AO rules"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionMeta(acpdriver.LaunchConfig{Permissions: tc.mode, SystemPrompt: tc.prompt})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("metadata = %#v, want %#v", got, tc.want)
			}
		})
	}
}
