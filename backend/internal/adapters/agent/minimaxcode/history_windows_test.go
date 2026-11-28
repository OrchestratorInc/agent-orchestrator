package minimaxcode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
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

// A drive-relative path is not absolute on Windows, yet "C:..\..\outside"
// resolves outside v2\sessions. The target holds a complete valid history, so
// only the location check can refuse it.
func TestNativeHistoryRejectsWindowsVolumeRelativePath(t *testing.T) {
	p, w, id := historyFixture(t)
	relative := `C:..\..\outside`
	target := filepath.Clean(filepath.Join(p, "v2", "sessions", relative))
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "messages.jsonl"} {
		body, err := os.ReadFile(filepath.Join(p, "v2", "sessions", "exact", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(p, "v2", "sqlite", "runtime-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE local_runtime_sessions SET history_relative_dir=? WHERE session_id=?`, relative, id); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeHistory(context.Background(), p, id, w); err == nil {
		t.Fatalf("accepted volume-relative history location %q", relative)
	}
}
