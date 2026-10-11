package stakpak

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestInteractiveStartupDoesNotClaimPositionalTaskDelivery(t *testing.T) {
	workspace := t.TempDir()
	for _, prompt := range []string{"", "--approve-all", "auth", "hello\nworld", "你好"} {
		t.Run(prompt, func(t *testing.T) {
			got, err := freshArgs(context.Background(), ports.LaunchConfig{WorkspacePath: workspace, Prompt: prompt})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"--workdir", workspace}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestFreshArgsRejectsUnsupportedInputs(t *testing.T) {
	workspace := t.TempDir()
	cases := []struct {
		name   string
		change func(*ports.LaunchConfig)
	}{
		{"relative workspace", func(c *ports.LaunchConfig) { c.WorkspacePath = "." }},
		{"NUL workspace", func(c *ports.LaunchConfig) { c.WorkspacePath += "\x00" }},
		{"NUL prompt", func(c *ports.LaunchConfig) { c.Prompt = "a\x00b" }},
		{"invalid UTF-8", func(c *ports.LaunchConfig) { c.Prompt = string([]byte{0xff}) }},
		{"model", func(c *ports.LaunchConfig) { c.Config.Model = "openai/model" }},
		{"effort", func(c *ports.LaunchConfig) { c.Config.Effort = "high" }},
		{"mode", func(c *ports.LaunchConfig) { c.Config.Mode = "high" }},
		{"configured permissions", func(c *ports.LaunchConfig) { c.Config.Permissions = ports.PermissionModeAuto }},
		{"permissions", func(c *ports.LaunchConfig) { c.Permissions = ports.PermissionModeDefault }},
		{"allowlist", func(c *ports.LaunchConfig) { c.AllowedTools = []string{"view"} }},
		{"denylist", func(c *ports.LaunchConfig) { c.DisallowedTools = []string{"run"} }},
		{"caller identity", func(c *ports.LaunchConfig) { c.NativeSessionID = "claimed" }},
		{"missing instructions", func(c *ports.LaunchConfig) { c.SystemPrompt = "private context" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ports.LaunchConfig{WorkspacePath: workspace}
			tc.change(&cfg)
			if argv, err := freshArgs(context.Background(), cfg); err == nil || argv != nil {
				t.Fatalf("unsupported input accepted: argv=%q err=%v", argv, err)
			}
		})
	}
}

func TestStandingInstructions(t *testing.T) {
	cases := []struct {
		name, content, expected string
		valid                   bool
	}{
		{"exact", "private\ncontext\n", "private\ncontext\n", true},
		{"file only", "private context", "", true},
		{"mismatch", "old context", "new context", false},
		{"whitespace differs", "context\n", "context", false},
		{"empty", "", "", false},
		{"blank", " \n\t", "", false},
		{"NUL", "a\x00b", "", false},
		{"invalid UTF-8", string([]byte{0xff}), "", false},
		{"oversized", strings.Repeat("x", maxInstructionBytes+1), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			path := filepath.Join(workspace, "system.md")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := ports.LaunchConfig{WorkspacePath: workspace, Prompt: "visible task", SystemPromptFile: path, SystemPrompt: tc.expected}
			got, err := freshArgs(context.Background(), cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid {
				want := []string{"--workdir", workspace, "--system-prompt-file", path}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("standing context leaked or argv changed: %q", got)
				}
			}
		})
	}
	for _, path := range []string{"relative.md", filepath.Join(t.TempDir(), "missing.md"), t.TempDir()} {
		if err := validateInstructions(context.Background(), path, ""); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.md")
		link := filepath.Join(dir, "link.md")
		if err := os.WriteFile(target, []byte("context"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks unavailable")
		}
		if err := validateInstructions(context.Background(), link, "context"); err == nil {
			t.Fatal("symlink accepted")
		}
	})
}

func TestQualificationRefusals(t *testing.T) {
	p := New()
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeAcceptEdits, ports.PermissionModeBypassPermissions, "invalid"} {
		if cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Permissions: mode}); cmd != nil || !errors.Is(err, ErrUnqualified) {
			t.Fatalf("launch was admitted: %q %v", cmd, err)
		}
	}
	for _, id := range []string{"", "latest", "d090b4d9-dcd4-4a14-b68d-77eb79c74d80"} {
		cfg := ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}}
		if cmd, ok, err := p.GetRestoreCommand(context.Background(), cfg); cmd != nil || ok || !errors.Is(err, ErrUnqualified) {
			t.Fatalf("restore could fall back to fresh: %q %v %v", cmd, ok, err)
		}
		if _, ok, err := p.SessionInfo(context.Background(), cfg.Session); ok || err != nil {
			t.Fatal("unproved native metadata accepted")
		}
	}
	if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{}); !errors.Is(err, ErrUnqualified) {
		t.Fatal(err)
	}
	if status, err := p.AuthStatus(context.Background()); status != ports.AgentAuthStatusUnknown || err != nil {
		t.Fatalf("auth=%s err=%v", status, err)
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := New()
	checks := []struct {
		name string
		call func() error
	}{
		{"argv", func() error { _, err := freshArgs(ctx, ports.LaunchConfig{}); return err }},
		{"instructions", func() error { return validateInstructions(ctx, "", "") }},
		{"launch", func() error { _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{}); return err }},
		{"restore", func() error { _, _, err := p.GetRestoreCommand(ctx, ports.RestoreConfig{}); return err }},
		{"hooks", func() error { return p.GetAgentHooks(ctx, ports.WorkspaceHookConfig{}) }},
		{"auth", func() error { _, err := p.AuthStatus(ctx); return err }},
		{"binary", func() error { _, err := p.ResolveBinary(ctx); return err }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestObservedInteractivePromptStrategy(t *testing.T) {
	got, err := New().GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{Prompt: "--approve-all"})
	if err != nil || got != ports.PromptDeliveryAfterStart {
		t.Fatalf("strategy=%q err=%v", got, err)
	}
}
