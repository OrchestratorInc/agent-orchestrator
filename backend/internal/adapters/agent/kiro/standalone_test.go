package kiro

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStandaloneWorkspaceKeepsConfiguredModelWithoutDaemonHooks(t *testing.T) {
	workspace := t.TempDir()
	if err := New().PrepareStandaloneWorkspace(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace, Config: ports.AgentConfig{Model: "configured-model"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(kiroAgentPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "configured-model") || strings.Contains(string(raw), "ao hooks") {
		t.Fatalf("standalone custom agent = %s", raw)
	}
}
