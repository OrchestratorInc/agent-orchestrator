package museacp

import (
	"context"
	"reflect"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDriverIsMuse(t *testing.T) {
	if got := New(muse.New(), nil).Harness(); got != domain.HarnessMuse {
		t.Fatalf("harness = %q, want %q", got, domain.HarnessMuse)
	}
}

// muse acp refuses approval flags, so approval travels as the session mode;
// bypass also drops the sandbox, as --yolo does in the terminal UI.
func TestConfigureLaunchesMuseACP(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  acpdriver.LaunchConfig
		args []string
		env  map[string]string
	}{
		{
			name: "default",
			cfg:  acpdriver.LaunchConfig{Permissions: ports.PermissionModeDefault},
			args: []string{"acp", "--trust-workspace"},
			env:  map[string]string{"MUSE_EXPERIMENTAL_ACP": "on"},
		},
		{
			name: "model and instructions",
			cfg:  acpdriver.LaunchConfig{Permissions: ports.PermissionModeAuto, Model: " muse-model ", SystemPrompt: " Be brief. "},
			args: []string{"acp", "--trust-workspace", "--model", "muse-model"},
			env:  map[string]string{"MUSE_EXPERIMENTAL_ACP": "on", muse.DeveloperPromptEnvVar: "Be brief."},
		},
		{
			name: "bypass",
			cfg:  acpdriver.LaunchConfig{Permissions: ports.PermissionModeBypassPermissions},
			args: []string{"acp", "--trust-workspace", "--disable-sandbox"},
			env:  map[string]string{"MUSE_EXPERIMENTAL_ACP": "on"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, env, err := configure(context.Background(), tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(args, tc.args) || !reflect.DeepEqual(env, tc.env) {
				t.Fatalf("configure = %v %v, want %v %v", args, env, tc.args, tc.env)
			}
			for _, arg := range args {
				if arg == "--yolo" || arg == "--approval-mode" {
					t.Fatalf("muse acp refuses %s: %v", arg, args)
				}
			}
		})
	}
}

func TestSessionModeMatchesTheTerminal(t *testing.T) {
	for permission, want := range map[ports.PermissionMode]string{
		ports.PermissionModeDefault:           "onRequest",
		ports.PermissionModeAcceptEdits:       "allowAll",
		ports.PermissionModeAuto:              "allowAll",
		ports.PermissionModeBypassPermissions: "allowAll",
	} {
		if got := sessionMode(permission); got != want {
			t.Errorf("sessionMode(%q) = %q, want %q", permission, got, want)
		}
	}
}

func TestHiddenCommands(t *testing.T) {
	for name, hidden := range map[string]bool{
		"manage-settings": true, "import": true, "migrate": true, "resume-claude": true, "resume-codex": true,
		"compact": false, "doctor": false, "plan": false, "my-skill": false,
	} {
		if got := hiddenCommand(acpsdk.AvailableCommand{Name: name}); got != hidden {
			t.Errorf("hiddenCommand(%q) = %v, want %v", name, got, hidden)
		}
	}
}

func TestVersionFloor(t *testing.T) {
	for output, ok := range map[string]bool{
		"Muse Code 1.4.25 (1.4.25-R9361.1)": true,
		"Muse Code 1.4.24":                  true,
		"Muse Code 1.4.23":                  false,
		"Muse Code 1.3.9":                   false,
		"muse":                              false,
	} {
		if err := validateVersionOutput(output); (err == nil) != ok {
			t.Errorf("validateVersionOutput(%q) = %v, want ok=%v", output, err, ok)
		}
	}
}
