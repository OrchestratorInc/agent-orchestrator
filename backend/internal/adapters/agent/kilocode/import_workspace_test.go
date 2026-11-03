package kilocode

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestKiloImportedWorkspaceUsesEffectiveLaunchEnvironment(t *testing.T) {
	t.Setenv("KILO_WORKSPACE_ID", "daemon-workspace")
	t.Setenv("KILO_DB", "")
	for _, test := range []struct {
		name string
		cfg  ports.WorkspaceHookConfig
		want bool
	}{
		{"ordinary workspace", ports.WorkspaceHookConfig{}, false},
		{"inherited workspace", ports.WorkspaceHookConfig{ImportConfigDir: "/native/kilo", NativeSessionID: "ses_new"}, true},
		{"explicit local", ports.WorkspaceHookConfig{ImportConfigDir: "/native/kilo", NativeSessionID: "ses_new", Env: map[string]string{"KILO_WORKSPACE_ID": ""}}, false},
		{"project workspace", ports.WorkspaceHookConfig{ImportConfigDir: "/native/kilo", NativeSessionID: "ses_new", Env: map[string]string{"KILO_WORKSPACE_ID": "project-workspace"}}, true},
		{"project database", ports.WorkspaceHookConfig{ImportConfigDir: "/native/kilo", NativeSessionID: "ses_new", Env: map[string]string{"KILO_WORKSPACE_ID": "", "KILO_DB": "other.db"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateKiloImportedWorkspace(test.cfg); (err != nil) != test.want {
				t.Fatalf("workspace guard = %v, want refusal=%v", err, test.want)
			}
		})
	}
}
