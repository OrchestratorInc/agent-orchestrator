package minimaxcode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeHistoryWindowsRestore(t *testing.T) {
	for _, directory := range []string{"plain", "profile space #100%"} {
		t.Run(directory, func(t *testing.T) {
			original, workspace, id := historyFixture(t)
			dataDir := filepath.Join(t.TempDir(), directory)
			profile, err := profilePath(dataDir, "restore")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(original, profile); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profile, "ao-standing-transport"), []byte(standingTransportMarker), 0o600); err != nil {
				t.Fatal(err)
			}
			plugin := New()
			plugin.resolvedBinary = "mcode"
			cfg := ports.RestoreConfig{DataDir: dataDir, Session: ports.SessionRef{
				ID: "restore", WorkspacePath: workspace,
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id},
			}}
			command, restored, err := plugin.GetRestoreCommand(context.Background(), cfg)
			if err != nil || !restored || !reflect.DeepEqual(command, []string{"mcode", "--session", id}) {
				t.Fatalf("valid Windows history did not restore exactly: command=%q restored=%v err=%v", command, restored, err)
			}
			cfg.Session.WorkspacePath = t.TempDir()
			if command, restored, err := plugin.GetRestoreCommand(context.Background(), cfg); err == nil || restored || command != nil {
				t.Fatalf("Windows restore bypassed workspace identity: command=%q restored=%v err=%v", command, restored, err)
			}
		})
	}
}
