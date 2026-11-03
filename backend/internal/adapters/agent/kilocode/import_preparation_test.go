package kilocode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestKiloNativeImportPreparation(t *testing.T) {
	for _, mode := range []string{"native import", "import failure", "missing row", "wrong directory", "missing history"} {
		t.Run(mode, func(t *testing.T) {
			dataRoot := t.TempDir()
			t.Setenv("AO_DATA_DIR", dataRoot)
			t.Setenv("KILO_WORKSPACE_ID", "")
			t.Setenv("OPENCODE_WORKSPACE_ID", "")
			t.Setenv("KILO_DB", "")
			root := filepath.Join(t.TempDir(), "kilo")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(root, "kilo.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			for _, schema := range []string{
				`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, workspace_id TEXT)`,
				`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT)`,
				`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, message_id TEXT)`,
				`CREATE TABLE session_message (id TEXT PRIMARY KEY)`,
			} {
				if _, err := db.Exec(schema); err != nil {
					t.Fatal(err)
				}
			}
			export, sourceID := kiloSyntheticExport()
			if _, err := db.Exec(`INSERT INTO session VALUES (?, '/source', 'wrk_source')`, sourceID); err != nil {
				t.Fatal(err)
			}
			cwd, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			run := func(_ context.Context, directory, config string, args ...string) ([]byte, error) {
				calls++
				if directory != cwd || config != root || len(args) != 2 {
					t.Fatalf("native command location/config/args = %q, %q, %v", directory, config, args)
				}
				if args[0] == "export" && args[1] == sourceID {
					return export, nil
				}
				if args[0] != "import" {
					t.Fatalf("unexpected native command: %v", args)
				}
				info, err := os.Stat(args[1])
				if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
					t.Fatalf("private native export = %v, %v", info, err)
				}
				if mode == "import failure" {
					return nil, errors.New("synthetic provider refusal")
				}
				if mode == "missing row" {
					return []byte("claimed success"), nil
				}
				data, err := os.ReadFile(args[1])
				if err != nil {
					t.Fatal(err)
				}
				var transformed struct {
					Info struct {
						ID        string  `json:"id"`
						Directory string  `json:"directory"`
						Workspace *string `json:"workspaceID"`
					} `json:"info"`
					Messages []struct {
						Info struct {
							ID string `json:"id"`
						} `json:"info"`
						Parts []struct {
							ID string `json:"id"`
						} `json:"parts"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(data, &transformed); err != nil {
					t.Fatal(err)
				}
				if transformed.Info.ID == sourceID || transformed.Info.Directory != cwd || transformed.Info.Workspace != nil {
					t.Fatalf("unsafe import identity: %#v", transformed.Info)
				}
				if mode == "wrong directory" {
					transformed.Info.Directory = "/source"
				}
				_, err = db.Exec(`INSERT INTO session VALUES (?, ?, NULL)`, transformed.Info.ID, transformed.Info.Directory)
				if err != nil || mode == "missing history" {
					return nil, err
				}
				for _, message := range transformed.Messages {
					if _, err := db.Exec(`INSERT INTO message VALUES (?, ?)`, message.Info.ID, transformed.Info.ID); err != nil {
						return nil, err
					}
					for _, part := range message.Parts {
						if _, err := db.Exec(`INSERT INTO part VALUES (?, ?, ?)`, part.ID, transformed.Info.ID, message.Info.ID); err != nil {
							return nil, err
						}
					}
				}
				return nil, nil
			}
			ref, err := New().prepareKiloImport(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: sourceID, ConfigDir: root, LaunchCWD: cwd}, run)
			if mode == "native import" {
				if err != nil || ref.NativeSessionID == "" || ref.NativeSessionID == sourceID || ref.ConfigDir != root {
					t.Fatalf("native import = %#v, %v", ref, err)
				}
			} else if err == nil || ref.NativeSessionID != "" {
				t.Fatalf("failed import published identity: %#v, %v", ref, err)
			}
			if calls != 2 {
				t.Fatalf("native command calls = %d", calls)
			}
			var sourceDirectory, sourceWorkspace string
			if err := db.QueryRow(`SELECT directory, workspace_id FROM session WHERE id = ?`, sourceID).Scan(&sourceDirectory, &sourceWorkspace); err != nil || sourceDirectory != "/source" || sourceWorkspace != "wrk_source" {
				t.Fatalf("original row changed: %q, %q, %v", sourceDirectory, sourceWorkspace, err)
			}
			entries, err := os.ReadDir(filepath.Join(dataRoot, "tmp", "native-import"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("private exports retained: %v, %v", entries, err)
			}
		})
	}
}

func TestKiloNativeOutputLimitCancelsCopy(t *testing.T) {
	cancelled := false
	output := &kiloImportOutput{remaining: 3, cancel: func() { cancelled = true }}
	// Hide WriterTo so io.Copy cannot bypass the writer's bound through a
	// promoted bytes.Buffer.ReadFrom implementation.
	input := struct{ io.Reader }{strings.NewReader("oversized native output")}
	if _, err := io.Copy(output, input); err == nil || !cancelled {
		t.Fatalf("output limit did not cancel copy: %v, cancelled=%v", err, cancelled)
	}
	if output.buffer.Len() != 0 {
		t.Fatal("over-limit output was retained")
	}
}
