package devin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const nativeImportTestID = "sage-otter-522"

func initializeImportFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "cli", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	const schema = `
CREATE TABLE refinery_schema_history(version INTEGER PRIMARY KEY,name TEXT,applied_on TEXT,checksum TEXT);
CREATE TABLE sessions(id TEXT PRIMARY KEY,working_directory TEXT NOT NULL,backend_type TEXT NOT NULL,model TEXT NOT NULL,agent_mode TEXT NOT NULL,created_at INTEGER NOT NULL,last_activity_at INTEGER NOT NULL,title TEXT,main_chain_id INTEGER,shell_last_seen_index INTEGER DEFAULT 0,cogs_json TEXT,workspace_dirs TEXT,hidden INTEGER NOT NULL DEFAULT 0,metadata TEXT);
CREATE TABLE message_nodes(row_id INTEGER PRIMARY KEY AUTOINCREMENT,session_id TEXT NOT NULL,node_id INTEGER NOT NULL,parent_node_id INTEGER,chat_message TEXT NOT NULL,created_at INTEGER NOT NULL,metadata TEXT,UNIQUE(session_id,node_id));
CREATE TABLE tool_call_state(session_id TEXT NOT NULL,tool_call_id TEXT NOT NULL,tool_call_json TEXT,tool_call_update_json TEXT,source_node_ids TEXT,PRIMARY KEY(session_id,tool_call_id));
CREATE TABLE prompt_history(id INTEGER PRIMARY KEY AUTOINCREMENT,content TEXT NOT NULL,timestamp INTEGER NOT NULL,session_id TEXT NOT NULL,is_shell INTEGER NOT NULL DEFAULT 0);
CREATE TABLE rendered_commits(id INTEGER PRIMARY KEY AUTOINCREMENT,session_id TEXT NOT NULL,sequence_number INTEGER NOT NULL,rendered_html TEXT NOT NULL,created_at INTEGER NOT NULL,UNIQUE(session_id,sequence_number));
CREATE TABLE live_row_times(session_id TEXT NOT NULL,row_key TEXT NOT NULL,started_at TEXT NOT NULL,PRIMARY KEY(session_id,row_key));
CREATE TABLE subagent_heads(session_id TEXT NOT NULL,agent_id TEXT NOT NULL,chain_node_id INTEGER NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(session_id,agent_id));`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		if _, err := db.Exec("INSERT INTO refinery_schema_history VALUES(?, 'native', '', 'native-checksum')", i); err != nil {
			t.Fatal(err)
		}
	}
}

func importMessageFixture(id, role, text string, user bool) string {
	b, _ := json.Marshal(map[string]any{"message_id": id, "role": role, "content": text, "metadata": map[string]any{"is_user_input": user}})
	return string(b)
}

