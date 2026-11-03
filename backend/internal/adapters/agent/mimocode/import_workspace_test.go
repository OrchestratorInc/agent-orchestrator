package mimocode

import (
	"path/filepath"
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
