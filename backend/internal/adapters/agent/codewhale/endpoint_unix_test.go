//go:build !windows

package codewhale

import (
	"context"
	"encoding/json"
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
					p := &Plugin{resolvedBinary: binary}
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
						cfg := ports.RestoreConfig{DataDir: data, Config: config, Env: env, Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: restoreTestID}}}
						if lifecycle == "imported" {
							path := writeRestoreSnapshot(t, root, workspace, restoreTestID, "")
							body, err := os.ReadFile(path)
							if err != nil {
								t.Fatal(err)
							}
							var document map[string]any
							if err := json.Unmarshal(body, &document); err != nil {
								t.Fatal(err)
							}
							document["metadata"].(map[string]any)["model_provider_id"] = provider.name
							body, err = json.Marshal(document)
							if err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(path, body, 0o600); err != nil {
								t.Fatal(err)
							}
							cfg.Session.Metadata[ports.MetadataKeyImportConfigDir] = root
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
