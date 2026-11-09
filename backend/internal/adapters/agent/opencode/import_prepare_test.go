package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The test binary doubles as a fake `opencode` CLI for PrepareImportedSession:
// `export` prints a fixture, and `import` records the session into a test
// opencode.db bound to the directory it ran in, as the real CLI does.
func TestMain(m *testing.M) {
	if db := os.Getenv("AO_FAKE_OPENCODE_DB"); db != "" {
		os.Exit(fakeOpenCode(db, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeOpenCode(dbPath string, args []string) int {
	if log := os.Getenv("AO_FAKE_OPENCODE_LOG"); log != "" {
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		_, _ = fmt.Fprintln(f, strings.Join(args, " "))
		_ = f.Close()
	}
	if len(args) < 3 || args[1] != "--pure" {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		return 2
	}
	switch args[0] {
	case "export":
		data, err := os.ReadFile(os.Getenv("AO_FAKE_OPENCODE_EXPORT"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Exporting session: %s\n", args[2])
		_, _ = os.Stdout.Write(data)
		return 0
	case "import":
		data, err := os.ReadFile(args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		cwd, _ := os.Getwd()
		if dir := os.Getenv("AO_FAKE_OPENCODE_BIND"); dir != "" {
			cwd = dir
		}
		id, err := storeOpenCodeExport(dbPath, data, cwd, time.Now().UnixMilli())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Imported session: %s\n", id)
		return 0
	}
	return 2
}

// storeOpenCodeExport records an export in a test opencode.db as OpenCode's
// import does: bound to cwd, storing each message's and part's data without
// its ids, keeping message times, and setting part timestamps at import time.
func storeOpenCodeExport(dbPath string, data []byte, cwd string, now int64) (string, error) {
	var doc struct {
		Info struct {
			ID string `json:"id"`
		} `json:"info"`
		Messages []struct {
			Info  map[string]any   `json:"info"`
			Parts []map[string]any `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO session (id, directory) VALUES (?, ?)`, doc.Info.ID, cwd); err != nil {
		return "", err
	}
	for _, m := range doc.Messages {
		id, _ := m.Info["id"].(string)
		created, _ := m.Info["time"].(map[string]any)["created"].(float64)
		delete(m.Info, "id")
		delete(m.Info, "sessionID")
		info, _ := json.Marshal(m.Info)
		if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`, id, doc.Info.ID, int64(created), now, string(info)); err != nil {
			return "", err
		}
		for _, part := range m.Parts {
			partID, _ := part["id"].(string)
			for _, key := range []string{"id", "sessionID", "messageID"} {
				delete(part, key)
			}
			body, _ := json.Marshal(part)
			if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`, partID, id, doc.Info.ID, now, now, string(body)); err != nil {
				return "", err
			}
		}
	}
	return doc.Info.ID, nil
}

const (
	ocSourceID = "ses_e55937592ffeaNXsUampvachsM"
	ocMsg1     = "msg_1aa6c8acd001mZuCzEhM7Ci74n"
	ocMsg2     = "msg_1aa6c8b0e002Q8d0rT4kLmNpQs"
	ocPart1    = "prt_1aa6c8acd002aBcDeFgHiJkLmN"
	ocPart2    = "prt_1aa6c8b0e003ZyXwVuTsRqPoNm"
	ocPart3    = "prt_1aa6c8b0e004ToolCallPartXyZ"
)

func openCodeExportFixture(sourceDir string) string {
	return `{"info":{"id":"` + ocSourceID + `","slug":"cosmic-otter","projectID":"842266f1","directory":"` + sourceDir + `",` +
		`"title":"Exact KIWI reply","version":"1.19.3","time":{"created":1762324000000,"updated":1762324100000},"revert":{"messageID":"` + ocMsg2 + `"}},` +
		`"messages":[` +
		`{"info":{"id":"` + ocMsg1 + `","sessionID":"` + ocSourceID + `","role":"user","time":{"created":1762324000001}},` +
		`"parts":[{"id":"` + ocPart1 + `","sessionID":"` + ocSourceID + `","messageID":"` + ocMsg1 + `","type":"text","text":"Reply with KIWI"}]},` +
		`{"info":{"id":"` + ocMsg2 + `","sessionID":"` + ocSourceID + `","role":"assistant","time":{"created":1762324000002},"parentID":"` + ocMsg1 + `","path":{"cwd":"` + sourceDir + `","root":"` + sourceDir + `"},"cost":0.000123456789,"tokens":{"input":12345678901}},` +
		`"parts":[{"id":"` + ocPart2 + `","sessionID":"` + ocSourceID + `","messageID":"` + ocMsg2 + `","type":"text","text":"KIWI"},` +
		`{"id":"` + ocPart3 + `","sessionID":"` + ocSourceID + `","messageID":"` + ocMsg2 + `","type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"ls"},"output":"README.md"}}]}` +
		`]}`
}

type fakeOpenCodeEnv struct {
	configDir, launch, source, log, export, dataDir string
}

func newFakeOpenCode(t *testing.T) (*Plugin, fakeOpenCodeEnv) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	env := fakeOpenCodeEnv{
		configDir: filepath.Join(root, "data"),
		launch:    filepath.Join(root, "worktree"),
		source:    filepath.Join(root, "source"),
		log:       filepath.Join(root, "calls.log"),
		export:    filepath.Join(root, "export.json"),
		dataDir:   filepath.Join(root, "ao-data"),
	}
	for _, dir := range []string{env.configDir, env.launch, env.source, env.dataDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Resolve the temp root so stored and launch paths compare as OpenCode's do.
	if env.launch, err = filepath.EvalSymlinks(env.launch); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(env.configDir, "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE session (id text PRIMARY KEY, directory text, parent_id text, time_archived integer)`,
		`CREATE TABLE message (id text PRIMARY KEY, session_id text, time_created integer, time_updated integer, data text)`,
		`CREATE TABLE part (id text PRIMARY KEY, message_id text, session_id text, time_created integer, time_updated integer, data text)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	if err := os.WriteFile(env.export, []byte(openCodeExportFixture(env.source)), 0o600); err != nil {
		t.Fatal(err)
	}
	// The source session the export describes, as discovery reads it.
	if _, err := storeOpenCodeExport(dbPath, []byte(openCodeExportFixture(env.source)), env.source, 1762324000500); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_FAKE_OPENCODE_DB", dbPath)
	t.Setenv("AO_FAKE_OPENCODE_EXPORT", env.export)
	t.Setenv("AO_FAKE_OPENCODE_LOG", env.log)
	t.Setenv("AO_FAKE_OPENCODE_BIND", "")
	return &Plugin{resolvedBinary: exe, importBinary: exe}, env
}

func (e fakeOpenCodeEnv) relocation() ports.ImportedSessionRelocation {
	return ports.ImportedSessionRelocation{NativeSessionID: ocSourceID, ConfigDir: e.configDir, SourceCWD: e.source, LaunchCWD: e.launch, DataDir: e.dataDir}
}

// fingerprint is the discovery fingerprint of a session in the test store.
func (e fakeOpenCodeEnv) fingerprint(t *testing.T, id string) string {
	t.Helper()
	got, err := openCodeSessionFingerprint(context.Background(), e.configDir, id)
	if err != nil || got == "" {
		t.Fatalf("fingerprint of %s = %q, %v", id, got, err)
	}
	return got
}

// addTurn appends a message to a session, as another turn in OpenCode would.
func (e fakeOpenCodeEnv) addTurn(t *testing.T, session, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(e.configDir, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, 1762324009999, 1762324009999, '{"role":"user"}')`, id, session); err != nil {
		t.Fatal(err)
	}
}

// editPart replaces the data of a session's part whose text or tool matches.
func (e fakeOpenCodeEnv) editPart(t *testing.T, session, match, data string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(e.configDir, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	res, err := db.Exec(`UPDATE part SET data = ? WHERE session_id = ? AND (json_extract(data, '$.text') = ? OR json_extract(data, '$.tool') = ?)`, data, session, match, match)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("edited %d parts matching %q", n, match)
	}
}

func (e fakeOpenCodeEnv) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(e.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestPrepareImportedOpenCodeSessionCopiesIntoLaunchDirectory(t *testing.T) {
	p, env := newFakeOpenCode(t)
	before, err := os.ReadFile(env.export)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := p.PrepareImportedSession(context.Background(), env.relocation())
	if err != nil {
		t.Fatal(err)
	}
	if ref.NativeSessionID == ocSourceID || !strings.HasPrefix(ref.NativeSessionID, ocSourceID[:openCodeIDTimeLen]) || len(ref.NativeSessionID) != len(ocSourceID) || ref.ConfigDir != env.configDir {
		t.Fatalf("prepared ref = %+v", ref)
	}
	copied, found, err := readOpenCodeCopy(context.Background(), env.configDir, ref.NativeSessionID)
	if err != nil || !found || copied.directory != env.launch || copied.messages != 2 {
		t.Fatalf("copy = %+v found=%v err=%v", copied, found, err)
	}
	if calls := env.calls(t); len(calls) != 2 || calls[0] != "export --pure "+ocSourceID || !strings.HasPrefix(calls[1], "import --pure ") {
		t.Fatalf("calls = %q", calls)
	}
	if after, _ := os.ReadFile(env.export); string(after) != string(before) {
		t.Fatal("source export changed")
	}
	if source, found, _ := readOpenCodeCopy(context.Background(), env.configDir, ocSourceID); !found || source.directory != env.source || source.messages != 2 {
		t.Fatalf("source session changed: %+v", source)
	}

	// A retry reuses the published copy without running OpenCode again.
	again, err := p.PrepareImportedSession(context.Background(), env.relocation())
	if err != nil || again != ref {
		t.Fatalf("retry = %+v, %v; want %+v", again, err, ref)
	}
	if calls := env.calls(t); len(calls) != 2 {
		t.Fatalf("retry ran OpenCode again: %q", calls)
	}
}

func TestPrepareImportedOpenCodeSessionRejectsCopyBoundElsewhere(t *testing.T) {
	p, env := newFakeOpenCode(t)
	t.Setenv("AO_FAKE_OPENCODE_BIND", env.source) // OpenCode bound the copy to the wrong folder.
	if _, err := p.PrepareImportedSession(context.Background(), env.relocation()); err == nil || !strings.Contains(err.Error(), "not bound to AO's workspace") {
		t.Fatalf("copy bound elsewhere accepted: %v", err)
	}
	// The mis-bound copy is never reused either.
	t.Setenv("AO_FAKE_OPENCODE_BIND", "")
	if _, err := p.PrepareImportedSession(context.Background(), env.relocation()); err == nil || !strings.Contains(err.Error(), "another directory") {
		t.Fatalf("mis-bound copy reused: %v", err)
	}
}

func TestPrepareImportedOpenCodeSessionRejectsBadInput(t *testing.T) {
	p, env := newFakeOpenCode(t)
	for name, mutate := range map[string]func(*ports.ImportedSessionRelocation){
		"invalid id":        func(r *ports.ImportedSessionRelocation) { r.NativeSessionID = "../ses_x" },
		"short id":          func(r *ports.ImportedSessionRelocation) { r.NativeSessionID = "ses_abc" },
		"relative launch":   func(r *ports.ImportedSessionRelocation) { r.LaunchCWD = "worktree" },
		"missing launch":    func(r *ports.ImportedSessionRelocation) { r.LaunchCWD = filepath.Join(env.launch, "gone") },
		"export of another": func(r *ports.ImportedSessionRelocation) { r.NativeSessionID = "ses_e55937592ffeOtherSession1234" },
	} {
		t.Run(name, func(t *testing.T) {
			r := env.relocation()
			mutate(&r)
			if ref, err := p.PrepareImportedSession(context.Background(), r); err == nil {
				t.Fatalf("accepted: %+v", ref)
			}
		})
	}
	if err := os.WriteFile(env.export, []byte(`{"info":{"id":"`+ocSourceID+`","directory":"/x"},"messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareImportedSession(context.Background(), env.relocation()); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("empty export accepted: %v", err)
	}
}

func TestRewriteOpenCodeExportRebindsIdentityAndDirectory(t *testing.T) {
	const source, launch = "/src/repo", "/ao/worktree"
	target := derivedOpenCodeID(ocSourceID, launch)
	out, n, err := rewriteOpenCodeExport([]byte(openCodeExportFixture(source)), ocSourceID, target, launch)
	if err != nil || n != 2 {
		t.Fatalf("rewrite n=%d err=%v", n, err)
	}
	text := string(out)
	for _, old := range []string{ocSourceID, ocMsg1, ocMsg2, ocPart1, ocPart2, source} {
		if strings.Contains(text, old) {
			t.Fatalf("rewritten export still references %q: %s", old, text)
		}
	}
	// Numbers keep full precision and unknown fields pass through.
	for _, keep := range []string{`"cost":0.000123456789`, `"input":12345678901`, `"projectID":"842266f1"`, `"slug":"cosmic-otter"`, `"text":"KIWI"`} {
		if !strings.Contains(text, keep) {
			t.Fatalf("rewritten export lost %s: %s", keep, text)
		}
	}
	var doc struct {
		Info struct {
			ID, Directory string
			Revert        struct{ MessageID string }
		}
		Messages []struct {
			Info struct {
				ID, SessionID, ParentID string
				Path                    struct{ Cwd, Root string }
			}
			Parts []struct{ ID, SessionID, MessageID string }
		}
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	m1, m2 := doc.Messages[0].Info, doc.Messages[1].Info
	if doc.Info.ID != target || doc.Info.Directory != launch || doc.Info.Revert.MessageID != m2.ID {
		t.Fatalf("session info = %+v", doc.Info)
	}
	if m1.SessionID != target || m2.SessionID != target || m2.ParentID != m1.ID || m2.Path.Cwd != launch || m2.Path.Root != launch {
		t.Fatalf("messages = %+v %+v", m1, m2)
	}
	for i, m := range doc.Messages {
		if p := m.Parts[0]; p.SessionID != target || p.MessageID != m.Info.ID || p.ID == "" {
			t.Fatalf("part %d = %+v", i, p)
		}
	}
	// Ids keep their prefix and time component so OpenCode's ordering holds.
	if m1.ID[:openCodeIDTimeLen] != ocMsg1[:openCodeIDTimeLen] || len(m1.ID) != len(ocMsg1) {
		t.Fatalf("message id %q lost its time component", m1.ID)
	}
	if _, _, err := rewriteOpenCodeExport([]byte(openCodeExportFixture(source)), "ses_e55937592ffeSomethingElse12", target, launch); err == nil {
		t.Fatal("export of a different session accepted")
	}
}

func TestDerivedOpenCodeIDIsStablePerLaunchDirectory(t *testing.T) {
	a, b := derivedOpenCodeID(ocSourceID, "/ao/one"), derivedOpenCodeID(ocSourceID, "/ao/two")
	if a != derivedOpenCodeID(ocSourceID, "/ao/one") || a == b || a == ocSourceID {
		t.Fatalf("ids: %q %q", a, b)
	}
	if !openCodeSessionID.MatchString(a) || len(a) != len(ocSourceID) {
		t.Fatalf("derived id %q is not an OpenCode session id", a)
	}
}

func TestPrepareImportedOpenCodeSessionStagesUnderAOData(t *testing.T) {
	p, env := newFakeOpenCode(t)
	external := t.TempDir()
	t.Setenv("TMPDIR", external)
	ref, err := p.PrepareImportedSession(context.Background(), env.relocation())
	if err != nil {
		t.Fatal(err)
	}
	calls := env.calls(t)
	staged := strings.TrimPrefix(calls[len(calls)-1], "import --pure ")
	staging := filepath.Join(env.dataDir, "import-staging")
	if rel, err := filepath.Rel(staging, staged); err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("export staged at %q, want under %q", staged, staging)
	}
	if left, _ := os.ReadDir(staging); len(left) != 0 {
		t.Fatalf("staging not cleaned up: %v", left)
	}
	if left, _ := os.ReadDir(external); len(left) != 0 {
		t.Fatalf("OS temporary directory used: %v", left)
	}
	if ref.NativeSessionID == "" {
		t.Fatal("no copy")
	}

	// Without AO's data directory there is nowhere private to stage a copy.
	_, env = newFakeOpenCode(t)
	r := env.relocation()
	r.DataDir = ""
	if _, err := p.PrepareImportedSession(context.Background(), r); err == nil || !strings.Contains(err.Error(), "data directory") {
		t.Fatalf("missing data directory accepted: %v", err)
	}
}

func TestPrepareImportedOpenCodeSessionHoldsTheSelectedHistory(t *testing.T) {
	t.Run("copy matches the selection", func(t *testing.T) {
		p, env := newFakeOpenCode(t)
		r := env.relocation()
		r.SourceFingerprint = env.fingerprint(t, ocSourceID)
		ref, err := p.PrepareImportedSession(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if got := env.fingerprint(t, ref.NativeSessionID); got != r.SourceFingerprint {
			t.Fatalf("copy fingerprint %q, selection %q", got, r.SourceFingerprint)
		}
	})
	t.Run("source grew before the first copy", func(t *testing.T) {
		p, env := newFakeOpenCode(t)
		r := env.relocation()
		r.SourceFingerprint = env.fingerprint(t, ocSourceID)
		env.addTurn(t, ocSourceID, "msg_1aa6c8ff0001LaterTurnInOpenC")
		if _, err := p.PrepareImportedSession(context.Background(), r); err == nil || !strings.Contains(err.Error(), "scan again") {
			t.Fatalf("grown source accepted: %v", err)
		}
		if calls := env.calls(t); len(calls) != 0 {
			t.Fatalf("grown source was exported: %q", calls)
		}
	})
	t.Run("retry after a lost checkpoint reuses the matching copy", func(t *testing.T) {
		// The copy was published, saving its identity failed, and the source
		// then gained a turn. The copy still holds exactly the archived history.
		p, env := newFakeOpenCode(t)
		r := env.relocation()
		r.SourceFingerprint = env.fingerprint(t, ocSourceID)
		ref, err := p.PrepareImportedSession(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		env.addTurn(t, ocSourceID, "msg_1aa6c8ff0001LaterTurnInOpenC")
		again, err := p.PrepareImportedSession(context.Background(), r)
		if err != nil || again != ref {
			t.Fatalf("retry = %+v, %v; want %+v", again, err, ref)
		}
	})
	t.Run("mismatched copy is rejected and kept", func(t *testing.T) {
		p, env := newFakeOpenCode(t)
		r := env.relocation()
		r.SourceFingerprint = env.fingerprint(t, ocSourceID)
		ref, err := p.PrepareImportedSession(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		env.addTurn(t, ref.NativeSessionID, "msg_1aa6c8ff0002TurnInTheCopyAOx")
		if _, err := p.PrepareImportedSession(context.Background(), r); err == nil || !strings.Contains(err.Error(), "selected history") {
			t.Fatalf("mismatched copy reused: %v", err)
		}
		if copied, found, _ := readOpenCodeCopy(context.Background(), env.configDir, ref.NativeSessionID); !found || copied.messages != 3 {
			t.Fatalf("mismatched copy was not preserved: %+v", copied)
		}
	})
}

func TestNativeSessionFingerprintCoversContent(t *testing.T) {
	for name, edit := range map[string]func(fakeOpenCodeEnv, *testing.T){
		// Same length as "KIWI": a count or length check would miss it.
		"same-length text edit": func(e fakeOpenCodeEnv, t *testing.T) {
			e.editPart(t, ocSourceID, "KIWI", `{"type":"text","text":"PEAR"}`)
		},
		"changed tool payload": func(e fakeOpenCodeEnv, t *testing.T) {
			e.editPart(t, ocSourceID, "bash", `{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"ls"},"output":"MARKER.md"}}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, env := newFakeOpenCode(t)
			r := env.relocation()
			r.SourceFingerprint = env.fingerprint(t, ocSourceID)
			edit(env, t)
			if env.fingerprint(t, ocSourceID) == r.SourceFingerprint {
				t.Fatal("edit did not change the fingerprint")
			}
			if _, err := p.PrepareImportedSession(context.Background(), r); err == nil || !strings.Contains(err.Error(), "scan again") {
				t.Fatalf("edited source accepted: %v", err)
			}
		})
	}
}

func TestNativeSessionFingerprintIgnoresCopiedIdentity(t *testing.T) {
	_, env := newFakeOpenCode(t)
	launch := t.TempDir()
	target := derivedOpenCodeID(ocSourceID, launch)
	doc, _, err := rewriteOpenCodeExport([]byte(openCodeExportFixture(env.source)), ocSourceID, target, launch)
	if err != nil {
		t.Fatal(err)
	}
	// Different ids, directory and part timestamps; same history.
	if _, err := storeOpenCodeExport(filepath.Join(env.configDir, "opencode.db"), doc, launch, 1762329999999); err != nil {
		t.Fatal(err)
	}
	if source, copied := env.fingerprint(t, ocSourceID), env.fingerprint(t, target); source != copied || !strings.HasPrefix(source, "opencode-v2:2:") {
		t.Fatalf("fingerprints %q and %q", source, copied)
	}
}
