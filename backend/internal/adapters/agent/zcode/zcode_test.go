package zcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "zcode" {
		t.Fatalf("ID = %q, want zcode", m.ID)
	}
	if m.Name != "ZCode" {
		t.Fatalf("Name = %q, want ZCode", m.Name)
	}
	hasAgent := false
	for _, c := range m.Capabilities {
		if c == adapters.CapabilityAgent {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Fatal("missing CapabilityAgent")
	}
}

func TestGetConfigSpec(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(spec.Fields) != 1 {
		t.Fatalf("expected exactly one field, got %d", len(spec.Fields))
	}
	field := spec.Fields[0]
	// The registry gate requires every harness to expose a "model" or "mode"
	// config field; zcode has no model CLI surface, so the starting permission
	// mode rides under "mode".
	if field.Key != "mode" {
		t.Fatalf("field key = %q, want mode", field.Key)
	}
	if field.Type != ports.ConfigFieldEnum {
		t.Fatalf("field type = %q, want enum", field.Type)
	}
	if !reflect.DeepEqual(field.Enum, permissionConfigEnum) {
		t.Fatalf("enum = %#v, want %#v", field.Enum, permissionConfigEnum)
	}
	if len(field.Enum) != 4 {
		t.Fatalf("enum has %d values, want 4", len(field.Enum))
	}
	if field.Default != string(ports.PermissionModeAcceptEdits) {
		t.Fatalf("Default = %v, want accept-edits", field.Default)
	}
	// The yolo hazard is the reason the default is pinned; the field
	// description (the UI surface for config fields) must carry it.
	if !strings.Contains(field.Description, "yolo") {
		t.Fatalf("description %q does not mention the yolo default hazard", field.Description)
	}
	for _, f := range spec.Fields {
		if f.Key == "model" {
			t.Fatal("unexpected model field: zcode has no model CLI surface to forward it to")
		}
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	s, err := (&Plugin{}).GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != ports.PromptDeliveryInCommand {
		t.Fatalf("strategy = %q, want %q", s, ports.PromptDeliveryInCommand)
	}
}

func TestGetLaunchCommandWithPrompt(t *testing.T) {
	ws := t.TempDir()
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: ws,
		Prompt:        "add a health check",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Default/unset permissions pin --mode edit (never the flagless yolo
	// inheritance).
	want := []string{"zcode", "--cwd", ws, "--mode", "edit", "-p", "add a health check"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandPrependsSystemPrompt(t *testing.T) {
	ws := t.TempDir()
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: ws,
		SystemPrompt:  "follow repo rules",
		Prompt:        "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Instructions sit before the task: they are the session's first turn.
	want := []string{"zcode", "--cwd", ws, "--mode", "edit", "-p", "follow repo rules\n\ndo the thing"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandSystemPromptOnlyTask(t *testing.T) {
	// An instructions-only first turn still forces a -p run: that turn
	// persists the role into the native session for later -c restores.
	ws := t.TempDir()
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: ws,
		SystemPrompt:  "you are the orchestrator",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"zcode", "--cwd", ws, "--mode", "edit", "-p", "you are the orchestrator"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandInlinesSystemPromptFileContents(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "system.md")
	if err := os.WriteFile(file, []byte("file contents win\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath:    ws,
		SystemPromptFile: file,
		SystemPrompt:     "inline ignored",
		Prompt:           "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"zcode", "--cwd", ws, "--mode", "edit", "-p", "file contents win\n\ndo the thing"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandSystemPromptFileReadError(t *testing.T) {
	p := &Plugin{resolvedBinary: "zcode"}
	_, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SystemPromptFile: filepath.Join(t.TempDir(), "missing.md"),
		SystemPrompt:     "inline ignored",
	})
	if err == nil {
		t.Fatal("expected error for unreadable system-prompt file, got nil")
	}
}

func TestGetLaunchCommandNoPromptNoInstructions(t *testing.T) {
	// Both empty: a bare interactive TUI launch, no -p.
	ws := t.TempDir()
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: ws,
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"zcode", "--cwd", ws, "--mode", "edit"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetLaunchCommandPermissionModes(t *testing.T) {
	ws := t.TempDir()
	for _, tc := range []struct {
		mode ports.PermissionMode
		want []string
	}{
		// default, empty, and unknown pin --mode edit; none may inherit
		// zcode's flagless yolo default.
		{ports.PermissionModeDefault, []string{"zcode", "--cwd", ws, "--mode", "edit"}},
		{"", []string{"zcode", "--cwd", ws, "--mode", "edit"}},
		{"turbo", []string{"zcode", "--cwd", ws, "--mode", "edit"}},
		{ports.PermissionModeAcceptEdits, []string{"zcode", "--cwd", ws, "--mode", "edit"}},
		{ports.PermissionModeAuto, []string{"zcode", "--cwd", ws, "--mode", "build"}},
		{ports.PermissionModeBypassPermissions, []string{"zcode", "--cwd", ws, "--mode", "yolo"}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			p := &Plugin{resolvedBinary: "zcode"}
			cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
				WorkspacePath: ws,
				Permissions:   tc.mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cmd, tc.want) {
				t.Fatalf("cmd = %#v, want %#v", cmd, tc.want)
			}
			for _, arg := range cmd {
				if arg == "-p" {
					t.Fatalf("cmd = %#v unexpectedly contains -p", cmd)
				}
			}
		})
	}
}

func TestGetLaunchCommandConfigPermissionsFallback(t *testing.T) {
	// A stored project config drives the mode when the launch carries no
	// explicit override.
	ws := t.TempDir()
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: ws,
		Config:        ports.AgentConfig{Permissions: ports.PermissionModeBypassPermissions},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zcode", "--cwd", ws, "--mode", "yolo"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}

	// The explicit per-spawn override wins over the stored config.
	cmd, err = p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		WorkspacePath: ws,
		Permissions:   ports.PermissionModeAuto,
		Config:        ports.AgentConfig{Permissions: ports.PermissionModeBypassPermissions},
	})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"zcode", "--cwd", ws, "--mode", "build"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandWithNativeID(t *testing.T) {
	// Vestigial branch: reads agentSessionId nothing writes for zcode today,
	// but must keep working if a capture probe populates it.
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			WorkspacePath: "/tmp/ws",
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "sess_abc"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}

	// Restore re-applies the pinned mode so a resumed session cannot fall back
	// onto zcode's flagless yolo default.
	want := []string{"zcode", "--cwd", "/tmp/ws", "--mode", "edit", "--resume", "sess_abc"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandContinueFallback(t *testing.T) {
	// Primary path: cwd-scoped -c, deterministic because each AO session owns
	// its worktree.
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			WorkspacePath: "/tmp/ws",
			Metadata:      map[string]string{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}

	want := []string{"zcode", "--cwd", "/tmp/ws", "--mode", "edit", "-c"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandReappliesExplicitAndConfiguredMode(t *testing.T) {
	p := &Plugin{resolvedBinary: "zcode"}
	ws := "/tmp/ws"

	// Explicit restore override.
	cmd, _, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Session:     ports.SessionRef{WorkspacePath: ws, Metadata: map[string]string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zcode", "--cwd", ws, "--mode", "yolo", "-c"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}

	// Stored project config when the restore carries no override.
	cmd, _, err = p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Config:  ports.AgentConfig{Permissions: ports.PermissionModeAuto},
		Session: ports.SessionRef{WorkspacePath: ws, Metadata: map[string]string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"zcode", "--cwd", ws, "--mode", "build", "-c"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandNoWorkspace(t *testing.T) {
	p := &Plugin{resolvedBinary: "zcode"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("ok=true with cmd %#v, want false", cmd)
	}
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil", cmd)
	}
}

func TestGetAgentHooksNoOp(t *testing.T) {
	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: t.TempDir()}); err != nil {
		t.Fatalf("GetAgentHooks err = %v, want nil", err)
	}
}

func TestSessionInfo(t *testing.T) {
	p := &Plugin{}

	info, ok, err := p.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "sess_abc",
			ports.MetadataKeyTitle:          "fix the flake",
			ports.MetadataKeySummary:        "done",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok=false with populated metadata, want true")
	}
	want := ports.SessionInfo{AgentSessionID: "sess_abc", Title: "fix the flake", Summary: "done"}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("info = %#v, want %#v", info, want)
	}

	info, ok, err = p.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("ok=true with empty metadata, info = %#v, want false", info)
	}
	if !reflect.DeepEqual(info, ports.SessionInfo{}) {
		t.Fatalf("info = %#v, want zero", info)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (&Plugin{}).GetConfigSpec(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetConfigSpec err = %v, want context.Canceled", err)
	}
	if _, err := (&Plugin{}).GetPromptDeliveryStrategy(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetPromptDeliveryStrategy err = %v, want context.Canceled", err)
	}
	if _, err := (&Plugin{}).GetLaunchCommand(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetLaunchCommand err = %v, want context.Canceled", err)
	}
	if err := (&Plugin{}).GetAgentHooks(ctx, ports.WorkspaceHookConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetAgentHooks err = %v, want context.Canceled", err)
	}
	if _, _, err := (&Plugin{}).GetRestoreCommand(ctx, ports.RestoreConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetRestoreCommand err = %v, want context.Canceled", err)
	}
	if _, _, err := (&Plugin{}).SessionInfo(ctx, ports.SessionRef{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SessionInfo err = %v, want context.Canceled", err)
	}
	if _, err := ResolveZcodeBinary(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveZcodeBinary err = %v, want context.Canceled", err)
	}
}
