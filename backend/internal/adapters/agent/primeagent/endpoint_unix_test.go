//go:build !windows

package primeagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProviderOverridesSurviveShellStartup(t *testing.T) {
	for _, provider := range []struct{ name, endpoint, key string }{
		{"openai", "OPENAI_BASE_URL", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"},
		{"deepseek", "DEEPSEEK_BASE_URL", "DEEPSEEK_API_KEY"},
		{"zai", "ZAI_BASE_URL", "ZAI_API_KEY"},
	} {
		for _, mode := range []string{"shell", "project", "clear"} {
			for _, lifecycle := range []string{"fresh", "existing", "imported"} {
				t.Run(provider.name+"/"+mode+"/"+lifecycle, func(t *testing.T) {
					data, root, workspace := t.TempDir(), t.TempDir(), t.TempDir()
					binary := filepath.Join(t.TempDir(), "synthetic-agent")
					script := "#!/bin/sh\nprintf '%s|%s' \"${" + provider.endpoint + "-__unset__}\" \"${" + provider.key + "-__unset__}\"\n"
					if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
						t.Fatal(err)
					}
					p := &Plugin{resolvedBinary: binary, extensionFlag: map[string]bool{binary: false}, daemonSocketFlag: map[string]bool{binary: true}}
					env := map[string]string{}
					want := "https://shell.example/v1|shell-credential"
					switch mode {
					case "project":
						env[provider.endpoint], env[provider.key] = "https://project.example/v1", "synthetic-project-credential"
						want = env[provider.endpoint] + "|" + env[provider.key]
					case "clear":
						env[provider.endpoint], env[provider.key] = "", ""
						want = "__unset__|__unset__"
					}
					p.AugmentRuntimeEnv(env, data)
					config := ports.AgentConfig{Model: provider.name + "/fixture-model"}
					var argv []string
					var err error
					if lifecycle == "fresh" {
						argv, err = p.GetLaunchCommand(context.Background(), ports.LaunchConfig{DataDir: data, WorkspacePath: workspace, Config: config, Env: env})
					} else {
						cfg := ports.RestoreConfig{DataDir: data, Config: config, Env: env, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: primeForkID}}}
						if lifecycle == "imported" {
							source := writePrimeNativeFixture(t, root, primeForkID, workspace, "", fmt.Sprintf(`{"type":"model_change","id":"model","parentId":null,"provider":%q,"modelId":"fixture-model"}`, provider.name), primeForkFixtureRows()[0])
							cfg = primeTestRestoreConfig(t, root, workspace, primeForkID)
							cfg.Env, cfg.DataDir = env, data
							if source == "" {
								t.Fatal("missing fixture")
							}
						}
						var ok bool
						argv, ok, err = p.GetRestoreCommand(context.Background(), cfg)
						if !ok || !slices.Contains(argv, "--resume") {
							t.Fatalf("missing native restore: %v, %v", argv, err)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					// Values, including credentials or credential-bearing URLs, must never
					// appear in argv. A startup file may overwrite the original env names.
					joined := strings.Join(argv, " ")
					for _, value := range []string{"https://project.example/v1", "synthetic-project-credential"} {
						if strings.Contains(joined, value) {
							t.Fatal("provider value leaked into argv")
						}
					}
					login := "export " + provider.endpoint + "=https://shell.example/v1; export " + provider.key + "=shell-credential; exec \"$@\""
					args := append([]string{"-c", login, "synthetic-login-shell"}, argv...)
					cmd := exec.Command("/bin/sh", args...)
					cmd.Env = processenv.Merge(env)
					out, err := cmd.CombinedOutput()
					if err != nil || string(out) != want {
						t.Fatalf("provider environment = %q, %v; want %q", out, err, want)
					}
				})
			}
		}
	}
}

func TestPrimeForkProviderEnvironment(t *testing.T) {
	for _, key := range []string{"OPENAI_BASE_URL", "OPENAI_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "DEEPSEEK_BASE_URL", "DEEPSEEK_API_KEY", "ZAI_BASE_URL", "ZAI_API_KEY"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "synthetic-inherited-value")
			for _, mode := range []string{"inherited", "override", "clear"} {
				t.Run(mode, func(t *testing.T) {
					env := map[string]string{}
					want := "synthetic-inherited-value"
					if mode == "override" {
						env[key] = "synthetic-project-value"
						want = env[key]
					}
					if mode == "clear" {
						env[key] = ""
						want = "__unset__"
					}
					root := t.TempDir()
					binary, result := filepath.Join(root, "synthetic-prime"), filepath.Join(root, "result")
					script := "#!/bin/sh\nprintf '%s' \"${" + key + "-__unset__}\" > \"$1\"\n"
					if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
						t.Fatal(err)
					}
					if _, err := runPrimeNativeFork(context.Background(), binary, root, env, result); err != nil {
						t.Fatal(err)
					}
					out, err := os.ReadFile(result)
					if err != nil || string(out) != want {
						t.Fatalf("fork environment = %q, %v; want %q", out, err, want)
					}
				})
			}
		})
	}
}
