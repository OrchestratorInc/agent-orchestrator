package reasonix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func compatiblePlugin() *Plugin {
	return &Plugin{lookup: func(context.Context) (string, error) { return "/reasonix", nil },
		probe: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "--version" {
				return []byte("reasonix v1.39.2-ao.642173c\n"), nil
			}
			return []byte("reasonix --append-system-prompt-file --permission-mode --resume-exact --dir --model"), nil
		}}
}

func TestLaunchAndRestore(t *testing.T) {
	workspace := t.TempDir()
	prompt := filepath.Join(t.TempDir(), "standing.md")
	if err := os.WriteFile(prompt, []byte("private standing instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode ports.PermissionMode
		want string
	}{
		{ports.PermissionModeDefault, "read-only"},
		{ports.PermissionModeAcceptEdits, "workspace-write"},
		{ports.PermissionModeAuto, "workspace-write"},
		{ports.PermissionModeBypassPermissions, "danger-full-access"},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			p := compatiblePlugin()
			cfg := ports.LaunchConfig{WorkspacePath: workspace, SystemPromptFile: prompt, Permissions: tc.mode, Prompt: "--evil\n用户 task", Config: ports.AgentConfig{Model: "custom/model"}, Kind: domain.KindWorker}
			got, err := p.GetLaunchCommand(t.Context(), cfg)
			want := []string{"/reasonix", "--dir", workspace, "--permission-mode", tc.want, "--append-system-prompt-file", prompt, "--model", "custom/model"}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("command=%v err=%v want=%v", got, err, want)
			}
			strategy, err := p.GetPromptDeliveryStrategy(t.Context(), cfg)
			if err != nil || strategy != ports.PromptDeliveryAfterStart {
				t.Fatalf("strategy=%s err=%v", strategy, err)
			}
			restored, ok, err := p.GetRestoreCommand(t.Context(), ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "af3245def600123abc"}}, SystemPromptFile: prompt, Permissions: tc.mode, Config: cfg.Config})
			want = append(want, "--resume-exact", "af3245def600123abc")
			if err != nil || !ok || !reflect.DeepEqual(restored, want) {
				t.Fatalf("restore=%v ok=%v err=%v", restored, ok, err)
			}
		})
	}
}

func TestLaunchFailsClosed(t *testing.T) {
	for _, cfg := range []ports.LaunchConfig{
		{}, {WorkspacePath: t.TempDir()},
		{WorkspacePath: t.TempDir(), SystemPromptFile: "relative.md"},
		{WorkspacePath: t.TempDir(), SystemPromptFile: filepath.Join(t.TempDir(), "missing")},
		{WorkspacePath: t.TempDir(), SystemPromptFile: t.TempDir()},
		{Permissions: ports.PermissionMode("unknown")},
	} {
		if cmd, err := compatiblePlugin().GetLaunchCommand(t.Context(), cfg); err == nil || cmd != nil {
			t.Fatalf("invalid launch accepted: %v %v", cmd, err)
		}
	}
	_, ok, err := compatiblePlugin().GetRestoreCommand(t.Context(), ports.RestoreConfig{})
	if err == nil || ok {
		t.Fatalf("missing native identity must not fall back to fresh launch: ok=%v err=%v", ok, err)
	}
}

func TestBinaryCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, version, help string
		fail                bool
	}{
		{"no exact resume", "reasonix v1.39.2-ao", "--append-system-prompt-file --permission-mode --resume --dir --model", true},
		{"released unsupported", "reasonix v1.39.2", "reasonix --model", true},
		{"collision", "other reasonix v1.2.3", "--append-system-prompt-file --permission-mode --resume-exact --dir --model", true},
		{"unidentified", "reasonix unknown", "--append-system-prompt-file --permission-mode --resume-exact --dir --model", true},
		{"compatible", "reasonix v1.39.2-ao.642173c", "--append-system-prompt-file --permission-mode --resume-exact --dir --model", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := compatiblePlugin()
			p.probe = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[0] == "--version" {
					return []byte(tc.version), nil
				}
				return []byte(tc.help), nil
			}
			_, err := p.ResolveBinary(t.Context())
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v fail=%v", err, tc.fail)
			}
			if err != nil && strings.Contains(err.Error(), tc.help) {
				t.Fatal("probe output leaked")
			}
		})
	}
	p := compatiblePlugin()
	p.probe = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("private diagnostics"), errors.New("private diagnostics")
	}
	if _, err := p.ResolveBinary(t.Context()); err == nil || strings.Contains(err.Error(), "private diagnostics") {
		t.Fatalf("unsafe probe error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.GetLaunchCommand(ctx, ports.LaunchConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
