package zcode

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGetLaunchCommandBuildsCrossPlatformArgv(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "zcode"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Kind:          domain.KindWorker,
		Prompt:        "fix this",
		WorkspacePath: "/tmp/workspace",
		SessionID:     "test-session-id",
	})
	if err != nil {
		t.Fatal(err)
	}

	// cfg.SessionID is the AO-internal id and must NOT be passed on launch;
	// ZCode mints its own native id, which GetRestoreCommand resumes by.
	// The prompt must not appear: AO delivers it after the TUI starts.
	want := []string{
		"zcode",
		"--cwd", "/tmp/workspace",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandOmitsCwdWithoutWorkspace(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "zcode"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zcode"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandMapsPermissionModes(t *testing.T) {
	tests := []struct {
		name        string
		permission  ports.PermissionMode
		want        []string
		notExpected string
	}{
		{
			name:        "default",
			permission:  ports.PermissionModeDefault,
			notExpected: "--mode",
		},
		{
			name:       "accept-edits",
			permission: ports.PermissionModeAcceptEdits,
			want:       []string{"--mode", "edit"},
		},
		{
			name:       "auto",
			permission: ports.PermissionModeAuto,
			want:       []string{"--mode", "build"},
		},
		{
			name:       "bypass-permissions",
			permission: ports.PermissionModeBypassPermissions,
			want:       []string{"--mode", "yolo"},
		},
		{
			name:        "empty",
			permission:  "",
			notExpected: "--mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "zcode"}
			cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
				Permissions: tt.permission,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(tt.want) > 0 && !containsSubsequence(cmd, tt.want) {
				t.Fatalf("command %#v does not contain %#v", cmd, tt.want)
			}
			if tt.notExpected != "" && contains(cmd, tt.notExpected) {
				t.Fatalf("command %#v contains %q", cmd, tt.notExpected)
			}
		})
	}
}

func TestGetLaunchCommandPassesDisallowedTools(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "zcode"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath:   "/tmp/workspace",
		DisallowedTools: []string{"Bash(git push)", "WebFetch"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(cmd, []string{"--disallowed-tools", "Bash(git push),WebFetch"}) {
		t.Fatalf("command %#v does not contain the joined --disallowed-tools value", cmd)
	}
}

func TestGetLaunchCommandRejectsUnsupportedOverrides(t *testing.T) {
	tests := []struct {
		name string
		cfg  ports.LaunchConfig
	}{
		{
			name: "model override",
			cfg:  ports.LaunchConfig{Config: ports.AgentConfig{Model: "glm-5"}},
		},
		{
			name: "effort override",
			cfg:  ports.LaunchConfig{Config: ports.AgentConfig{Effort: "high"}},
		},
		{
			name: "allowed tools",
			cfg:  ports.LaunchConfig{AllowedTools: []string{"Read"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "zcode"}
			if _, err := plugin.GetLaunchCommand(context.Background(), tt.cfg); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

func TestGetLaunchCommandMissingBinaryFails(t *testing.T) {
	plugin := &Plugin{}

	// When the binary is not on PATH or any well-known location, the resolver
	// MUST surface ports.ErrAgentBinaryNotFound rather than a silent string
	// fallback. On machines where zcode is installed, resolution succeeds and
	// the command must start with it.
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
			t.Fatalf("err = %v, want wrapped ports.ErrAgentBinaryNotFound", err)
		}
		return
	}
	if len(cmd) == 0 || cmd[0] == "" {
		t.Fatalf("cmd = %#v, want a resolved binary", cmd)
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	plugin := &Plugin{}

	got, err := plugin.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.PromptDeliveryInCommand {
		t.Fatalf("promptless session: got %v, want %v", got, ports.PromptDeliveryInCommand)
	}

	got, err = plugin.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{Prompt: "fix this"})
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.PromptDeliveryAfterStart {
		t.Fatalf("prompted session: got %v, want %v", got, ports.PromptDeliveryAfterStart)
	}
}

func TestPromptReadinessHintsWaitsForComposer(t *testing.T) {
	hints, err := (&Plugin{}).PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if hints.InitialDelay <= 0 {
		t.Fatalf("hints.InitialDelay = %v, want a bounded settle delay", hints.InitialDelay)
	}
	// Keystrokes typed before the composer renders are lost (observed live as
	// an echoed prompt line, an empty composer and a session that never ran),
	// so readiness must wait for the composer placeholder — with a bounded
	// timeout so a build that drops it degrades to fixed-delay delivery.
	if len(hints.Patterns) != 1 || hints.Patterns[0] != "Ask a task about this workspace" {
		t.Fatalf("hints.Patterns = %#v, want the composer placeholder marker", hints.Patterns)
	}
	if hints.Timeout <= 0 {
		t.Fatalf("hints.Timeout = %v, want a bounded fallback timeout", hints.Timeout)
	}
	if hints.PollInterval <= 0 || hints.Lines <= 0 {
		t.Fatalf("hints.PollInterval = %v, hints.Lines = %v, want positive poll settings", hints.PollInterval, hints.Lines)
	}
}

func TestGetRestoreCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "zcode"}

	tests := []struct {
		name           string
		agentSessionID string
		workspacePath  string
		permission     ports.PermissionMode
		wantOk         bool
		want           []string
	}{
		{
			name:           "restore with session id",
			agentSessionID: "sess_example",
			workspacePath:  "/tmp/workspace",
			permission:     ports.PermissionModeDefault,
			wantOk:         true,
			want:           []string{"zcode", "--cwd", "/tmp/workspace", "--resume", "sess_example"},
		},
		{
			name:           "restore re-applies permission mode",
			agentSessionID: "sess_example",
			workspacePath:  "/tmp/workspace",
			permission:     ports.PermissionModeBypassPermissions,
			wantOk:         true,
			want:           []string{"zcode", "--cwd", "/tmp/workspace", "--mode", "yolo", "--resume", "sess_example"},
		},
		{
			name:          "no session id falls back to latest session for the worktree",
			workspacePath: "/tmp/workspace",
			permission:    ports.PermissionModeDefault,
			wantOk:        true,
			want:          []string{"zcode", "--cwd", "/tmp/workspace", "--continue"},
		},
		{
			name:   "neither session id nor workspace",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
				Session: ports.SessionRef{
					Metadata:      map[string]string{"agentSessionId": tt.agentSessionID},
					WorkspacePath: tt.workspacePath,
				},
				Permissions: tt.permission,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.wantOk {
				t.Fatalf("unexpected ok: got %v, want %v", ok, tt.wantOk)
			}
			if tt.wantOk && !reflect.DeepEqual(cmd, tt.want) {
				t.Fatalf("unexpected command\nwant: %#v\n got: %#v", tt.want, cmd)
			}
		})
	}
}

