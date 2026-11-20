//go:build !windows

package primeagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPrimeEndpointRoutingAfterShellStartup(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"shell gateway", nil, "https://shell.example/v1"},
		{"project gateway", map[string]string{"OPENAI_BASE_URL": "https://project.example/v1"}, "https://project.example/v1"},
		{"explicit public endpoint", map[string]string{"OPENAI_BASE_URL": "https://api.openai.com/v1"}, "https://api.openai.com/v1"},
		{"explicit clear", map[string]string{"OPENAI_BASE_URL": ""}, "__unset__"},
	} {
		for _, restore := range []bool{false, true} {
			name := tc.name + "/fresh"
			if restore {
				name = tc.name + "/existing restore"
			}
			t.Run(name, func(t *testing.T) {
				binary := filepath.Join(t.TempDir(), "prime-agent")
				if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"${OPENAI_BASE_URL-__unset__}\"\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				p := &Plugin{resolvedBinary: binary, extensionFlag: map[string]bool{binary: false}}
				data, workspace := t.TempDir(), t.TempDir()
				config := ports.AgentConfig{Model: "openai/gpt-4.1-mini"}
				var argv []string
				var err error
				if restore {
					var ok bool
					argv, ok, err = p.GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: data, Config: config, Env: tc.env, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: primeForkID}}})
					if !ok || !slices.Contains(argv, primeForkID) {
						t.Fatalf("existing conversation was not selected: %v, %v", argv, err)
					}
				} else {
					argv, err = p.GetLaunchCommand(context.Background(), ports.LaunchConfig{DataDir: data, WorkspacePath: workspace, Config: config, Env: tc.env})
				}
				if err != nil {
					t.Fatal(err)
				}
				args := append([]string{"-c", `export OPENAI_BASE_URL=https://shell.example/v1; exec "$@"`, "synthetic-login-shell"}, argv...)
				out, err := exec.Command("/bin/sh", args...).CombinedOutput()
				if err != nil || strings.TrimSpace(string(out)) != tc.want {
					t.Fatalf("endpoint after shell startup = %q, %v; want %q", out, err, tc.want)
				}
			})
		}
	}
}

func TestPrimeForkEndpointEnvironment(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://inherited.example/v1")
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"inherited", nil, "https://inherited.example/v1"},
		{"custom", map[string]string{"OPENAI_BASE_URL": "https://project.example/v1"}, "https://project.example/v1"},
		{"clear", map[string]string{"OPENAI_BASE_URL": ""}, "__unset__"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binary, result := filepath.Join(root, "prime-agent"), filepath.Join(root, "endpoint.txt")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"${OPENAI_BASE_URL-__unset__}\" > \"$1\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := runPrimeNativeFork(context.Background(), binary, root, tc.env, result); err != nil {
				t.Fatal(err)
			}
			out, err := os.ReadFile(result)
			if err != nil || string(out) != tc.want {
				t.Fatalf("fork endpoint = %q, %v; want %q", out, err, tc.want)
			}
		})
	}
}
