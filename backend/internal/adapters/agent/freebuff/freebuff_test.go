package freebuff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCandidateLaunchTaskIsNotArgv(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "space - workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"", "--trust-agents", "first\nsecond", "你好 🚀", "$(touch unwanted); `exit`"} {
		t.Run(prompt, func(t *testing.T) {
			cfg := ports.LaunchConfig{WorkspacePath: workspace, Prompt: prompt}
			got, err := candidateLaunchCommand(context.Background(), cfg)
			want := []string{"freebuff", "--cwd", workspace}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("argv = %v, err = %v; want %v", got, err, want)
			}
			strategy, err := New().GetPromptDeliveryStrategy(context.Background(), cfg)
			if err != nil || strategy != ports.PromptDeliveryAfterStart {
				t.Fatalf("strategy = %q, err = %v", strategy, err)
			}
		})
	}
}

func TestCandidateLaunchRejectsUnsupportedInputs(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*ports.LaunchConfig)
	}{
		{"model", func(c *ports.LaunchConfig) { c.Config.Model = "model" }},
		{"blank model", func(c *ports.LaunchConfig) { c.Config.Model = " " }},
		{"effort", func(c *ports.LaunchConfig) { c.Config.Effort = "high" }},
		{"mode", func(c *ports.LaunchConfig) { c.Config.Mode = "high" }},
		{"accept edits", func(c *ports.LaunchConfig) { c.Permissions = ports.PermissionModeAcceptEdits }},
		{"auto", func(c *ports.LaunchConfig) { c.Permissions = ports.PermissionModeAuto }},
		{"bypass", func(c *ports.LaunchConfig) { c.Permissions = ports.PermissionModeBypassPermissions }},
		{"unknown permission", func(c *ports.LaunchConfig) { c.Permissions = "future" }},
		{"nested permission", func(c *ports.LaunchConfig) { c.Config.Permissions = ports.PermissionModeAuto }},
		{"allow tools", func(c *ports.LaunchConfig) { c.AllowedTools = []string{"read"} }},
		{"deny tools", func(c *ports.LaunchConfig) { c.DisallowedTools = []string{"shell"} }},
		{"native identity", func(c *ports.LaunchConfig) { c.NativeSessionID = "id" }},
		{"instructions", func(c *ports.LaunchConfig) { c.SystemPrompt = "private" }},
		{"instruction file", func(c *ports.LaunchConfig) { c.SystemPromptFile = file }},
		{"empty workspace", func(c *ports.LaunchConfig) { c.WorkspacePath = "" }},
		{"relative workspace", func(c *ports.LaunchConfig) { c.WorkspacePath = "-workspace" }},
		{"missing workspace", func(c *ports.LaunchConfig) { c.WorkspacePath = filepath.Join(workspace, "missing") }},
		{"file workspace", func(c *ports.LaunchConfig) { c.WorkspacePath = file }},
		{"newline workspace", func(c *ports.LaunchConfig) { c.WorkspacePath = workspace + "\n" }},
		{"nul workspace", func(c *ports.LaunchConfig) { c.WorkspacePath = workspace + "\x00" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ports.LaunchConfig{WorkspacePath: workspace}
			tc.change(&cfg)
			if cmd, err := candidateLaunchCommand(context.Background(), cfg); err == nil || cmd != nil {
				t.Fatalf("unsupported input admitted: argv = %v, err = %v", cmd, err)
			}
		})
	}
}

func TestPublicLaunchRemainsUnqualified(t *testing.T) {
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault} {
		cfg := ports.LaunchConfig{WorkspacePath: t.TempDir(), Permissions: mode}
		cmd, err := New().GetLaunchCommand(context.Background(), cfg)
		if cmd != nil || !errors.Is(err, ErrQualificationRequired) {
			t.Fatalf("launch admitted: argv = %v, err = %v", cmd, err)
		}
	}
}

func TestRestoreAlwaysHardErrors(t *testing.T) {
	// Even a plausible ID is insufficient without exact loaded identity,
	// workspace/inference corroboration and renewed instruction delivery.
	for _, id := range []string{"", "missing", "../other", "2026-10-11_12-00-00", "corrupt-state"} {
		t.Run(id, func(t *testing.T) {
			cfg := ports.RestoreConfig{Session: ports.SessionRef{
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id},
			}}
			cmd, ok, err := New().GetRestoreCommand(context.Background(), cfg)
			if cmd != nil || ok || !errors.Is(err, ErrRestoreUnsupported) {
				t.Fatalf("unsafe restore fallback: argv = %v, ok = %v, err = %v", cmd, ok, err)
			}
		})
	}
}

func TestHooksRefuseStandingInstructions(t *testing.T) {
	for _, cfg := range []ports.WorkspaceHookConfig{
		{SystemPrompt: "private"}, {SystemPromptFile: "/private/instructions"},
	} {
		if err := New().GetAgentHooks(context.Background(), cfg); !errors.Is(err, ErrQualificationRequired) {
			t.Fatalf("instructions silently accepted: %v", err)
		}
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := New()
	checks := []struct {
		name string
		call func() error
	}{
		{"launch", func() error {
			_, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{})
			return err
		}},
		{"candidate", func() error {
			_, err := candidateLaunchCommand(ctx, ports.LaunchConfig{})
			return err
		}},
		{"restore", func() error {
			_, _, err := p.GetRestoreCommand(ctx, ports.RestoreConfig{})
			return err
		}},
		{"hooks", func() error { return p.GetAgentHooks(ctx, ports.WorkspaceHookConfig{}) }},
		{"delivery", func() error {
			_, err := p.GetPromptDeliveryStrategy(ctx, ports.LaunchConfig{})
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}
