package commandcodeacp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestConfigureNeverPassesModFlag locks issue #6784: `cmd acp` accepts
// --mod/--mod-option but silently never loads the mod (verified on
// v1.82.4/v1.83.0: the same sentinel mod logs under `cmd -p --mod` and logs
// nothing under `cmd acp --mod`). Passing the flag would look configured while
// staying inert, so configure must never emit it; standing context rides
// sessionPreamble instead.
func TestConfigureNeverPassesModFlag(t *testing.T) {
	args, _, err := configure(context.Background(), acpdriver.LaunchConfig{})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	for _, banned := range []string{"--mod", "--mod-option"} {
		if slices.Contains(args, banned) {
			t.Fatalf("args %v include %s, which `cmd acp` accepts but silently ignores (issue #6784)", args, banned)
		}
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

func TestConfigurePointsTheSessionAtItsOwnHome(t *testing.T) {
	dataDir := t.TempDir()
	args, env, err := configure(context.Background(), acpdriver.LaunchConfig{
		SessionID:    "mer-home-1",
		DataDir:      dataDir,
		SystemPrompt: "You are an AO worker. Coordinate through the board.",
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	for _, arg := range args {
		if strings.Contains(arg, "worker") || strings.Contains(arg, "--append-system-prompt") {
			t.Fatalf("argv leaked the system prompt or used an unsupported flag: %#v", args)
		}
	}
	home := env["HOME"]
	if home == "" || env["USERPROFILE"] != home {
		t.Fatalf("env = %#v, want HOME and USERPROFILE to agree", env)
	}
	if want := filepath.Join(dataDir, "harnesses", "command-code", "mer-home-1"); home != want {
		t.Fatalf("HOME = %q, want %q", home, want)
	}
	memory, err := os.ReadFile(filepath.Join(home, ".commandcode", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read session memory: %v", err)
	}
	if !strings.Contains(string(memory), "Coordinate through the board") {
		t.Fatalf("session memory = %q, want the standing instructions", memory)
	}
}

// A session that cannot write its memory would run with no AO role at all, so the
// launch fails rather than starting a role-less conversation.
func TestConfigureFailsWhenTheSessionHomeCannotBeWritten(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "file-not-a-dir")
	if err := os.WriteFile(dataDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	if _, _, err := configure(context.Background(), acpdriver.LaunchConfig{
		SessionID:    "mer-home-2",
		DataDir:      dataDir,
		SystemPrompt: "You are an AO worker.",
	}); err == nil {
		t.Fatal("configure succeeded without a writable session home")
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
