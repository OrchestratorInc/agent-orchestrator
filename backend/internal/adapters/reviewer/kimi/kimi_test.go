package kimi

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandUsesPlanModeAndEmptySkillsDirectory(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kimi")
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return binary, nil }}
	root := t.TempDir()
	inv := ports.ReviewInvocation{
		TaskPromptRoot: root, SystemPromptFile: filepath.Join(root, "system.md"), Prompt: "Read task.",
	}
	spec, err := r.ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(root, skillsDirectoryName)
	want := []string{binary, "--plan", "--auto", "--skills-dir", skillsDir}
	if !slices.Equal(spec.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", spec.Argv, want)
	}
	if !strings.Contains(spec.InitialMessage, filepath.ToSlash(inv.SystemPromptFile)) || !strings.Contains(spec.InitialMessage, inv.Prompt) {
		t.Fatalf("initial message = %q", spec.InitialMessage)
	}
	entries, err := os.ReadDir(skillsDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("skills directory entries = %#v, %v", entries, err)
	}
	for _, forbidden := range []string{"--print", "--quiet", "--command", "--yolo"} {
		if slices.Contains(spec.Argv, forbidden) {
			t.Fatalf("argv contains forbidden flag %q: %#v", forbidden, spec.Argv)
		}
	}
}

func TestReviewCommandPreflightValidationAndCancel(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "kimi")
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return binary, nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{})
	if err != nil || !slices.Equal(spec.Argv, []string{binary, "--plan", "--auto"}) {
		t.Fatalf("spec = %#v, %v", spec, err)
	}
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{TaskPromptRoot: t.TempDir()}); err == nil {
		t.Fatal("expected missing system prompt rejection")
	}
	r.resolveBinary = func(context.Context) (string, error) { return "kimi", nil }
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{}); err == nil {
		t.Fatal("expected relative binary rejection")
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInterrupt || cancel.Interrupts != 2 {
		t.Fatalf("cancel = %#v, %v", cancel, err)
	}
}
