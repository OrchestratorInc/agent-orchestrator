package primeagent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestStandaloneWorkspaceInstallsRequiredExtensionWithoutDaemonCalls(t *testing.T) {
	dir := t.TempDir()
	if err := New().PrepareStandaloneWorkspace(context.Background(), ports.WorkspaceHookConfig{DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	path, err := extensionPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "export default function") || strings.Contains(string(raw), "ao hooks") || strings.Contains(string(raw), "spawnSync") {
		t.Fatalf("standalone extension = %s", raw)
	}
}
