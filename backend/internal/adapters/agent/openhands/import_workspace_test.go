package openhands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportedOpenHandsWorkspaceUsesResolvedEnvironment(t *testing.T) {
	workspace, other := t.TempDir(), t.TempDir()
	t.Setenv("OPENHANDS_WORK_DIR", other)
	for _, test := range []struct {
		name string
		env  map[string]string
		root string
		want bool
	}{
		{"ordinary session", nil, "", false},
		{"inherited other workdir", nil, "native", true},
		{"matching project workdir", map[string]string{"OPENHANDS_WORK_DIR": workspace}, "native", false},
		{"different project workdir", map[string]string{"OPENHANDS_WORK_DIR": other}, "native", true},
		{"empty override", map[string]string{"OPENHANDS_WORK_DIR": ""}, "native", true},
		{"relative override", map[string]string{"OPENHANDS_WORK_DIR": "."}, "native", true},
		{"missing override", map[string]string{"OPENHANDS_WORK_DIR": filepath.Join(workspace, "missing")}, "native", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, ImportConfigDir: test.root, NativeSessionID: "native", Env: test.env}
			if err := validateImportedOpenHandsWorkspace(t.Context(), cfg); (err != nil) != test.want {
				t.Fatalf("workdir validation = %v, want refusal=%v", err, test.want)
			}
		})
	}
}

func TestImportedOpenHandsWorkspaceWithoutOverride(t *testing.T) {
	old, present := os.LookupEnv("OPENHANDS_WORK_DIR")
	if err := os.Unsetenv("OPENHANDS_WORK_DIR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv("OPENHANDS_WORK_DIR", old)
		} else {
			_ = os.Unsetenv("OPENHANDS_WORK_DIR")
		}
	})
	if err := validateImportedOpenHandsWorkspace(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: t.TempDir(), ImportConfigDir: "native"}); err != nil {
		t.Fatal(err)
	}
}

func TestImportedOpenHandsHookRejectsRedirectionBeforeWrites(t *testing.T) {
	workspace := t.TempDir()
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace, ImportConfigDir: "native", NativeSessionID: "native", Env: map[string]string{"OPENHANDS_WORK_DIR": t.TempDir()}}
	if err := New().GetAgentHooks(t.Context(), cfg); err == nil {
		t.Fatal("imported hook accepted a different project workdir")
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("workspace changed before validation: %v, %v", entries, err)
	}
}
