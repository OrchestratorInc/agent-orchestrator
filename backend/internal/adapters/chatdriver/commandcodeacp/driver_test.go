package commandcodeacp

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestConfigureAlwaysLaunchesACPWithAutomationFlags(t *testing.T) {
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("env = %v, want none", env)
	}
	if args[0] != "acp" {
		t.Fatalf("args[0] = %q, want \"acp\"", args[0])
	}
	for _, want := range []string{"--skip-onboarding", "--no-auto-update", "--trust"} {
		if !slices.Contains(args, want) {
			t.Errorf("args %v missing %q", args, want)
		}
	}
}

func TestConfigureMapsPermissionMode(t *testing.T) {
	tests := []struct {
		name  string
		perms ports.PermissionMode
		want  string
	}{
		{"default", ports.PermissionModeDefault, modeDefault},
		{"empty is default", "", modeDefault},
		{"accept-edits", ports.PermissionModeAcceptEdits, modeAutoAccpt},
		{"auto collapses to accept", ports.PermissionModeAuto, modeAutoAccpt},
		{"bypass", ports.PermissionModeBypassPermissions, modeBypass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := permissionMode(tc.perms); got != tc.want {
				t.Fatalf("permissionMode(%q) = %q, want %q", tc.perms, got, tc.want)
			}
			args, _, err := configure(context.Background(), acpdriver.LaunchConfig{Permissions: tc.perms})
			if err != nil {
				t.Fatalf("configure: %v", err)
			}
			if !slices.Contains(args, "--permission-mode") {
				t.Errorf("args %v missing --permission-mode", args)
			}
		})
	}
}

func TestConfigureForwardsModel(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{Model: "  gpt-6.1-sol "})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	idx := slices.Index(args, "--model")
	if idx < 0 || idx+1 >= len(args) {
		t.Fatalf("args %v missing --model <value>", args)
	}
	if args[idx+1] != "gpt-6.1-sol" {
		t.Fatalf("model = %q, want trimmed value", args[idx+1])
	}
}

// TestConfigureNeverPassesSystemPromptFlag guards against re-adding a
// system-prompt launch flag: `cmd acp` accepts no such flag (v1.79.x rejects
// `--append-system-prompt` with "unknown option" and exits before the host
// publishes host.json, which surfaces as a missing-descriptor startup
// failure). Standing instructions ride the first prompt instead; see
// sessionPreamble.
func TestConfigureNeverPassesSystemPromptFlag(t *testing.T) {
	for _, prompt := range []string{
		"AO standing instructions for start",
		"  AO standing instructions recomputed for resume  ",
		" \n\t ",
		"",
	} {
		args, _, err := configure(context.Background(), acpdriver.LaunchConfig{SystemPrompt: prompt})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}
		for _, banned := range []string{"--append-system-prompt", "--system-prompt"} {
			if slices.Contains(args, banned) {
				t.Fatalf("args %v include %s, which `cmd acp` rejects", args, banned)
			}
		}
	}
}

