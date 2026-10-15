package tau

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRestoreRejectsUnusableNativeHistory(t *testing.T) {
	const id = "8bc1a3fbba2142bcb406fe4a4b6fe21b"
	for _, kind := range []string{"missing", "empty", "malformed", "wrong header", "wrong cwd", "directory", "ambiguous", "valid", "legacy index"} {
		t.Run(kind, func(t *testing.T) {
			workspace := t.TempDir()
			plugin := fixtureRestorePlugin(t, id, workspace)
			home := os.Getenv("TAU_HOME")
			dir := filepath.Join(home, "sessions", "project")
			history := filepath.Join(dir, id+".jsonl")
			switch kind {
			case "missing":
				if err := os.Remove(history); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(history, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(history, []byte("{not-json"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong header":
				if err := os.WriteFile(history, []byte(`{"type":"message"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong cwd":
				header, _ := json.Marshal(map[string]string{"type": "session_info", "cwd": t.TempDir()})
				if err := os.WriteFile(history, header, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(history); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(history, 0700); err != nil {
					t.Fatal(err)
				}
			case "ambiguous":
				row, _ := json.Marshal(map[string]string{"id": id, "path": filepath.Join(dir, "another.jsonl")})
				if err := os.WriteFile(filepath.Join(home, "sessions", "index.jsonl"), row, 0600); err != nil {
					t.Fatal(err)
				}
			case "legacy index":
				if err := os.Rename(filepath.Join(dir, "index.jsonl"), filepath.Join(home, "sessions", "index.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			before, readErr := os.ReadFile(history)
			_, restorable, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: id}}, Permissions: ports.PermissionModeBypassPermissions})
			valid := kind == "valid" || kind == "legacy index"
			if valid {
				if err != nil || !restorable {
					t.Fatalf("valid history rejected: %v, %v", restorable, err)
				}
			} else if err == nil || restorable || !strings.Contains(err.Error(), "history") {
				t.Fatalf("unusable history admitted: %v, %v", restorable, err)
			}
			after, afterErr := os.ReadFile(history)
			if readErr == nil && (afterErr != nil || string(after) != string(before)) {
				t.Fatal("restore validation changed native history")
			}
			if kind == "missing" {
				if _, err := os.Stat(history); !os.IsNotExist(err) {
					t.Fatalf("validation created missing native history: %v", err)
				}
			}
		})
	}
}
