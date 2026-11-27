package sessionmanager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kilocode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
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
				Info  struct{ ID, Role string }
				Parts []struct{ ID, Type, Text string }
			}
		}
		if err := json.Unmarshal(body, &export); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(root, "kilo", "kilo.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO session (id,directory,workspace_id,parent_id,title,time_updated,time_archived) VALUES (?, ?, NULL, NULL, 'Reserved native copy', 1700000000000, NULL)`, export.Info.ID, export.Info.Directory); err != nil {
			t.Fatal(err)
		}
		for i, message := range export.Messages {
			data, err := json.Marshal(map[string]string{"role": message.Info.Role})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO message (id,session_id,time_created,data) VALUES (?, ?, ?, ?)`, message.Info.ID, export.Info.ID, 1700000000000+i, string(data)); err != nil {
				t.Fatal(err)
			}
			for _, part := range message.Parts {
				data, err := json.Marshal(map[string]string{"type": part.Type, "text": part.Text})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO part (id,session_id,message_id,time_created,data) VALUES (?, ?, ?, ?, ?)`, part.ID, export.Info.ID, message.Info.ID, 1700000000000+i, string(data)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if source := os.Getenv("AO_KILO_CHECKPOINT_SOURCE"); source != "" {
			if _, err := db.Exec(`INSERT INTO message (id,session_id,time_created,data) VALUES ('msg_source_only',?,1700000002000,'{"role":"user"}')`, source); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO part (id,session_id,message_id,time_created,data) VALUES ('prt_source_only',?,'msg_source_only',1700000002000,'{"type":"text","text":"SOURCE_ONLY_LATER"}')`, source); err != nil {
				t.Fatal(err)
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

type kiloArchiveRecoveryAgent struct {
	terminalImportAgent
	plugin *kilocode.Plugin
}

func (a *kiloArchiveRecoveryAgent) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	return a.plugin.PrepareImportedSession(ctx, r)
}

type kiloArchiveRecoveryStore struct {
	*failingImportWorkspaceStore
	messages   []sessionimport.Message
	history    [][]sessionimport.Message
	replaceErr error
}

func (s *kiloArchiveRecoveryStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return []domain.ProjectRecord{s.projects["mer"]}, nil
}
func (*kiloArchiveRecoveryStore) CreateImportedSession(context.Context, domain.SessionRecord, []sessionimport.Message) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, errors.New("unexpected registration during recovery")
}
func (s *kiloArchiveRecoveryStore) ReplaceImportedMessages(_ context.Context, _ domain.SessionID, messages []sessionimport.Message) error {
	s.history = append(s.history, append([]sessionimport.Message(nil), messages...))
	if s.replaceErr != nil {
		return s.replaceErr
	}
	s.messages = append([]sessionimport.Message(nil), messages...)
	return nil
}

func TestKiloImportCheckpointFailureReusesNativeIdentity(t *testing.T) {
	for _, failure := range []string{"none", "checkpoint", "archive"} {
		t.Run(failure, func(t *testing.T) { testKiloPreparedArchiveRecovery(t, failure) })
	}
}

func testKiloPreparedArchiveRecovery(t *testing.T, failure string) {
	t.Helper()
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
	source, message, part := "ses_"+strings.Repeat("a", 26), "msg_"+strings.Repeat("b", 26), "prt_"+strings.Repeat("c", 26)
	t.Setenv("AO_KILO_CHECKPOINT_SOURCE", source)
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
		`CREATE TABLE session (id TEXT PRIMARY KEY,directory TEXT,workspace_id TEXT,parent_id TEXT,title TEXT,time_updated INTEGER,time_archived INTEGER)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY,session_id TEXT,time_created INTEGER,data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY,session_id TEXT,message_id TEXT,time_created INTEGER,data TEXT)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY,session_id TEXT)`,
	} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO session (id,directory,workspace_id,parent_id,title,time_updated,time_archived) VALUES (?,'/source','source-workspace',NULL,'Original',1700000000000,NULL)`, source); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO message (id,session_id,time_created,data) VALUES (?,?,1700000000000,'{"role":"user"}')`, message, source); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO part (id,session_id,message_id,time_created,data) VALUES (?,?,?,1700000000000,'{"type":"text","text":"checkpoint recovery marker"}')`, part, source, message); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	agent := &kiloArchiveRecoveryAgent{terminalImportAgent: terminalImportAgent{native: true}, plugin: kilocode.New()}
	m, store, _ := newExitedResumeManager(t, rt, agent)
	m.dataDir = t.TempDir()
	failing := &failingImportWorkspaceStore{fakeStore: store}
	if failure == "checkpoint" {
		failing.failAt = 1
	}
	archive := &kiloArchiveRecoveryStore{failingImportWorkspaceStore: failing}
	m.store = archive
	svc := sessionimport.New(archive, m.dataDir, nil)
	m.SetImportArchiveRefresher(svc)
	launch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := store.sessions["mer-1"]
	rec.Harness, rec.Mode = domain.HarnessKilocode, domain.SessionModeTUI
	rec.Metadata.WorkspacePath, rec.Metadata.Branch, rec.Metadata.RuntimeHandleID, rec.Metadata.Prompt = launch, "ao/import", "", ""
	rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID, rec.Metadata.NativeTranscriptPath = source, source, source
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: source, ConfigDir: config, CWD: "/source", Transferred: true, Prepared: true}
	store.sessions[rec.ID] = rec
	if err := svc.RefreshArchive(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if failure == "archive" {
		archive.replaceErr = errors.New("injected archive checkpoint failure")
	}
	result, firstErr := m.ResumeAgentWithMode(t.Context(), rec.ID)
	if failure == "none" {
		if firstErr != nil || result.Session.NeedsImportResume() {
			t.Fatalf("initial prepared launch=%+v err=%v", result, firstErr)
		}
	} else if firstErr == nil || rt.created != 0 {
		t.Fatalf("failure not injected before runtime: %v created=%d", firstErr, rt.created)
	}
	var target string
	if err := db.QueryRow(`SELECT id FROM session WHERE id != ?`, source).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if failure == "checkpoint" && store.sessions[rec.ID].Metadata.ImportSource.ResumeNativeID != "" {
		t.Fatal("failed checkpoint persisted target")
	}
	if failure != "none" {
		if _, err := db.Exec(`INSERT INTO message (id,session_id,time_created,data) VALUES ('msg_native_later',?,1700000001000,'{"role":"assistant"}')`, target); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO part (id,session_id,message_id,time_created,data) VALUES ('prt_native_later',?,'msg_native_later',1700000001000,'{"type":"text","text":"NATIVE_ONLY_LATER"}')`, target); err != nil {
			t.Fatal(err)
		}
		agent.plugin, archive.replaceErr = kilocode.New(), nil
		result, err = m.ResumeAgentWithMode(t.Context(), rec.ID)
		if err != nil || result.Session.NeedsImportResume() {
			t.Fatalf("prepared retry=%+v err=%v", result, err)
		}
	}
	got := store.sessions[rec.ID]
	binding := got.Metadata.ImportSource
	if got.Metadata.AgentSessionID != target || got.Metadata.ProviderConversationID != target || binding.ResumeNativeID != target || binding.NativeID != source || binding.ConfigDir != config || binding.ResumeConfigDir != config || binding.ResumeCwd != launch || binding.ResumeTranscriptPath != filepath.Join(config, "kilo.db", target) {
		t.Fatalf("prepared binding=%+v metadata=%+v", binding, got.Metadata)
	}
	want := 1
	if failure != "none" {
		want = 2
	}
	if len(archive.messages) != want || archive.messages[0].Text != "checkpoint recovery marker" || (want == 2 && archive.messages[1].Text != "NATIVE_ONLY_LATER") {
		t.Fatalf("archive differs from reserved native graph: %+v", archive.messages)
	}
	for _, snapshot := range archive.history {
		for _, message := range snapshot {
			if strings.Contains(message.Text, "SOURCE_ONLY_LATER") {
				t.Fatal("source-only turn entered displayed archive")
			}
		}
	}
	// Reopen the adopted identity after the original grows again. Its archived
	// imported history and reserved native graph must not be replaced.
	if _, err := db.Exec(`INSERT INTO message (id,session_id,time_created,data) VALUES ('msg_source_again',?,1700000003000,'{"role":"user"}')`, source); err != nil {
		t.Fatal(err)
	}
	got.Activity.State = domain.ActivityExited
	store.sessions[rec.ID] = got
	rt.exitedLaunches[got.Metadata.RuntimeLaunchID] = true
	if reopened, err := m.ResumeAgentWithMode(t.Context(), rec.ID); err != nil || reopened.Session.Metadata.AgentSessionID != target {
		t.Fatalf("same-ID reopen=%+v err=%v", reopened, err)
	}
	if len(archive.messages) != want {
		t.Fatal("reopen replaced the prepared archive")
	}
	var copies, sourceTurns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session WHERE id != ?`, source).Scan(&copies); err != nil || copies != 1 {
		t.Fatalf("copies=%d err=%v", copies, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM message WHERE id='msg_source_only' AND session_id=?`, source).Scan(&sourceTurns); err != nil || sourceTurns != 1 {
		t.Fatal("source-only history was removed")
	}
	commands, err := os.ReadFile(filepath.Join(root, "commands"))
	if err != nil || strings.Count(string(commands), "export\n") != 1 || strings.Count(string(commands), "import\n") != 1 {
		t.Fatalf("commands=%q err=%v", commands, err)
	}
}