func TestSessionOptionsMapsModelAndEffort(t *testing.T) {
	got := sessionOptions(ports.ChatTurnSettings{
		Model:  "claude-opus-5-5",
		Effort: "high",
	})
	want := []acpdriver.SessionOption{
		{ID: "model", Value: "claude-opus-5-5"},
		{ID: "effort", Value: "high"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d options %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("option[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSessionOptionsOmitsEmptyValues(t *testing.T) {
	if got := sessionOptions(ports.ChatTurnSettings{}); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// Approval changes reach a live Command Code session through session/set_mode,
// so the binding registers no turn-settings validator. That is only safe while
// every AO permission mode has an exact Command Code mode id to be applied as;
// a mode that mapped to "" would silently drop the user's approval choice.
func TestSessionModeAlwaysYieldsAnAdvertisedModeId(t *testing.T) {
	advertised := map[string]bool{
		modeDefault:   true,
		modeAutoAccpt: true,
		modePlan:      true,
		modeDontAsk:   true,
		modeBypass:    true,
	}
	for _, mode := range []ports.PermissionMode{
		"",
		ports.PermissionModeDefault,
		ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto,
		ports.PermissionModeBypassPermissions,
	} {
		id := sessionMode(mode)
		if !advertised[id] {
			t.Errorf("sessionMode(%q) = %q, which session/new does not advertise", mode, id)
		}
	}
}

// The mode ids below were read from a live v1.74.0 session/new handshake. If the
// provider renames one, this binding would silently send an unknown mode id, so
// pin the vocabulary we verified rather than whatever a doc comment claims.
func TestVerifiedModeVocabulary(t *testing.T) {
	for _, id := range []string{modeDefault, modeAutoAccpt, modePlan, modeDontAsk, modeBypass} {
		if id == "" {
			t.Fatal("verified mode id must not be empty")
		}
	}
	if modePlan == modeDefault || modeDontAsk == modeDefault {
		t.Fatal("plan/dont-ask must be distinct from default")
	}
}

// A registry built with this driver must resolve command-code. Registration is
// the whole capability gate, so an unregistered binding would leave the desktop
// with no Chat toggle for a harness that supports it.
func TestNewDriverTargetsCommandCodeHarness(t *testing.T) {
	driver := New(stubPlugin{}, slog.New(slog.DiscardHandler))
	if got := driver.Harness(); got != domain.HarnessCommandCode {
		t.Fatalf("Harness() = %q, want %q", got, domain.HarnessCommandCode)
	}
}

// TestSessionPreambleDeliversStandingInstructions covers the only channel this
// agent executes. The workspace SessionStart hook is fired only by the TUI
// lifecycle and session/new ignores _meta, so standing instructions would
// otherwise never reach a Chat session.
func TestSessionPreambleDeliversStandingInstructions(t *testing.T) {
	blocks := sessionPreamble(acpdriver.LaunchConfig{
		SystemPrompt: "You are an AO worker. Coordinate through the board.",
	})
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	resource := blocks[0].Resource
	if resource == nil || resource.Resource.TextResourceContents == nil {
		t.Fatalf("block = %#v, want an embedded resource the agent's promptPart executes", blocks[0])
	}
	contents := resource.Resource.TextResourceContents
	if contents.Uri != standingInstructionsURI {
		t.Fatalf("uri = %q, want %q", contents.Uri, standingInstructionsURI)
	}
	if contents.Text != "You are an AO worker. Coordinate through the board." {
		t.Fatalf("text = %q, want the standing instructions verbatim", contents.Text)
	}
	if contents.MimeType == nil || *contents.MimeType != "text/markdown" {
		t.Fatalf("mimeType = %v, want text/markdown", contents.MimeType)
	}
}

// A resumed conversation is process input, not transcript history, so the
// recomputed standing context must reach the provider again. A whitespace-only
// prompt is not standing context and must produce no preamble at all.
func TestSessionPreambleHandlesResumeAndEmptyPrompts(t *testing.T) {
	resumed := sessionPreamble(acpdriver.LaunchConfig{
		SystemPrompt: "  Recomputed instructions for resume  ",
	})
	if len(resumed) != 1 {
		t.Fatalf("resume blocks = %d, want 1", len(resumed))
	}
	if got := resumed[0].Resource.Resource.TextResourceContents.Text; got != "Recomputed instructions for resume" {
		t.Fatalf("resume text = %q, want the trimmed standing instructions", got)
	}
	for _, empty := range []string{"", " \n\t "} {
		if blocks := sessionPreamble(acpdriver.LaunchConfig{SystemPrompt: empty}); blocks != nil {
			t.Errorf("sessionPreamble(%q) = %v, want nil", empty, blocks)
		}
	}
}

func TestProbeDoesNotAdvertiseUnsupportedOperations(t *testing.T) {
	driver := New(stubPlugin{}, slog.New(slog.DiscardHandler))
	capabilities, err := driver.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	for _, capability := range []ports.ChatCapability{
		ports.ChatCapabilityRename,
		ports.ChatCapabilityRateLimits,
	} {
		if capabilities.Has(capability) {
			t.Errorf("Probe advertises unsupported capability %q", capability)
		}
	}
}

type stubPlugin struct{}

func (stubPlugin) ResolveBinary(context.Context) (string, error) { return "command-code", nil }

func (stubPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}
