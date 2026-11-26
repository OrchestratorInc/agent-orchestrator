//go:build !windows

package memcode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMemcodeFreshAndRestorePreserveEndpointAfterShellStartup(t *testing.T) {
	bin := t.TempDir()
	program := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'memcode v0.50.0'; else printf '%s' \"${OPENAI_BASE_URL-unset}\"; fi\n"
	if err := os.WriteFile(filepath.Join(bin, "memcode"), []byte(program), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"inherited endpoint", nil, "https://shell.invalid/v1"},
		{"project endpoint", map[string]string{"OPENAI_BASE_URL": "https://project.invalid/v1"}, "https://project.invalid/v1"},
		{"project clear", map[string]string{"OPENAI_BASE_URL": ""}, "unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, restore := range []bool{false, true} {
				env := map[string]string{"OPENAI_API_KEY": "synthetic-secret"}
				for key, value := range tc.env {
					env[key] = value
				}
				p := New()
				p.AugmentRuntimeLaunchEnv(env, t.TempDir(), domain.SessionID("ao-route"), "launch")
				workspace := t.TempDir()
				var argv []string
				if restore {
					seedHistory(t, workspace, validHistory)
					plan, err := p.PrepareNativeRestore(t.Context(), ports.RestoreConfig{Env: env, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: testID}}})
					if err != nil {
						t.Fatal(err)
					}
					argv = plan.Argv()
				} else {
					var err error
					argv, err = p.GetLaunchCommand(t.Context(), ports.LaunchConfig{WorkspacePath: workspace, Env: env})
					if err != nil {
						t.Fatal(err)
					}
				}
				if strings.Contains(strings.Join(argv, " "), "synthetic-secret") || strings.Contains(strings.Join(argv, " "), "https://project.invalid") {
					t.Fatal("provider values entered command arguments")
				}
				cmd := exec.CommandContext(t.Context(), argv[0], argv[1:]...)
				cmd.Env = os.Environ()
				for key, value := range env {
					cmd.Env = append(cmd.Env, key+"="+value)
				}
				// Simulate a login shell overwriting the canonical variable while
				// leaving the private explicit-value bridge intact.
				cmd.Env = append(cmd.Env, "OPENAI_BASE_URL=https://shell.invalid/v1")
				out, err := cmd.Output()
				if err != nil || string(out) != tc.want {
					t.Fatalf("restore=%v endpoint=%q err=%v want=%q", restore, out, err, tc.want)
				}
			}
		})
	}
}
