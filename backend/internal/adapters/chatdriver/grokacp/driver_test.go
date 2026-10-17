package grokacp

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type stubPlugin struct{}

func (stubPlugin) ResolveBinary(context.Context) (string, error) {
	return "/Users/test/.grok/bin/grok", nil
}
func (stubPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}

// Flag order is load-bearing: --permission-mode is a global flag (before
// `agent`) and --always-approve is an agent flag (after `agent`).
func TestConfigureMapsPermissionModeOntoLaunchArgv(t *testing.T) {
	for _, tc := range []struct {
		mode ports.PermissionMode
		want []string
	}{
		{"", []string{"--no-auto-update", "--permission-mode", "default", "agent", "stdio"}},
		{ports.PermissionModeDefault, []string{"--no-auto-update", "--permission-mode", "default", "agent", "stdio"}},
		{ports.PermissionModeAcceptEdits, []string{"--no-auto-update", "--permission-mode", "default", "agent", "stdio"}},
		{ports.PermissionModeAuto, []string{"--no-auto-update", "--permission-mode", "auto", "agent", "stdio"}},
		{ports.PermissionModeBypassPermissions, []string{"--no-auto-update", "agent", "--always-approve", "stdio"}},
	} {
		args, env, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: tc.mode})
		if err != nil {
			t.Fatalf("configure(%q): %v", tc.mode, err)
		}
		if !reflect.DeepEqual(args, tc.want) {
			t.Errorf("configure(%q) args = %#v, want %#v", tc.mode, args, tc.want)
		}
		if env != nil {
			t.Errorf("configure(%q) env = %#v, want nil", tc.mode, env)
		}
	}
}

// `grok agent stdio` ignores the global --rules flag, so standing
// instructions must never ride the argv.
func TestConfigureNeverPassesStandingInstructionsAsFlags(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{SystemPrompt: "AO rules"})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	for _, arg := range args {
		if arg == "--rules" || arg == "AO rules" || arg == "--system-prompt-override" {
			t.Fatalf("args = %#v, want no system-prompt flag", args)
		}
	}
}

func TestSessionMetaDeliversStandingInstructionsAsRules(t *testing.T) {
	got := sessionMeta(acpdriver.LaunchConfig{SystemPrompt: "  You are an AO worker.  "})
	want := map[string]any{"rules": "You are an AO worker."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sessionMeta = %#v, want %#v", got, want)
	}
	for _, empty := range []string{"", " \n\t "} {
		if got := sessionMeta(acpdriver.LaunchConfig{SystemPrompt: empty}); got != nil {
			t.Errorf("sessionMeta(%q) = %#v, want nil", empty, got)
		}
	}
}

func TestSessionOptionsMapModelAndEffort(t *testing.T) {
	got := sessionOptions(ports.ChatTurnSettings{Model: " grok-4.7 ", Effort: "high"})
	want := []acpdriver.SessionOption{{ID: "model", Value: "grok-4.7"}, {ID: "reasoning_effort", Value: "high"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sessionOptions = %#v, want %#v", got, want)
	}
	if got := sessionOptions(ports.ChatTurnSettings{}); got != nil {
		t.Fatalf("empty settings = %#v, want nil", got)
	}
}

func TestValidateTurnSettingsRefusesChangesThatNeedANewProcess(t *testing.T) {
	ok := []struct{ initial, requested ports.PermissionMode }{
		{ports.PermissionModeDefault, ""},
		{ports.PermissionModeDefault, ports.PermissionModeDefault},
		{ports.PermissionModeDefault, ports.PermissionModeAcceptEdits},
		{ports.PermissionModeAcceptEdits, ports.PermissionModeDefault},
		{ports.PermissionModeAuto, ports.PermissionModeAuto},
		{ports.PermissionModeBypassPermissions, ports.PermissionModeBypassPermissions},
	}
	for _, tc := range ok {
		if err := validateTurnSettings(tc.initial, ports.ChatTurnSettings{Approval: tc.requested}); err != nil {
			t.Errorf("%q -> %q: %v", tc.initial, tc.requested, err)
		}
	}
	refused := []struct{ initial, requested ports.PermissionMode }{
		{ports.PermissionModeDefault, ports.PermissionModeAuto},
		{ports.PermissionModeAcceptEdits, ports.PermissionModeBypassPermissions},
		{ports.PermissionModeBypassPermissions, ports.PermissionModeDefault},
		{ports.PermissionModeAuto, ports.PermissionModeBypassPermissions},
	}
	for _, tc := range refused {
		err := validateTurnSettings(tc.initial, ports.ChatTurnSettings{Approval: tc.requested})
		if !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
			t.Errorf("%q -> %q = %v, want ErrChatPermissionModeUnsupported", tc.initial, tc.requested, err)
		}
	}
}

func TestPermissionPolicyApprovesEditsOnlyForAcceptEdits(t *testing.T) {
	edit := acpsdk.ToolKindEdit
	execute := acpsdk.ToolKindExecute
	params := func(kind *acpsdk.ToolKind) acpsdk.RequestPermissionRequest {
		return acpsdk.RequestPermissionRequest{
			ToolCall: acpsdk.ToolCallUpdate{Kind: kind},
			Options: []acpsdk.PermissionOption{
				{OptionId: "reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
				{OptionId: "allow-edits-session", Kind: acpsdk.PermissionOptionKindAllowAlways},
				{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
			},
		}
	}
	if id, ok := permissionPolicy(ports.PermissionModeAcceptEdits, params(&edit)); !ok || id != "allow" {
		t.Fatalf("accept-edits on edit = (%q, %v), want (allow, true)", id, ok)
	}
	if _, ok := permissionPolicy(ports.PermissionModeAcceptEdits, params(&execute)); ok {
		t.Fatal("accept-edits answered a non-edit tool")
	}
	for _, mode := range []ports.PermissionMode{
		ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions,
	} {
		if _, ok := permissionPolicy(mode, params(&edit)); ok {
			t.Errorf("%q answered a permission request Grok enforces itself", mode)
		}
	}
}

func TestNewDriverTargetsGrokHarness(t *testing.T) {
	driver := New(stubPlugin{}, slog.New(slog.DiscardHandler))
	if driver.Harness() != domain.HarnessGrok {
		t.Fatalf("harness = %q, want %q", driver.Harness(), domain.HarnessGrok)
	}
	capabilities, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	for _, capability := range []ports.ChatCapability{ports.ChatCapabilityModels, ports.ChatCapabilityConfigOptions} {
		if !capabilities.Has(capability) {
			t.Errorf("Probe does not advertise %q", capability)
		}
	}
}
