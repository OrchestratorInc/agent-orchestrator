package aider

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandUsesAskModeAndReadOnlyPromptContext(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/aider", nil }}
	inv := ports.ReviewInvocation{SystemPromptFile: "/ao/system.md", TaskPromptFile: "/ao/task.md"}
	spec, err := r.ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/opt/aider", "--chat-mode", "ask", "--dry-run", "--no-auto-commits",
		"--no-dirty-commits", "--no-gitignore", "--no-check-update", "--no-stream",
		"--no-pretty", "--read", "/ao/system.md", "--read", "/ao/task.md",
	}
	if !slices.Equal(spec.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", spec.Argv, want)
	}
	if spec.InitialMessage == "" || r.ReviewProcessReusable() {
		t.Fatalf("message = %q, reusable = %v", spec.InitialMessage, r.ReviewProcessReusable())
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInterrupt || cancel.Interrupts != 2 {
		t.Fatalf("cancel = %#v, %v", cancel, err)
	}
}

func TestReviewCommandPropagatesBinaryResolutionFailure(t *testing.T) {
	want := errors.New("missing aider")
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "", want }}
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{}); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
