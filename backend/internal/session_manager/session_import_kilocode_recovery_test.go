package sessionmanager

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kilocode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The native CLI fixture writes only this test's synthetic conversation tables.
// Running the real preparer through its command boundary also tests that its
// read-only connection observes publication before the S2 checkpoint fails.
func TestKiloImportCheckpointCommand(t *testing.T) {
	if os.Getenv("AO_KILO_CHECKPOINT_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		t.Fatal("missing synthetic CLI command")
	}
	args = args[1:]
	root := os.Getenv("AO_KILO_CHECKPOINT_ROOT")
	log, err := os.OpenFile(filepath.Join(root, "commands"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(log, args[0]); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	switch args[0] {
	case "--version":
		fmt.Println("7.8.25")
	case "export":
		body, err := os.ReadFile(filepath.Join(root, "export.json"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Print(string(body))
	case "import":
		body, err := os.ReadFile(args[1])
		if err != nil {
			t.Fatal(err)
		}
		var export struct {
			Info     struct{ ID, Directory string }
			Messages []struct {
				Info  struct{ ID string }
				Parts []struct{ ID string }
			}
		}
		if err := json.Unmarshal(body, &export); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(root, "kilo", "kilo.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO session VALUES (?, ?, NULL)`, export.Info.ID, export.Info.Directory); err != nil {
			t.Fatal(err)
		}
		for _, message := range export.Messages {
			if _, err := db.Exec(`INSERT INTO message VALUES (?, ?)`, message.Info.ID, export.Info.ID); err != nil {
				t.Fatal(err)
			}
			for _, part := range message.Parts {
				if _, err := db.Exec(`INSERT INTO part VALUES (?, ?, ?)`, part.ID, export.Info.ID, message.Info.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unexpected synthetic CLI command: %s", args[0])
	}
	os.Exit(0)
}

func TestKiloImportCheckpointFailureReusesNativeIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX synthetic CLI launcher")
	}
	root := t.TempDir()
	config := filepath.Join(root, "kilo")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"KILO_WORKSPACE_ID", "OPENCODE_WORKSPACE_ID", "KILO_DB"} {
		t.Setenv(key, "")
	}
	t.Setenv("AO_KILO_CHECKPOINT_HELPER", "1")
	t.Setenv("AO_KILO_CHECKPOINT_ROOT", root)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := "#!/bin/sh\nexec '" + strings.ReplaceAll(binary, "'", "'\\''") + "' -test.run='^TestKiloImportCheckpointCommand$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "kilo-cli"), []byte(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "kilo-cli"), filepath.Join(bin, "kilo")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	source := "ses_" + strings.Repeat("a", 26)
	message := "msg_" + strings.Repeat("b", 26)
	part := "prt_" + strings.Repeat("c", 26)
	export := fmt.Sprintf(`{"info":{"id":%q,"directory":"/source"},"messages":[{"info":{"id":%q,"sessionID":%q,"role":"user"},"parts":[{"id":%q,"sessionID":%q,"messageID":%q,"type":"text","text":"checkpoint recovery marker"}]}]}`, source, message, source, part, source, message)
	if err := os.WriteFile(filepath.Join(root, "export.json"), []byte(export), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(config, "kilo.db"))
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
	if _, err := db.Exec(`INSERT INTO session VALUES (?, '/source', 'source-workspace')`, source); err != nil {
		t.Fatal(err)
	}
	m, store, _ := newExitedResumeManager(t, &fakeRuntime{}, &terminalImportAgent{})
	m.dataDir = t.TempDir()
	failing := &failingImportWorkspaceStore{fakeStore: store, failAt: 1}
	m.store = failing
	rec := store.sessions["mer-1"]
	rec.Harness = domain.HarnessKilocode
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: source, ConfigDir: config, Prepared: true}
	store.sessions[rec.ID] = rec
	launch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareImportedNativeSession(context.Background(), rec, launch, kilocode.New(), nil); err == nil || !strings.Contains(err.Error(), "injected database write failure") {
		t.Fatalf("checkpoint failure not injected after publication: %v", err)
	}
	var target string
	if err := db.QueryRow(`SELECT id FROM session WHERE id != ?`, source).Scan(&target); err != nil {
		t.Fatalf("native import was not published before checkpoint failure: %v", err)
	}
	if store.sessions[rec.ID].Metadata.ImportSource.ResumeNativeID != "" {
		t.Fatal("failed checkpoint persisted a native identity")
	}
	// A fresh adapter has no memory of the allocated IDs. It must recover
	// from the durable reservation, including any later provider-owned rows.
	if _, err := db.Exec(`INSERT INTO message VALUES ('msg_later', ?)`, target); err != nil {
		t.Fatal(err)
	}
	again, err := m.prepareImportedNativeSession(context.Background(), store.sessions[rec.ID], launch, kilocode.New(), nil)
	if err != nil || again.Metadata.AgentSessionID != target || again.Metadata.ProviderConversationID != target || store.sessions[rec.ID].Metadata.ImportSource.ResumeNativeID != target {
		t.Fatalf("checkpoint retry changed the native target: %#v, %v", again.Metadata, err)
	}
	var copies, later int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session WHERE id != ?`, source).Scan(&copies); err != nil || copies != 1 {
		t.Fatalf("retry created another native copy: %d, %v", copies, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM message WHERE id = 'msg_later' AND session_id = ?`, target).Scan(&later); err != nil || later != 1 {
		t.Fatalf("retry changed later native history: %d, %v", later, err)
	}
	commands, err := os.ReadFile(filepath.Join(root, "commands"))
	if err != nil || strings.Count(string(commands), "export\n") != 1 || strings.Count(string(commands), "import\n") != 1 {
		t.Fatalf("retry ran export/import again: %q, %v", commands, err)
	}
}