func TestExitDetectionModeIsSupervisor(t *testing.T) {
	if got := (&Plugin{}).ExitDetectionMode(); got != ports.AgentExitDetectionSupervisor {
		t.Fatalf("ExitDetectionMode = %q, want %q", got, ports.AgentExitDetectionSupervisor)
	}
}

func TestManifest(t *testing.T) {
	plugin := &Plugin{}

	manifest := plugin.Manifest()
	if manifest.ID != adapterID {
		t.Fatalf("unexpected manifest ID: got %q, want %q", manifest.ID, adapterID)
	}
	if manifest.ID != string(domain.HarnessZCode) {
		t.Fatalf("manifest ID %q does not match domain.HarnessZCode", manifest.ID)
	}
	if manifest.Name != "ZCode" {
		t.Fatalf("unexpected manifest name: got %q, want \"ZCode\"", manifest.Name)
	}
	if len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != adapters.CapabilityAgent {
		t.Fatalf("unexpected capabilities: %#v", manifest.Capabilities)
	}
}

func TestSessionInfoReadsCapturedMetadata(t *testing.T) {
	plugin := &Plugin{}

	_, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		ID: "session-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unexpected ok without captured metadata")
	}

	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		ID:       "session-123",
		Metadata: map[string]string{"agentSessionId": "sess_example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || info.AgentSessionID != "sess_example" {
		t.Fatalf("info = %#v, ok = %v; want captured agentSessionId", info, ok)
	}
}

func TestGetConfigSpecReportsModeEnum(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Fields) != 1 || spec.Fields[0].Key != "mode" {
		t.Fatalf("spec fields = %#v, want a single mode field", spec.Fields)
	}
	for _, want := range []string{"plan", "build", "edit", "yolo", "guarded"} {
		if !contains(spec.Fields[0].Enum, want) {
			t.Fatalf("mode enum %#v missing %q", spec.Fields[0].Enum, want)
		}
	}
}

func TestGetLaunchCommandUsesConfiguredModeWithoutOverride(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "zcode"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: "/tmp/workspace",
		Config:        ports.AgentConfig{Mode: "plan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(cmd, []string{"--mode", "plan"}) {
		t.Fatalf("command %#v does not contain the configured mode", cmd)
	}
}

func TestGetLaunchCommandPermissionOverrideWinsOverConfiguredMode(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "zcode"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: "/tmp/workspace",
		Config:        ports.AgentConfig{Mode: "plan"},
		Permissions:   ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubsequence(cmd, []string{"--mode", "yolo"}) || contains(cmd, "plan") {
		t.Fatalf("command %#v did not prefer the permission override", cmd)
	}
}

func TestSupportsTerminalVersionRefreshIsOff(t *testing.T) {
	if (&Plugin{}).SupportsTerminalVersionRefresh() {
		t.Fatal("SupportsTerminalVersionRefresh = true, want false")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func containsSubsequence(haystack, needle []string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j, n := range needle {
			if haystack[i+j] != n {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
