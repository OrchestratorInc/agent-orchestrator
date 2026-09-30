package workerexec

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestInteractiveLaunchConsumesProjectAgentSettings(t *testing.T) {
	for _, test := range []struct {
		harness, credentialType, secret string
		config                          domain.ProjectAgentConfig
		flags                           []string
	}{
		{"codex", "auth_json", `{"tokens":{"access_token":"test"}}`, domain.ProjectAgentConfig{Model: "review-model", Effort: "max", Permissions: "accept-edits"}, []string{"--model", "review-model", "model_reasoning_effort=\"max\"", "--ask-for-approval", "on-request"}},
		{"codex", "auth_json", `{"tokens":{"access_token":"test"}}`, domain.ProjectAgentConfig{Permissions: "default"}, nil},
		{"claude-code", "api_key", "test", domain.ProjectAgentConfig{Model: "review-model", Effort: "high", Permissions: "auto"}, []string{"--model", "review-model", "--effort", "high", "--permission-mode", "auto"}},
		{"cursor", "api_key", "test", domain.ProjectAgentConfig{Model: "review-model", Mode: "plan", Permissions: "bypass-permissions"}, []string{"--model", "review-model", "--mode", "plan", "--yolo"}},
		{"opencode", "openai_api_key", "test", domain.ProjectAgentConfig{Model: "openai/review-model", Permissions: "auto"}, []string{"--model", "openai/review-model", "--auto"}},
	} {
		t.Run(test.harness, func(t *testing.T) {
			root := t.TempDir()
			workerHome := t.TempDir()
			t.Setenv("CODEX_HOME", workerHome)
			t.Setenv("CLAUDE_CONFIG_DIR", workerHome)
			command, err := (HarnessBuilder{DataDir: root, ConfigRoot: filepath.Join(root, "reviewer")}).BuildInteractive(worker.LaunchContext{
				SessionID: "review-run", Kind: "reviewer", Harness: test.harness, Mode: "trusted", Model: test.config.Model,
				AgentConfig: test.config, AgentSessionID: "worker-thread",
			}, worker.CredentialResponse{Provider: test.harness, CredentialType: test.credentialType, Secret: test.secret}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, flag := range test.flags {
				if !slices.Contains(command.Args, flag) {
					t.Fatalf("missing %q from %#v", flag, command.Args)
				}
			}
			if test.config.Permissions == "default" && slices.Contains(command.Args, "--dangerously-bypass-approvals-and-sandbox") {
				t.Fatalf("agent defaults unexpectedly bypass permissions: %#v", command.Args)
			}
			if slices.Contains(command.Args, "--resume") || slices.Contains(command.Args, "resume") || slices.Contains(command.Args, "worker-thread") {
				t.Fatalf("review resumed worker context: %#v", command.Args)
			}
			for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
				if home := command.Env[key]; home != "" && home == workerHome {
					t.Fatalf("review used worker %s", key)
				}
			}
			if command.Cleanup != nil {
				command.Cleanup()
			}
			if entries, err := os.ReadDir(workerHome); err != nil || len(entries) != 0 {
				t.Fatalf("review modified worker home: entries=%v err=%v", entries, err)
			}
		})
	}
}
