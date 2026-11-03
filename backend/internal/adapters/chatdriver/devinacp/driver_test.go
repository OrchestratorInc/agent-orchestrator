package devinacp

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePlugin struct {
	binary string
	status ports.AgentAuthStatus
}

func (p fakePlugin) ResolveBinary(context.Context) (string, error) { return p.binary, nil }
func (p fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return p.status, nil
}

func TestDriverReusesDevinPluginAndDeclaresNativeFeatures(t *testing.T) {
	driver := New(fakePlugin{binary: "/user/bin/devin", status: ports.AgentAuthStatusAuthorized}, nil)
	if driver.Harness() != domain.HarnessDevin {
		t.Fatalf("harness = %q, want %q", driver.Harness(), domain.HarnessDevin)
	}
	caps, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	for _, capability := range []ports.ChatCapability{
		ports.ChatCapabilityStreaming,
		ports.ChatCapabilityTools,
		ports.ChatCapabilityApprovals,
		ports.ChatCapabilityInterrupt,
		ports.ChatCapabilityResume,
		ports.ChatCapabilityHistory,
		ports.ChatCapabilityUsage,
		ports.ChatCapabilityPlans,
		ports.ChatCapabilityModels,
	} {
		if !caps.Has(capability) {
			t.Errorf("capability %q is false", capability)
		}
	}
}

func TestConfigureLaunchesNativeACPSubcommand(t *testing.T) {
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{
		WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeDefault,
		SystemPrompt: "AO worker instructions",
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if want := []string{"acp"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if env != nil {
		t.Fatalf("env = %#v, want nil", env)
	}
}

func TestConfigurePassesLaunchModel(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{
		WorkspacePath: t.TempDir(), Model: "swe-2-high",
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if want := []string{"acp", "--model", "swe-2-high"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSessionModeMapsOntoAdvertisedDevinModes(t *testing.T) {
	for _, tc := range []struct {
		permission ports.PermissionMode
		want       string
	}{
		{ports.PermissionModeDefault, ""},
		{ports.PermissionModeAcceptEdits, "accept-edits"},
		{ports.PermissionModeAuto, "smart"},
		{ports.PermissionModeBypassPermissions, "bypass"},
	} {
		if got := sessionMode(tc.permission); got != tc.want {
			t.Errorf("sessionMode(%q) = %q, want %q", tc.permission, got, tc.want)
		}
	}
}

func TestSessionOptionsMapModelAndThoughtLevel(t *testing.T) {
	tests := []struct {
		name     string
		settings ports.ChatTurnSettings
		want     []acpdriver.SessionOption
	}{
		{name: "empty"},
		{
			name:     "model",
			settings: ports.ChatTurnSettings{Model: "swe-2-high"},
			want:     []acpdriver.SessionOption{{ID: "model", Value: "swe-2-high"}},
		},
		{
			name:     "effort",
			settings: ports.ChatTurnSettings{Effort: "max"},
			want:     []acpdriver.SessionOption{{ID: "thought_level", Value: "max"}},
		},
		{
			name:     "approval is not a config option",
			settings: ports.ChatTurnSettings{Approval: ports.PermissionModeBypassPermissions},
		},
		{
			name: "model and effort",
			settings: ports.ChatTurnSettings{
				Model: "claude-opus-5-5-medium", Effort: "high",
				Approval: ports.PermissionModeAuto,
			},
			want: []acpdriver.SessionOption{
				{ID: "model", Value: "claude-opus-5-5-medium"},
				{ID: "thought_level", Value: "high"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionOptions(tc.settings); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("sessionOptions(%#v) = %#v, want %#v", tc.settings, got, tc.want)
			}
		})
	}
}

func TestSessionPreambleCarriesStandingInstructions(t *testing.T) {
	if got := sessionPreamble(acpdriver.LaunchConfig{}); got != nil {
		t.Fatalf("empty prompt preamble = %#v, want nil", got)
	}
	blocks := sessionPreamble(acpdriver.LaunchConfig{SystemPrompt: "AO worker instructions"})
	if len(blocks) != 1 || blocks[0].Text == nil {
		t.Fatalf("preamble = %#v, want one text block", blocks)
	}
	if text := blocks[0].Text.Text; !strings.Contains(text, "AO worker instructions") {
		t.Fatalf("preamble text %q missing the standing instructions", text)
	}
}

func TestDriverSupportsEveryAOPermissionMode(t *testing.T) {
	checker, ok := New(fakePlugin{binary: "/user/bin/devin"},
		slog.New(slog.NewTextHandler(io.Discard, nil))).(ports.ChatPermissionModeChecker)
	if !ok {
		t.Fatal("the Devin driver does not report which permission modes it runs in")
	}
	for _, mode := range ports.PermissionModesStrictestFirst {
		if !checker.SupportsPermissionMode(mode) {
			t.Errorf("Devin refuses %q, which maps onto an advertised session mode", mode)
		}
	}
}