func nativeImportFixture(t *testing.T) (string, string, *sql.DB) {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	initializeImportFixture(t, root)
	db, err := sql.Open("sqlite", filepath.Join(root, "cli", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO sessions(id,working_directory,backend_type,model,agent_mode,created_at,last_activity_at,title,main_chain_id,cogs_json,workspace_dirs,metadata) VALUES(?,?,'windsurf','native-model','normal',1790000000,1790000001,'native chat',3,'[]','[]','{}')`, nativeImportTestID, cwd); err != nil {
		t.Fatal(err)
	}
	for _, n := range []struct {
		id         int
		parent     any
		role, text string
		user       bool
	}{
		{0, nil, "system", "hidden system", false}, {1, 0, "user", "remember PLUM", true}, {2, 1, "user", "hidden runtime", false}, {3, 2, "assistant", "PLUM remembered", false}, {4, 1, "assistant", "inactive branch", false},
	} {
		if _, err := db.Exec(`INSERT INTO message_nodes(session_id,node_id,parent_node_id,chat_message,created_at) VALUES(?,?,?,?,1790000001)`, nativeImportTestID, n.id, n.parent, importMessageFixture(string(rune('a'+n.id)), n.role, n.text, n.user)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO tool_call_state VALUES(?,'tool-1','{"toolCallId":"tool-1","rawInput":{"command":"pwd"}}','{"status":"completed"}','[3]')`, nativeImportTestID); err != nil {
		t.Fatal(err)
	}
	return root, cwd, db
}

func TestImportArchiveReadsOnlyActiveHumanHistory(t *testing.T) {
	root, cwd, _ := nativeImportFixture(t)
	a, err := ReadImportArchive(context.Background(), root, nativeImportTestID)
	if err != nil || a.CWD != cwd || len(a.Messages) != 2 || a.Messages[0].Text != "remember PLUM" || a.Messages[1].Text != "PLUM remembered" || a.Fingerprint == "" {
		t.Fatalf("native archive invalid: %#v, %v", a, err)
	}
}

func TestImportArchiveRejectsCorruptAndUnsupportedState(t *testing.T) {
	for _, kind := range []string{"cycle before user", "missing before user", "missing head", "blank", "attachments", "external workspace", "subagent", "schema"} {
		t.Run(kind, func(t *testing.T) {
			root, _, db := nativeImportFixture(t)
			var err error
			switch kind {
			case "cycle before user":
				_, err = db.Exec("UPDATE message_nodes SET parent_node_id=0 WHERE node_id=0")
			case "missing before user":
				_, err = db.Exec("DELETE FROM message_nodes WHERE node_id=0")
			case "missing head":
				_, err = db.Exec("UPDATE sessions SET main_chain_id=99")
			case "blank":
				_, err = db.Exec("UPDATE message_nodes SET chat_message=? WHERE node_id=1", importMessageFixture("user", "user", " \t ", true))
			case "attachments":
				_, err = db.Exec(`UPDATE message_nodes SET chat_message=json_set(chat_message,'$.images',json('["external-image"]')) WHERE node_id=1`)
			case "external workspace":
				b, _ := json.Marshal([]string{t.TempDir()})
				_, err = db.Exec("UPDATE sessions SET workspace_dirs=?", string(b))
			case "subagent":
				_, err = db.Exec("INSERT INTO subagent_heads VALUES(?,'child',3,0)", nativeImportTestID)
			case "schema":
				_, err = db.Exec("INSERT INTO refinery_schema_history VALUES(21,'future','','')")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadImportArchive(context.Background(), root, nativeImportTestID); err == nil {
				t.Fatalf("accepted %s", kind)
			}
		})
	}
}

func TestImportPreparationPreservesNativeStateRetriesAndUpgradeReopen(t *testing.T) {
	ctx := context.Background()
	root, cwd, source := nativeImportFixture(t)
	a, err := ReadImportArchive(ctx, root, nativeImportTestID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cli", "sessions.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(before)
	p := New()
	p.importVersion = func(context.Context) error { return nil }
	p.importInitialize = func(_ context.Context, root string) error { initializeImportFixture(t, root); return nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: nativeImportTestID, ConfigDir: root, SourceCWD: cwd, LaunchCWD: t.TempDir(), DataDir: t.TempDir(), SourceFingerprint: a.Fingerprint, Env: map[string]string{"XDG_CONFIG_HOME": t.TempDir()}}
	ref, err := p.PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != hash {
		t.Fatalf("source changed: %v", err)
	}
	db, err := sql.Open("sqlite", ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	var actualCWD string
	var branches, tools int
	if err := db.QueryRow("SELECT working_directory FROM sessions WHERE id=?", nativeImportTestID).Scan(&actualCWD); err != nil {
		t.Fatal(err)
	}
	wantCWD, err := filepath.EvalSymlinks(r.LaunchCWD)
	if err != nil {
		t.Fatal(err)
	}
	if actualCWD != wantCWD {
		t.Fatalf("native cwd=%s, want %s", actualCWD, wantCWD)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM message_nodes").Scan(&branches); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM tool_call_state").Scan(&tools); err != nil {
		t.Fatal(err)
	}
	if branches != 5 || tools != 1 {
		t.Fatalf("native state lost: nodes%d tools%d", branches, tools)
	}
	if _, err := db.Exec(`INSERT INTO message_nodes(session_id,node_id,parent_node_id,chat_message,created_at) VALUES(?,99,3,?,1790000002)`, nativeImportTestID, importMessageFixture("later", "user", "new private turn", true)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE sessions SET main_chain_id=99"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_ = source.Close()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	p.importVersion = func(context.Context) error { return errors.New("new CLI version") }
	again, err := p.PrepareImportedSession(ctx, r)
	if err != nil || again.ConfigDir != ref.ConfigDir {
		t.Fatalf("retry lost evolved history: %#v, %v", again, err)
	}
	p.resolvedBinary = "devin-test-binary"
	restore := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeImportTestID, ports.MetadataKeyImportSourceNativeID: nativeImportTestID, ports.MetadataKeyImportConfigDir: ref.ConfigDir}}}
	argv, ok, err := p.GetRestoreCommand(ctx, restore)
	if err != nil || !ok || !slices.Contains(argv, nativeImportTestID) || !slices.Contains(argv, "--config") {
		t.Fatalf("upgraded restore=%v,%v,%v", argv, ok, err)
	}
	env := map[string]string{importStoreEnv: ref.ConfigDir, "DEVIN_SESSION_STORE": "files"}
	p.AugmentRuntimeLaunchEnv(env, "", "", "")
	if env["DEVIN_SESSION_STORE"] != "sqlite" || env["XDG_CONFIG_HOME"] != filepath.Join(ref.ConfigDir, "config") {
		t.Fatal("import launch did not preserve private configuration")
	}
	hook := ports.WorkspaceHookConfig{ImportConfigDir: ref.ConfigDir, NativeSessionID: nativeImportTestID, WorkspacePath: r.LaunchCWD, Env: env}
	if err := p.PrepareRuntimeLaunch(ctx, hook); err != nil {
		t.Fatal(err)
	}
	// Imported prelaunch validates private proof instead of writing either
	// global Devin or Claude workspace trust configuration.
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalPaths := []string{filepath.Join(home, ".claude.json"), filepath.Join(home, ".local", "share", "devin", "cli", "trusted_workspaces.json")}
	for _, path := range globalPaths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.PreLaunch(ctx, ports.LaunchConfig{WorkspacePath: hook.WorkspacePath, Env: env}); err != nil {
		t.Fatal(err)
	}
	for _, path := range globalPaths {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != "{}" {
			t.Fatalf("global trust changed: %s,%v", path, err)
		}
	}
	if state, err := p.ProbeNativeSession(ctx, ref); err != nil || state != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("upgraded probe=%v,%v", state, err)
	}
	r.LaunchCWD = t.TempDir()
	if _, err := p.PrepareImportedSession(ctx, r); err == nil {
		t.Fatal("new preparation accepted unverified CLI")
	}
	if err := os.Remove(ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := p.GetRestoreCommand(ctx, restore); err == nil || ok {
		t.Fatal("missing native state fell through to fresh chat")
	}
	if err := p.PrepareRuntimeLaunch(ctx, hook); err == nil {
		t.Fatal("runtime accepted missing native history")
	}
}
