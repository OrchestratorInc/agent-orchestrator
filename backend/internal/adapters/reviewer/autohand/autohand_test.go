package autohand

import (
	"context"
	"reflect"
	"slices"
	"testing"

	workerautohand "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/autohand"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandKeepsNativeApprovalsEnabled(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/autohand", nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{
		TaskPromptRoot: "/ao/prompts", SystemPromptFile: "/ao/prompts/system.md", WorkspacePath: "/worker", Prompt: "read task",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"--yes", "--unrestricted"} {
		if slices.Contains(spec.Argv, forbidden) {
			t.Fatalf("argv enables %q: %#v", forbidden, spec.Argv)
		}
	}
	want := []string{"/opt/autohand", "--path", "/worker", "--sys-prompt", "/ao/prompts/system.md", "--", "read task"}
	if !slices.Equal(spec.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", spec.Argv, want)
	}
	wantEnv := map[string]string{workerautohand.NoBrowserEnv: "1"}
	if !reflect.DeepEqual(spec.Env, wantEnv) {
		t.Fatalf("env = %#v, want %#v", spec.Env, wantEnv)
	}
}

func TestReviewCommandDisablesAutomaticBrowserWithoutTaskPrompt(t *testing.T) {
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/autohand", nil }}

	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{})
	if err != nil {
		t.Fatal(err)
	}

	wantEnv := map[string]string{workerautohand.NoBrowserEnv: "1"}
	if !reflect.DeepEqual(spec.Env, wantEnv) {
		t.Fatalf("env = %#v, want %#v", spec.Env, wantEnv)
	}
}
