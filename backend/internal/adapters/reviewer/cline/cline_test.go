package cline

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandUsesSystemPromptWithoutAutoApproval(t *testing.T) {
	root := t.TempDir()
	system := filepath.Join(root, "system.md")
	if err := os.WriteFile(system, []byte("review only"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/cline", nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{TaskPromptRoot: root, SystemPromptFile: system, Prompt: "read task"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spec.Argv, []string{"/opt/cline", "-s", "review only"}) || spec.InitialMessage != "read task" {
		t.Fatalf("spec = %+v", spec)
	}
	for _, forbidden := range []string{"--auto-approve", "--yolo"} {
		if slices.Contains(spec.Argv, forbidden) {
			t.Fatalf("argv enables %q: %#v", forbidden, spec.Argv)
		}
	}
}
