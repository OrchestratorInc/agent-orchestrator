package kilocode

import (
	"os"
	"slices"
	"strings"
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

func TestKiloImportedHookRejectsProjectDatabaseBeforeWorkspaceWrites(t *testing.T) {
	workspace := t.TempDir()
	err := New().GetAgentHooks(t.Context(), ports.WorkspaceHookConfig{WorkspacePath: workspace, ImportConfigDir: "/native/kilo", NativeSessionID: "ses_new", Env: map[string]string{"KILO_WORKSPACE_ID": "", "KILO_DB": "/other/history.db"}})
	if err == nil || !strings.Contains(err.Error(), "database filename") {
		t.Fatalf("project database override = %v", err)
	}
	if _, err := os.Stat(kilocodePluginPath(workspace)); !os.IsNotExist(err) {
		t.Fatal("workspace hooks were written before import validation")
	}
}

func TestKiloImportedRestorePinsStableDatabase(t *testing.T) {
	t.Setenv("KILO_WORKSPACE_ID", "")
	t.Setenv("KILO_DB", "")
	p := &Plugin{resolvedBinary: "kilo"}
	cmd, ok, err := p.GetRestoreCommand(t.Context(), ports.RestoreConfig{Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ses_imported", ports.MetadataKeyImportConfigDir: "/native/kilo"}}})
	if err != nil || !ok || !slices.Contains(cmd, "KILO_DB=kilo.db") {
		t.Fatalf("imported database binding = %v, %v, %v", cmd, ok, err)
	}
}

func TestKiloImportedRestoreRejectsEffectiveWorkspaceOverride(t *testing.T) {
	t.Setenv("KILO_WORKSPACE_ID", "inherited-workspace")
	t.Setenv("KILO_DB", "")
	for _, test := range []struct {
		name   string
		env    map[string]string
		refuse bool
	}{
		{"inherited workspace", nil, true},
		{"explicit local", map[string]string{"KILO_WORKSPACE_ID": ""}, false},
		{"project workspace", map[string]string{"KILO_WORKSPACE_ID": "project-workspace"}, true},
		{"project database", map[string]string{"KILO_WORKSPACE_ID": "", "KILO_DB": "other.db"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &Plugin{resolvedBinary: "kilo"}
			_, ok, err := p.GetRestoreCommand(t.Context(), ports.RestoreConfig{Env: test.env, Session: ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "ses_imported", ports.MetadataKeyImportConfigDir: "/native/kilo"}}})
			if (err != nil) != test.refuse || ok == test.refuse {
				t.Fatalf("restore guard ok=%v err=%v want refusal=%v", ok, err, test.refuse)
			}
		})
	}
}
