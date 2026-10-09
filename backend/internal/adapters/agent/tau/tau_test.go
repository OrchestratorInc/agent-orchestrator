package tau

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func fixturePlugin() *Plugin {
	return &Plugin{resolvedBinary: "/test/tau", run: func(context.Context, string, ...string) ([]byte, error) { return []byte("tau 0.4.7\n"), nil }}
}

func TestLaunchKeepsTasksOutOfArgvAndRequiresExplicitBypass(t *testing.T) {
	for _, prompt := range []string{"sessions", "setup", "--version", "first\nsecond"} {
		t.Run(prompt, func(t *testing.T) {
			p := fixturePlugin()
			cfg := ports.LaunchConfig{WorkspacePath: t.TempDir(), Prompt: prompt, Permissions: ports.PermissionModeBypassPermissions}
			cmd, err := p.GetLaunchCommand(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range cmd {
				if arg == prompt {
					t.Fatalf("task escaped into argv: %#v", cmd)
				}
			}
			if cmd[len(cmd)-1] != "--new-session" {
				t.Fatalf("fresh launch can reuse a default session: %#v", cmd)
			}
			strategy, _ := p.GetPromptDeliveryStrategy(context.Background(), cfg)
			if strategy != ports.PromptDeliveryAfterStart || !p.FirstSignalProvesInputReady() {
				t.Fatal("task must wait for native mounted TUI readiness")
			}
		})
	}
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, "unknown"} {
		if _, err := fixturePlugin().GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: mode}); err == nil {
			t.Fatalf("accepted unsupported permissions %q", mode)
		}
	}
}

func TestRestoreUsesExactNativeIDAndReappliesPrivateInstructions(t *testing.T) {
	work := t.TempDir()
	instructions := filepath.Join(t.TempDir(), "standing.txt")
	if err := os.WriteFile(instructions, []byte("private token"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "8bc1a3fbba2142bcb406fe4a4b6fe21b"
	cfg := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: work, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}, Permissions: ports.PermissionModeBypassPermissions, SystemPromptFile: instructions, Config: ports.AgentConfig{Model: "openrouter/vendor/model"}}
	p := fixtureRestorePlugin(id, work)
	cmd, ok, err := p.GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok {
		t.Fatalf("restore = %#v, %v, %v", cmd, ok, err)
	}
	want := []string{"/test/tau", "--cwd", work, "--approve", "--no-extensions", "--extension", extensionPath(work), "--append-system-prompt", instructions, "--provider", "openrouter", "--model", "vendor/model", "--session", id}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("argv = %#v, want %#v", cmd, want)
	}
	for _, invalid := range []string{"latest", "default", "../escape", "--help", "short"} {
		cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] = invalid
		if _, _, err := fixturePlugin().GetRestoreCommand(context.Background(), cfg); err == nil {
			t.Fatalf("accepted invalid ID %q", invalid)
		}
	}
	cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] = ""
	if _, ok, err := fixturePlugin().GetRestoreCommand(context.Background(), cfg); err != nil || ok {
		t.Fatalf("missing ID = %v, %v", ok, err)
	}
}

func TestVersionGateRejectsUninspectedBinary(t *testing.T) {
	p := fixturePlugin()
	p.run = func(context.Context, string, ...string) ([]byte, error) { return []byte("tau 0.3.0"), nil }
	_, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeBypassPermissions})
	if err == nil || !strings.Contains(err.Error(), SupportedVersion) {
		t.Fatalf("version gate = %v", err)
	}
}

func TestManagedHooksPreserveUserFiles(t *testing.T) {
	work := t.TempDir()
	cfg := ports.WorkspaceHookConfig{WorkspacePath: work}
	p := fixturePlugin()
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(extensionPath(work))
	if err != nil || !strings.Contains(string(data), "agent_settled") || !strings.Contains(string(data), "session_start") {
		t.Fatalf("hooks = %s, %v", data, err)
	}
	if err := os.WriteFile(extensionPath(work), []byte("user extension"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("overwrote user extension")
	}
}

func TestCancellationAndContexts(t *testing.T) {
	p := fixturePlugin()
	if p.InterruptInput() != "\x1b" {
		t.Fatal("Tau requires Escape rather than Ctrl+C")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("launch cancellation = %v", err)
	}
	if err := p.GetAgentHooks(ctx, ports.WorkspaceHookConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("hooks cancellation = %v", err)
	}
}

func TestToolRestrictionsAreRejectedOnLaunchAndRestore(t *testing.T) {
	for _, tc := range []struct {
		name            string
		allowed, denied []string
	}{
		{name: "allow", allowed: []string{"Read"}},
		{name: "deny", denied: []string{"Bash"}},
		{name: "both", allowed: []string{"Read"}, denied: []string{"Bash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixturePlugin()
			p.run = func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("unsupported policy reached provider probe")
				return nil, nil
			}
			workspace := t.TempDir()
			_, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{WorkspacePath: workspace, Permissions: ports.PermissionModeBypassPermissions, AllowedTools: tc.allowed, DisallowedTools: tc.denied})
			if err == nil || !strings.Contains(err.Error(), "tool allow/deny restrictions") {
				t.Fatalf("launch restriction error = %v", err)
			}
			_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "8bc1a3fbba2142bcb406fe4a4b6fe21b"}}, Permissions: ports.PermissionModeBypassPermissions, AllowedTools: tc.allowed, DisallowedTools: tc.denied})
			if ok || err == nil || !strings.Contains(err.Error(), "tool allow/deny restrictions") {
				t.Fatalf("restore restriction = %v, %v", ok, err)
			}
		})
	}
}

func fixtureRestorePlugin(id, workspace string) *Plugin {
	p := fixturePlugin()
	p.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "sessions" {
			return []byte(id + "\tUntitled\tmodel\t" + workspace + "\n"), nil
		}
		return []byte("tau 0.4.7\n"), nil
	}
	return p
}

func TestRestoreRejectsMissingOrMismatchedWorkspace(t *testing.T) {
	const id = "8bc1a3fbba2142bcb406fe4a4b6fe21b"
	workspace := t.TempDir()
	for _, tc := range []struct{ name, nativeID, nativeWorkspace string }{
		{"missing ID", "different", workspace},
		{"different directory", id, t.TempDir()},
		{"missing directory", id, filepath.Join(t.TempDir(), "gone")},
		{"relative directory", id, "relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixtureRestorePlugin(tc.nativeID, tc.nativeWorkspace)
			_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}, Permissions: ports.PermissionModeBypassPermissions})
			if err == nil || ok {
				t.Fatalf("restore accepted unowned workspace: %v, %v", ok, err)
			}
		})
	}
}

func TestRestoreAcceptsSymlinkToSameDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges")
	}
	const id = "8bc1a3fbba2142bcb406fe4a4b6fe21b"
	workspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	p := fixtureRestorePlugin(id, alias)
	_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}, Permissions: ports.PermissionModeBypassPermissions})
	if err != nil || !ok {
		t.Fatalf("same workspace alias rejected: %v, %v", ok, err)
	}
}
