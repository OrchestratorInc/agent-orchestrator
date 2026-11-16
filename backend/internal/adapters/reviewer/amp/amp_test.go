package amp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandWritesDenyByDefaultSettings(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/amp", nil }}
	root := t.TempDir()
	inv := ports.ReviewInvocation{TaskPromptRoot: root, SystemPromptFile: "/ao/system.md", Prompt: "Read task."}
	spec, err := r.ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(root, "amp-settings.json")
	if !slices.Equal(spec.Argv, []string{"/opt/amp", "--settings-file", settingsPath}) {
		t.Fatalf("argv = %#v", spec.Argv)
	}
	if spec.InitialMessage != "First read and follow the reviewer role in `/ao/system.md`. Then Read task." {
		t.Fatalf("initial message = %q", spec.InitialMessage)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["amp.dangerouslyAllowAll"] != false || settings["amp.remoteThreadCreation.enabled"] != false {
		t.Fatalf("unsafe settings = %#v", settings)
	}
	permissions, ok := settings["amp.permissions"].([]any)
	if !ok || len(permissions) != 7 {
		t.Fatalf("permissions = %#v", settings["amp.permissions"])
	}
	last := permissions[len(permissions)-1].(map[string]any)
	if last["tool"] != "*" || last["action"] != "reject" {
		t.Fatalf("catch-all permission = %#v", last)
	}
}

func TestReviewCommandPreflightShapeAndCancellation(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/amp", nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{})
	if err != nil || !slices.Equal(spec.Argv, []string{"/opt/amp"}) || spec.InitialMessage != "" {
		t.Fatalf("spec = %#v, %v", spec, err)
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInterrupt || cancel.Interrupts != 2 {
		t.Fatalf("cancel = %#v, %v", cancel, err)
	}
}

func TestReviewCommandForwardsSavedAmpMode(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/amp", nil }}
	root := t.TempDir()
	// A configured model reaches the reviewer as the model field, exactly as
	// it does for Amp workers.
	saved := domain.AgentConfig{Model: "high"}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{TaskPromptRoot: root, Prompt: "Read task.", Config: saved})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/opt/amp", "--settings-file", filepath.Join(root, "amp-settings.json"), "--mode", "high"}; !slices.Equal(spec.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", spec.Argv, want)
	}
	restore, _, err := r.ReviewRestoreCommand(context.Background(), ports.ReviewInvocation{Config: domain.AgentConfig{Mode: "low"}})
	if err != nil || !slices.Equal(restore.Argv, []string{"/opt/amp", "--mode", "low"}) {
		t.Fatalf("restore argv = %#v, %v", restore.Argv, err)
	}
	unknown, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{Config: domain.AgentConfig{Model: "gpt-5"}})
	if err != nil || !slices.Equal(unknown.Argv, []string{"/opt/amp"}) {
		t.Fatalf("unknown mode argv = %#v, %v", unknown.Argv, err)
	}
}
