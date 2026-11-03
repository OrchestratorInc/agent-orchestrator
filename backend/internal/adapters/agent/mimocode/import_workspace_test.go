package mimocode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMiMoImportedWorkspaceUsesEffectiveLaunchEnvironment(t *testing.T) {
	t.Setenv("MIMOCODE_WORKSPACE_ID", "daemon-workspace")
	t.Setenv("MIMOCODE_DB", "")
	root := t.TempDir()
	for _, test := range []struct {
		name string
		cfg  ports.WorkspaceHookConfig
		want bool
	}{
		{"ordinary workspace", ports.WorkspaceHookConfig{}, false},
		{"inherited workspace", ports.WorkspaceHookConfig{ImportConfigDir: root, NativeSessionID: "ses_new"}, true},
		{"explicit local", ports.WorkspaceHookConfig{ImportConfigDir: root, NativeSessionID: "ses_new", Env: map[string]string{"MIMOCODE_WORKSPACE_ID": ""}}, false},
		{"project workspace", ports.WorkspaceHookConfig{ImportConfigDir: root, NativeSessionID: "ses_new", Env: map[string]string{"MIMOCODE_WORKSPACE_ID": "project-workspace"}}, true},
		{"project database", ports.WorkspaceHookConfig{ImportConfigDir: root, NativeSessionID: "ses_new", Env: map[string]string{"MIMOCODE_WORKSPACE_ID": "", "MIMOCODE_DB": "other.db"}}, true},
		{"pinned database", ports.WorkspaceHookConfig{ImportConfigDir: root, NativeSessionID: "ses_new", Env: map[string]string{"MIMOCODE_WORKSPACE_ID": "", "MIMOCODE_DB": filepath.Join(root, "mimocode.db")}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMiMoImportedWorkspace(test.cfg); (err != nil) != test.want {
				t.Fatalf("workspace guard = %v, want refusal=%v", err, test.want)
			}
		})
	}
}

func TestMiMoPreparedDatabaseBinding(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mimocode.db")
	got, err := New().NativeSessionConfigDir(t.Context(), map[string]string{"MIMOCODE_DB": path})
	if err != nil || got != root {
		t.Fatalf("prepared native root = %q, %v", got, err)
	}
	if _, err := New().NativeSessionConfigDir(t.Context(), map[string]string{"MIMOCODE_DB": filepath.Join(root, "unrelated.db")}); err == nil {
		t.Fatal("unsupported database filename accepted")
	}
}

func TestMiMoImportedHookRejectsProjectWorkspaceBeforeWrites(t *testing.T) {
	workspace := t.TempDir()
	err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{WorkspacePath: workspace, ImportConfigDir: t.TempDir(), NativeSessionID: "ses_new", Env: map[string]string{"MIMOCODE_WORKSPACE_ID": "project-workspace", "MIMOCODE_DB": ""}})
	if err == nil || !strings.Contains(err.Error(), "implicit local workspace") {
		t.Fatalf("project workspace override = %v", err)
	}
	if _, err := os.Stat(mimoHookPath(workspace)); !os.IsNotExist(err) {
		t.Fatal("workspace hooks were written before import validation")
	}
}
