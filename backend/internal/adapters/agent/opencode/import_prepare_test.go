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
		var doc struct {
			Info struct {
				ID string `json:"id"`
			} `json:"info"`
			Messages []struct {
				Info struct {
					ID string `json:"id"`
				} `json:"info"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		cwd, _ := os.Getwd()
		if dir := os.Getenv("AO_FAKE_OPENCODE_BIND"); dir != "" {
			cwd = dir
		}
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			return 1
		}
		defer func() { _ = db.Close() }()
		if _, err := db.Exec(`INSERT INTO session (id, directory) VALUES (?, ?)`, doc.Info.ID, cwd); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, m := range doc.Messages {
			if _, err := db.Exec(`INSERT INTO message (id, session_id) VALUES (?, ?)`, m.Info.ID, doc.Info.ID); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		fmt.Printf("Imported session: %s\n", doc.Info.ID)
		return 0
	}
	return 2
}

const (
	ocSourceID = "ses_e55937592ffeaNXsUampvachsM"
	ocMsg1     = "msg_1aa6c8acd001mZuCzEhM7Ci74n"
	ocMsg2     = "msg_1aa6c8b0e002Q8d0rT4kLmNpQs"
	ocPart1    = "prt_1aa6c8acd002aBcDeFgHiJkLmN"
	ocPart2    = "prt_1aa6c8b0e003ZyXwVuTsRqPoNm"
)

func openCodeExportFixture(sourceDir string) string {
	return `{"info":{"id":"` + ocSourceID + `","slug":"cosmic-otter","projectID":"842266f1","directory":"` + sourceDir + `",` +
		`"title":"Exact KIWI reply","version":"1.19.3","time":{"created":1762324000000,"updated":1762324100000},"revert":{"messageID":"` + ocMsg2 + `"}},` +
		`"messages":[` +
		`{"info":{"id":"` + ocMsg1 + `","sessionID":"` + ocSourceID + `","role":"user","time":{"created":1762324000001}},` +
		`"parts":[{"id":"` + ocPart1 + `","sessionID":"` + ocSourceID + `","messageID":"` + ocMsg1 + `","type":"text","text":"Reply with KIWI"}]},` +
		`{"info":{"id":"` + ocMsg2 + `","sessionID":"` + ocSourceID + `","role":"assistant","parentID":"` + ocMsg1 + `","path":{"cwd":"` + sourceDir + `","root":"` + sourceDir + `"},"cost":0.000123456789,"tokens":{"input":12345678901}},` +
		`"parts":[{"id":"` + ocPart2 + `","sessionID":"` + ocSourceID + `","messageID":"` + ocMsg2 + `","type":"text","text":"KIWI"}]}` +
		`]}`
}

type fakeOpenCodeEnv struct {
	configDir, launch, source, log, export string
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
	}
	for _, dir := range []string{env.configDir, env.launch, env.source} {
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
		`CREATE TABLE message (id text PRIMARY KEY, session_id text)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	if err := os.WriteFile(env.export, []byte(openCodeExportFixture(env.source)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_FAKE_OPENCODE_DB", dbPath)
	t.Setenv("AO_FAKE_OPENCODE_EXPORT", env.export)
	t.Setenv("AO_FAKE_OPENCODE_LOG", env.log)
	t.Setenv("AO_FAKE_OPENCODE_BIND", "")
	return &Plugin{resolvedBinary: exe}, env
}

func (e fakeOpenCodeEnv) relocation() ports.ImportedSessionRelocation {
	return ports.ImportedSessionRelocation{NativeSessionID: ocSourceID, ConfigDir: e.configDir, SourceCWD: e.source, LaunchCWD: e.launch}
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
	if _, found, _ := readOpenCodeCopy(context.Background(), env.configDir, ocSourceID); found {
		t.Fatal("source session must not be written")
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
