package devin

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandUsesVisibleInteractiveTUI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "devin")
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return binary, nil }}
	inv := ports.ReviewInvocation{
		TaskPromptRoot: "/ao/prompts", SystemPromptFile: "/ao/prompts/system.md", Prompt: "Read task.md.",
	}
	spec, err := r.ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spec.Argv, []string{binary, "--permission-mode", "auto"}) {
		t.Fatalf("argv = %#v", spec.Argv)
	}
	if !strings.Contains(spec.InitialMessage, "/ao/prompts/system.md") || !strings.Contains(spec.InitialMessage, "Read task.md.") {
		t.Fatalf("initial message = %q", spec.InitialMessage)
	}
	for _, forbidden := range []string{"--sandbox", "--print", "--prompt"} {
		if slices.Contains(spec.Argv, forbidden) {
			t.Fatalf("argv contains forbidden flag %q: %#v", forbidden, spec.Argv)
		}
	}
}

func TestReviewPromptReadinessHintsDelayInitialInjection(t *testing.T) {
	hints, err := (&Reviewer{}).ReviewPromptReadinessHints(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hints.InitialDelay < 2*time.Second {
		t.Fatalf("initial delay = %s, want at least 2s", hints.InitialDelay)
	}
}

func TestReviewCommandRejectsUnsafeInputs(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "devin", nil }}
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{}); err == nil {
		t.Fatal("expected relative binary rejection")
	}
	binary := filepath.Join(t.TempDir(), "devin")
	r.resolveBinary = func(context.Context) (string, error) { return binary, nil }
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{TaskPromptRoot: "/ao"}); err == nil {
		t.Fatal("expected missing system prompt rejection")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ReviewCommand(ctx, ports.ReviewInvocation{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestReviewMessageAndCancel(t *testing.T) {
	r := &Reviewer{}
	msg, err := r.ReviewMessage(context.Background(), ports.ReviewInvocation{Prompt: "next"})
	if err != nil || msg != "next" {
		t.Fatalf("message = %q, %v", msg, err)
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInterrupt || cancel.Interrupts != 1 {
		t.Fatalf("cancel = %#v, %v", cancel, err)
	}
}
