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
	for _, mode := range []string{"native import", "tampered reservation", "lost acknowledgement", "retry pending", "import failure", "missing row", "wrong directory", "missing history", "export changes source", "import changes source", "lost acknowledgement changes source", "retry pending changes source", "wrong directory changes source", "foreign history changes source", "wrong workspace changes source", "unknown version"} {
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
				`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT)`,
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
				if directory != cwd || config != root {
					t.Fatalf("native command location/config/args = %q, %q, %v", directory, config, args)
				}
				if args[0] == "--version" {
					if mode == "unknown version" {
						return []byte("8.0.0"), nil
					}
					return []byte("7.8.25\n"), nil
				}
				if len(args) != 2 {
					t.Fatalf("unexpected args: %v", args)
				}
				if args[0] == "export" && args[1] == sourceID {
					if mode == "export changes source" {
						if _, err := db.Exec(`UPDATE session SET directory = '/changed' WHERE id = ?`, sourceID); err != nil {
							t.Fatal(err)
						}
					}
					return export, nil
				}
				if args[0] != "import" {
					t.Fatalf("unexpected native command: %v", args)
				}
				if strings.Contains(mode, "changes source") && mode != "export changes source" {
					if _, err := db.Exec(`INSERT INTO session_message VALUES ('canonical-change', ?)`, sourceID); err != nil {
						t.Fatal(err)
					}
				}
				info, err := os.Stat(args[1])
				if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
					t.Fatalf("private native export = %v, %v", info, err)
				}
				if mode == "import failure" || strings.HasPrefix(mode, "retry pending") && calls == 3 {
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
				// The receipt must already identify this exact graph BEFORE the
				// native provider makes any session/message/part rows visible.
				reservationPath := filepath.Join(dataRoot, "native-imports", "kilo", kiloReservationName(root, sourceID, cwd, ""))
				proof, err := os.ReadFile(reservationPath)
				if err != nil {
					t.Fatal(err)
				}
				reservationInfo, err := os.Stat(reservationPath)
				if err != nil || runtime.GOOS != "windows" && reservationInfo.Mode().Perm() != 0o600 {
					t.Fatalf("reservation is not private: %v, %v", reservationInfo, err)
				}
				var reservation kiloImportReservation
				if json.Unmarshal(proof, &reservation) != nil || reservation.NativeID != transformed.Info.ID || string(reservation.Export) != string(data) {
					t.Fatal("native import ran before its exact identity graph was reserved")
				}
				if strings.HasPrefix(mode, "wrong directory") {
					transformed.Info.Directory = "/source"
				}
				_, err = db.Exec(`INSERT INTO session VALUES (?, ?, NULL)`, transformed.Info.ID, transformed.Info.Directory)
				if err != nil || mode == "missing history" {
					return nil, err
				}
				if mode == "wrong workspace changes source" {
					if _, err := db.Exec(`UPDATE session SET workspace_id = 'wrk_foreign' WHERE id = ?`, transformed.Info.ID); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "foreign history changes source" {
					// A row at the reserved session ID is insufficient evidence.
					_, err := db.Exec(`INSERT INTO message VALUES ('msg_foreign', ?)`, transformed.Info.ID)
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
				if strings.HasPrefix(mode, "lost acknowledgement") {
					return nil, errors.New("native process lost after committing import")
				}
				return nil, nil
			}
			request := ports.ImportedSessionRelocation{NativeSessionID: sourceID, ConfigDir: root, LaunchCWD: cwd, DataDir: dataRoot}
			ref, err := New().prepareKiloImport(context.Background(), request, run)
			if mode == "native import" || mode == "tampered reservation" || mode == "import changes source" {
				if err != nil || ref.NativeSessionID == "" || ref.NativeSessionID == sourceID || ref.ConfigDir != root {
					t.Fatalf("native import = %#v, %v", ref, err)
				}
			} else if err == nil || ref.NativeSessionID != "" {
				t.Fatalf("failed import published identity: %#v, %v", ref, err)
			}
			if mode == "native import" || strings.HasPrefix(mode, "lost acknowledgement") || mode == "retry pending" || mode == "import changes source" {
				reservationPath := filepath.Join(dataRoot, "native-imports", "kilo", kiloReservationName(root, sourceID, cwd, ""))
				body, err := os.ReadFile(reservationPath)
				if err != nil {
					t.Fatal(err)
				}
				var reserved kiloImportReservation
				if err := json.Unmarshal(body, &reserved); err != nil {
					t.Fatal(err)
				}
				if mode == "native import" {
					// Even a source change after a successful publication must not
					// redirect retry to a new export or a different native identity.
					if _, err := db.Exec(`INSERT INTO session_message VALUES ('later-source-turn', ?)`, sourceID); err != nil {
						t.Fatal(err)
					}
				}
				again, retryErr := New().prepareKiloImport(context.Background(), request, run)
				if retryErr != nil || again.NativeSessionID == "" || again.NativeSessionID != reserved.NativeID || again.TranscriptPath != filepath.Join(root, "kilo.db", reserved.NativeID) || (mode == "native import" || mode == "import changes source") && again != ref {
					t.Fatalf("reservation retry = %#v, %v", again, retryErr)
				}
				after, err := os.ReadFile(reservationPath)
				if err != nil || string(after) != string(body) {
					t.Fatal("adoption replaced the reserved snapshot")
				}
				var copies int
				if err := db.QueryRow(`SELECT COUNT(*) FROM session WHERE id != ?`, sourceID).Scan(&copies); err != nil || copies != 1 {
					t.Fatalf("retry forked native history: copies=%d, %v", copies, err)
				}
			}
			if mode == "tampered reservation" {
				path := filepath.Join(dataRoot, "native-imports", "kilo", kiloReservationName(root, sourceID, cwd, ""))
				if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing history" || strings.HasPrefix(mode, "wrong directory") || mode == "foreign history changes source" || mode == "wrong workspace changes source" || mode == "retry pending changes source" || mode == "tampered reservation" {
				if again, err := New().prepareKiloImport(context.Background(), request, run); err == nil || again.NativeSessionID != "" {
					t.Fatalf("retry accepted rejected publication: %#v, %v", again, err)
				}
			}
			wantCalls := 3
			if mode == "native import" || strings.HasPrefix(mode, "lost acknowledgement") || mode == "missing history" || strings.HasPrefix(mode, "wrong directory") || mode == "foreign history changes source" || mode == "wrong workspace changes source" || mode == "retry pending changes source" || mode == "import changes source" || mode == "tampered reservation" {
				wantCalls = 4 // retry checks version but never exports/imports again
			}
			if mode == "retry pending" {
				wantCalls = 5 // version plus import reuses the unpublished reservation
			}
			if mode == "unknown version" {
				wantCalls = 1
			}
			if mode == "export changes source" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("native command calls = %d", calls)
			}
			var sourceDirectory, sourceWorkspace string
			if err := db.QueryRow(`SELECT directory, workspace_id FROM session WHERE id = ?`, sourceID).Scan(&sourceDirectory, &sourceWorkspace); err != nil || (sourceDirectory != "/source" && mode != "export changes source") || sourceWorkspace != "wrk_source" {
				t.Fatalf("original row changed: %q, %q, %v", sourceDirectory, sourceWorkspace, err)
			}
			if strings.Contains(mode, "changes source") && mode != "export changes source" || mode == "native import" {
				var sourceOnlyTurns, copiedTurns int
				if err := db.QueryRow(`SELECT COUNT(*) FROM session_message WHERE session_id = ?`, sourceID).Scan(&sourceOnlyTurns); err != nil || sourceOnlyTurns != 1 {
					t.Fatalf("source-only turn was lost: %d, %v", sourceOnlyTurns, err)
				}
				if err := db.QueryRow(`SELECT COUNT(*) FROM session_message WHERE session_id != ?`, sourceID).Scan(&copiedTurns); err != nil || copiedTurns != 0 {
					t.Fatalf("source-only turn entered the reserved copy: %d, %v", copiedTurns, err)
				}
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

func TestKiloImportRejectsWorkspaceOverrideBeforeNativeCommand(t *testing.T) {
	t.Setenv("KILO_WORKSPACE_ID", "")
	_, err := New().prepareKiloImport(t.Context(), ports.ImportedSessionRelocation{NativeSessionID: "ses_source", Env: map[string]string{"KILO_WORKSPACE_ID": "override"}, ConfigDir: filepath.Join(t.TempDir(), "kilo"), LaunchCWD: t.TempDir()}, func(context.Context, string, string, ...string) ([]byte, error) {
		t.Fatal("native command ran with workspace override")
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("workspace override = %v", err)
	}
}
