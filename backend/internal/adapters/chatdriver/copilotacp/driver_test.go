package copilotacp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePlugin struct {
	installed  []ports.WorkspaceHookConfig
	installErr error
}

func (p *fakePlugin) ResolveBinary(context.Context) (string, error) { return "/bin/copilot", nil }
func (p *fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}
func (p *fakePlugin) InstallAgentProfile(_ context.Context, cfg ports.WorkspaceHookConfig) error {
	p.installed = append(p.installed, cfg)
	return p.installErr
}

func TestDriverIdentity(t *testing.T) {
	driver := New(&fakePlugin{}, nil)
	if driver.Harness() != domain.HarnessCopilot {
		t.Fatalf("Harness = %q", driver.Harness())
	}
	cfg := config(&fakePlugin{})
	if !cfg.RequireAdvertisedOptions {
		t.Error("Copilot acknowledges unadvertised options without applying them; RequireAdvertisedOptions must be set")
	}
	if cfg.SessionMode != nil {
		t.Error("Copilot's session modes are task modes; approval must not be mapped onto them")
	}
	if !cfg.Capabilities[ports.ChatCapabilityUsage] {
		t.Error("usage capability not declared")
	}
}

// Bypass launches without --allow-all: Copilot cannot leave bypass once it
// starts with that flag, so bypass goes through allow_all instead.
func TestConfigureApprovalArgs(t *testing.T) {
	cases := map[ports.PermissionMode][]string{
		ports.PermissionModeDefault:           {"--acp"},
		"":                                    {"--acp"},
		ports.PermissionModeAcceptEdits:       {"--acp", "--allow-tool", "write"},
		ports.PermissionModeAuto:              {"--acp", "--allow-all-tools"},
		ports.PermissionModeBypassPermissions: {"--acp"},
	}
	for mode, want := range cases {
		plugin := &fakePlugin{}
		args, env, err := configure(context.Background(), plugin, acpdriver.LaunchConfig{Permissions: mode})
		if err != nil {
			t.Fatalf("%q: %v", mode, err)
		}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("%q: args = %v, want %v", mode, args, want)
		}
		if env != nil {
			t.Errorf("%q: env = %v, want none", mode, env)
		}
		if len(plugin.installed) != 0 {
			t.Errorf("%q: installed a profile without standing instructions", mode)
		}
	}
}

func TestConfigureNeverPassesModelOrAgentFlags(t *testing.T) {
	args, _, err := configure(context.Background(), &fakePlugin{}, acpdriver.LaunchConfig{
		SessionID: "s1", WorkspacePath: "/w", Model: "gpt-5.5", SystemPrompt: "be AO",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range args {
		if arg == "--model" || arg == "--agent" {
			t.Errorf("args %v contain %s, which Copilot ignores under --acp", args, arg)
		}
	}
}

func TestConfigureInstallsAgentProfile(t *testing.T) {
	plugin := &fakePlugin{}
	if _, _, err := configure(context.Background(), plugin, acpdriver.LaunchConfig{
		SessionID: "s1", WorkspacePath: "/w", SystemPrompt: "be AO",
	}); err != nil {
		t.Fatal(err)
	}
	want := []ports.WorkspaceHookConfig{{SessionID: "s1", WorkspacePath: "/w", SystemPrompt: "be AO"}}
	if !reflect.DeepEqual(plugin.installed, want) {
		t.Errorf("installed = %+v, want %+v", plugin.installed, want)
	}

	failing := &fakePlugin{installErr: errors.New("disk full")}
	if _, _, err := configure(context.Background(), failing, acpdriver.LaunchConfig{
		SessionID: "s1", WorkspacePath: "/w", SystemPrompt: "be AO",
	}); err == nil {
		t.Error("a failed profile install must fail the launch, not start without instructions")
	}
}

func TestLaunchSessionOptionsSelectsProfile(t *testing.T) {
	got := launchSessionOptions(acpdriver.LaunchConfig{SessionID: "proj_1.x", SystemPrompt: "be AO"})
	want := []acpdriver.SessionOption{{ID: "agent", Value: "ao-proj-1-x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("options = %v, want %v", got, want)
	}
	if got := launchSessionOptions(acpdriver.LaunchConfig{SessionID: "s1"}); got != nil {
		t.Errorf("no standing instructions: options = %v, want none", got)
	}
}

func TestSessionOptions(t *testing.T) {
	cases := []struct {
		settings ports.ChatTurnSettings
		want     []acpdriver.SessionOption
	}{
		{ports.ChatTurnSettings{}, nil},
		{ports.ChatTurnSettings{Effort: "high"}, nil},
		{ports.ChatTurnSettings{Model: " gpt-5.5 "}, []acpdriver.SessionOption{{ID: "model", Value: "gpt-5.5"}}},
		{ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions},
			[]acpdriver.SessionOption{{ID: "allow_all", Value: "on"}}},
		{ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits},
			[]acpdriver.SessionOption{{ID: "allow_all", Value: "off"}}},
		{ports.ChatTurnSettings{Approval: ports.PermissionModeDefault},
			[]acpdriver.SessionOption{{ID: "allow_all", Value: "off"}}},
	}
	for _, tc := range cases {
		if got := sessionOptions(tc.settings); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v: options = %v, want %v", tc.settings, got, tc.want)
		}
	}
}

func TestValidateTurnSettings(t *testing.T) {
	const (
		def    = ports.PermissionModeDefault
		edits  = ports.PermissionModeAcceptEdits
		auto   = ports.PermissionModeAuto
		bypass = ports.PermissionModeBypassPermissions
	)
	allowed := map[ports.PermissionMode][]ports.PermissionMode{
		def:    {def, bypass},
		edits:  {edits, bypass},
		auto:   {auto, bypass},
		bypass: {def, bypass},
	}
	for initial, ok := range allowed {
		for _, requested := range []ports.PermissionMode{def, edits, auto, bypass} {
			err := validateTurnSettings(initial, ports.ChatTurnSettings{Approval: requested})
			want := false
			for _, mode := range ok {
				want = want || mode == requested
			}
			if want && err != nil {
				t.Errorf("%s -> %s: unexpected error %v", initial, requested, err)
			}
			if !want && !errors.Is(err, acpdriver.ErrACPSetterUnsupported) {
				t.Errorf("%s -> %s: err = %v, want ErrACPSetterUnsupported", initial, requested, err)
			}
		}
	}
	if err := validateTurnSettings(def, ports.ChatTurnSettings{Model: "m", Effort: "high"}); err != nil {
		t.Errorf("model/effort without approval: %v", err)
	}
}

func TestValidateInitialize(t *testing.T) {
	info := func(name, version string) acpsdk.InitializeResponse {
		return acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: name, Version: version}}
	}
	for _, ok := range []acpsdk.InitializeResponse{
		info("Copilot", "1.0.93"), info("Copilot", "1.0.100"), info("Copilot", "1.1.0"),
		info("Copilot", "2.0.0-beta.1"),
	} {
		if err := validateInitialize(ok); err != nil {
			t.Errorf("%+v: %v", ok.AgentInfo, err)
		}
	}
	for _, bad := range []acpsdk.InitializeResponse{
		{}, info("Copilot", "1.0.92"), info("Copilot", "0.9.200"), info("Copilot", "dev"),
		info("Claude Code", "1.0.93"),
	} {
		if err := validateInitialize(bad); err == nil {
			t.Errorf("%+v: admitted", bad.AgentInfo)
		}
	}
}
