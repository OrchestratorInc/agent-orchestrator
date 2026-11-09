package droid

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandUsesAutonomousInteractiveSettings(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "droid")
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return binary, nil }}
	if r.ReviewProcessReusable() {
		t.Fatal("Droid reviewer tasks must launch fresh with their initial prompt")
	}
	root := t.TempDir()
	inv := ports.ReviewInvocation{
		WorkspacePath:  "/workspace/pr",
		TaskPromptRoot: root, SystemPromptFile: filepath.Join(root, "system.md"), Prompt: "Read task.",
	}
	spec, err := r.ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(root, settingsFilename)
	want := []string{binary, "--settings", settingsPath, "--append-system-prompt-file", inv.SystemPromptFile, "--cwd", inv.WorkspacePath, inv.Prompt}
	if !slices.Equal(spec.Argv, want) || spec.InitialMessage != "" {
		t.Fatalf("spec = %#v, want argv %#v", spec, want)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var rawSettings map[string]any
	if err := json.Unmarshal(raw, &rawSettings); err != nil {
		t.Fatal(err)
	}
	if session, ok := rawSettings["sessionDefaultSettings"].(map[string]any); ok {
		if _, ok := session["interactionMode"]; ok {
			t.Fatalf("settings should not force spec interaction mode: %s", raw)
		}
	} else {
		t.Fatalf("settings missing sessionDefaultSettings: %s", raw)
	}
	var parsed struct {
		Session struct {
			AutonomyLevel string `json:"autonomyLevel"`
		} `json:"sessionDefaultSettings"`
		CloudSessionSync bool `json:"cloudSessionSync"`
		IDEAutoConnect   bool `json:"ideAutoConnect"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Session.AutonomyLevel != "high" || parsed.CloudSessionSync || parsed.IDEAutoConnect {
		t.Fatalf("settings = %#v", parsed)
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

func TestReviewCommandPreflightValidationAndCancel(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "droid")
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return binary, nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{})
	if err != nil || !slices.Equal(spec.Argv, []string{binary}) {
		t.Fatalf("spec = %#v, %v", spec, err)
	}
	r.resolveBinary = func(context.Context) (string, error) { return "droid", nil }
	if _, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{}); err == nil {
		t.Fatal("expected relative binary rejection")
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInterrupt || cancel.Interrupts != 1 {
		t.Fatalf("cancel = %#v, %v", cancel, err)
	}
}
