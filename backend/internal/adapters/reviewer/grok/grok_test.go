package grok

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestReviewCommandUsesRulesWithoutUnattendedPermissionFlag(t *testing.T) {
	root := t.TempDir()
	system := filepath.Join(root, "system.md")
	if err := os.WriteFile(system, []byte("review only"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "/opt/grok", nil }}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{TaskPromptRoot: root, SystemPromptFile: system, Prompt: "read task"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(spec.Argv, "--rules") || !slices.Contains(spec.Argv, "review only") {
		t.Fatalf("argv = %#v", spec.Argv)
	}
	for _, forbidden := range []string{"--permission-mode", "auto", "bypassPermissions"} {
		if slices.Contains(spec.Argv, forbidden) {
			t.Fatalf("argv contains unattended permission token %q: %#v", forbidden, spec.Argv)
		}
	}
	if !strings.Contains(HostTrustWarning, "user-approved") {
		t.Fatalf("warning = %q", HostTrustWarning)
	}
}
