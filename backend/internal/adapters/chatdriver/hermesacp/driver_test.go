package hermesacp

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

func TestConfigureLaunchesNativeACPWithEnvSystemPrompt(t *testing.T) {
	tests := []struct {
		name string
		cfg  acpdriver.LaunchConfig
		want []string
		env  map[string]string
	}{
		{name: "defaults", want: []string{"acp"}},
		{
			name: "system prompt rides in the env overlay",
			cfg:  acpdriver.LaunchConfig{SystemPrompt: "  Follow AO rules.  "},
			want: []string{"acp"},
			env:  map[string]string{"HERMES_EPHEMERAL_SYSTEM_PROMPT": "Follow AO rules."},
		},
		{
			name: "model is session-owned, never a launch flag",
			cfg:  acpdriver.LaunchConfig{Model: "anthropic/claude-sonnet"},
			want: []string{"acp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, env, err := configure(context.Background(), tt.cfg)
			if err != nil {
				t.Fatalf("configure: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("args = %#v, want %#v", got, tt.want)
			}
			if !reflect.DeepEqual(env, tt.env) {
				t.Fatalf("env = %#v, want %#v", env, tt.env)
			}
		})
	}
}

func TestSessionModeMapsApprovalVocabulary(t *testing.T) {
	tests := []struct {
		mode ports.PermissionMode
		want string
	}{
		{ports.PermissionModeDefault, ""},
		{ports.PermissionMode("bogus"), ""},
		{ports.PermissionModeAcceptEdits, "accept_edits"},
		{ports.PermissionModeAuto, "dont_ask"},
		{ports.PermissionModeBypassPermissions, "dont_ask"},
	}
	for _, tt := range tests {
		if got := sessionMode(tt.mode); got != tt.want {
			t.Fatalf("sessionMode(%q) = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func allowOption(id string, kind acpsdk.PermissionOptionKind) acpsdk.PermissionOption {
	optionID := acpsdk.PermissionOptionId(id)
	return acpsdk.PermissionOption{OptionId: optionID, Name: id, Kind: kind}
}

func TestPermissionPolicyAutoApprovesOnlyInBypassMode(t *testing.T) {
	request := acpsdk.RequestPermissionRequest{
		Options: []acpsdk.PermissionOption{
			allowOption("deny_once", acpsdk.PermissionOptionKindRejectOnce),
			allowOption("allow_once", acpsdk.PermissionOptionKindAllowOnce),
			allowOption("allow_always", acpsdk.PermissionOptionKindAllowAlways),
		},
	}

	if id, handled := permissionPolicy(ports.PermissionModeAcceptEdits, request); handled {
		t.Fatalf("accept-edits policy handled the request with %q, want passthrough", id)
	}
	if id, handled := permissionPolicy(ports.PermissionModeDefault, request); handled {
		t.Fatalf("default policy handled the request with %q, want passthrough", id)
	}

	id, handled := permissionPolicy(ports.PermissionModeBypassPermissions, request)
	if !handled || id != "allow_always" {
		t.Fatalf("bypass policy = (%q, %v), want allow_always", id, handled)
	}

	request.Options = []acpsdk.PermissionOption{allowOption("allow_once", acpsdk.PermissionOptionKindAllowOnce)}
	id, handled = permissionPolicy(ports.PermissionModeBypassPermissions, request)
	if !handled || id != "allow_once" {
		t.Fatalf("bypass fallback = (%q, %v), want allow_once", id, handled)
	}

	request.Options = []acpsdk.PermissionOption{allowOption("deny_once", acpsdk.PermissionOptionKindRejectOnce)}
	if _, handled = permissionPolicy(ports.PermissionModeBypassPermissions, request); handled {
		t.Fatal("bypass policy handled a request with no allow option, want passthrough")
	}
}

func TestVersionProbeRequiresVersionOutput(t *testing.T) {
	if err := versionProbe(context.Background(), "/nonexistent/hermes"); err == nil {
		t.Fatal("versionProbe with a missing binary should fail")
	}
}

func TestDriverReusesHermesPluginForProbe(t *testing.T) {
	plugin := &fakePlugin{status: ports.AgentAuthStatusAuthorized, binary: "/usr/bin/hermes"}
	versionCalls := 0
	driver := newDriver(plugin, func(_ context.Context, bin string) error {
		versionCalls++
		if bin != "/usr/bin/hermes" {
			t.Fatalf("version probe binary = %q, want /usr/bin/hermes", bin)
		}
		return nil
	}, nil)

	caps, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if driver.Harness() != domain.HarnessHermes {
		t.Fatalf("harness = %q", driver.Harness())
	}
	for _, capability := range []ports.ChatCapability{
		ports.ChatCapabilityStreaming, ports.ChatCapabilityTools, ports.ChatCapabilityApprovals,
		ports.ChatCapabilityInterrupt, ports.ChatCapabilityResume, ports.ChatCapabilityDiffs,
	} {
		if !caps.Has(capability) {
			t.Errorf("missing capability %q", capability)
		}
	}
	if caps.Has(ports.ChatCapabilityUsage) {
		t.Error("Hermes advertises no usage surface; usage capability should be off")
	}
	if plugin.resolveCalls != 1 || plugin.authCalls != 1 || versionCalls != 1 {
		t.Fatalf("plugin calls = resolve %d, auth %d, version %d; want one each",
			plugin.resolveCalls, plugin.authCalls, versionCalls)
	}
}

func TestDriverRejectsUnauthenticatedHermes(t *testing.T) {
	driver := newDriver(
		&fakePlugin{status: ports.AgentAuthStatusUnauthorized, binary: "/usr/bin/hermes"},
		func(context.Context, string) error { return nil },
		nil,
	)
	if _, err := driver.Probe(context.Background()); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("Probe error = %v, want ErrChatAuthRequired", err)
	}
}

type fakePlugin struct {
	binary       string
	status       ports.AgentAuthStatus
	resolveCalls int
	authCalls    int
}

func (p *fakePlugin) ResolveBinary(context.Context) (string, error) {
	p.resolveCalls++
	return p.binary, nil
}

func (p *fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	p.authCalls++
	return p.status, nil
}
