package auggie

import (
	"context"
	"slices"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandUsesRulesAndUserPermissions(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/auggie", nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		TaskPromptRoot: "/ao/prompts", SystemPromptFile: "/ao/prompts/system.md", Prompt: "read task",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/auggie", "--rules", "/ao/prompts/system.md"}
	if !slices.Equal(spec.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", spec.Argv, want)
	}
	if spec.InitialMessage != "read task" {
		t.Fatalf("initial message = %q, want interactive task", spec.InitialMessage)
	}
	if r.ReviewProcessReusable() {
		t.Fatal("Auggie reviewer must force a fresh process for each pass")
	}
}
