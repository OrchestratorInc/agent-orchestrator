package crush

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandKeepsNativeApprovalsEnabled(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/crush", nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		TaskPromptRoot: "/ao/prompts", SystemPromptFile: "/ao/prompts/system.md", WorkspacePath: "/worker", Prompt: "read task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(spec.Argv, "--yolo") {
		t.Fatalf("argv enables unattended mode: %#v", spec.Argv)
	}
	if spec.InitialMessage == "" || !strings.Contains(spec.InitialMessage, "system.md") {
		t.Fatalf("initial message = %q", spec.InitialMessage)
	}
}
