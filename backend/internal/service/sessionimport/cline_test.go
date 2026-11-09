package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const clineTestID = "1790000000000_synthetic"

func clineFixture(t *testing.T, root, cwd, messages string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", clineTestID)
	meta, err := json.Marshal(map[string]string{"session_id": clineTestID, "cwd": cwd, "started_at": "2026-10-01T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, clineTestID+".json"), string(meta))
	return writeFile(t, filepath.Join(dir, clineTestID+".messages.json"), messages)
}

func TestClineDiscoveryReadsOnlyConversationText(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := clineFixture(t, root, cwd, `{
  "version":1, "sessionId":"`+clineTestID+`", "system_prompt":"private instructions",
  "messages":[
    {"role":"system","content":"private"},
    {"role":"user","content":[{"type":"text","text":"<environment_details>context</environment_details>"}]},
    {"role":"user","content":[{"type":"text","text":"build a greeting"}],"ts":1790848800000},
    {"role":"tool","content":"private tool output"},
    {"role":"assistant","content":[{"type":"thinking","text":"hidden"},{"type":"text","text":"Hello"},{"type":"tool_use","input":{"text":"hidden"}}],"ts":1790848860000}
  ]
}`)
	groups, err := discoverCline(context.Background(), root)
	if err != nil || len(groups) != 1 || groups[0].nativeID() != clineTestID || len(groups[0].paths) != 2 {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	for _, paths := range [][]string{groups[0].paths, {path}} {
		got, err := readTranscript(context.Background(), domain.HarnessCline, root, paths...)
		if err != nil {
			t.Fatal(err)
		}
		wantTexts(t, got.messages, "user: build a greeting", "assistant: Hello")
		if got.cwd != cwd || got.nativeID != clineTestID || got.title != "build a greeting" {
			t.Fatalf("transcript = %+v", got)
		}
	}
}

func TestClineDropsInjectedContextBlocksFromTypedTurn(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := clineFixture(t, root, cwd, `{
  "version":1, "sessionId":"`+clineTestID+`",
  "messages":[
    {"role":"user","content":[{"type":"text","text":"build a greeting"},{"type":"text","text":"<environment_details>secret cwd listing</environment_details>"}],"ts":1790848800000},
    {"role":"assistant","content":[{"type":"text","text":"Hello"}],"ts":1790848860000}
  ]
}`)
	got, err := readTranscript(context.Background(), domain.HarnessCline, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: build a greeting", "assistant: Hello")
	if got.title != "build a greeting" {
		t.Fatalf("title = %q", got.title)
	}
}

func TestClineOversizedAndTornDocument(t *testing.T) {
	oldRecord, oldString := maxRecordBytes, maxSkimString
	maxRecordBytes, maxSkimString = 512, 128
	t.Cleanup(func() { maxRecordBytes, maxSkimString = oldRecord, oldString })
	root := t.TempDir()
	path := clineFixture(t, root, t.TempDir(), `{"sessionId":"`+clineTestID+`","messages":[
{"role":"user","content":"keep this prompt"},
{"role":"assistant","content":[{"type":"tool_use","input":{"text":"`+strings.Repeat("x", 4096)+`"}},{"type":"text","text":"visible reply"}]},
{"role":"assistant","content":"torn`)
	got, err := readClineTranscript(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: keep this prompt", "assistant: visible reply")
}

func TestClineRejectsIdentityMismatchAndEscapingTranscript(t *testing.T) {
	root := t.TempDir()
	path := clineFixture(t, root, t.TempDir(), `{"sessionId":"wrong","messages":[{"role":"user","content":"hello"}]}`)
	if _, err := readClineTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted messages identity mismatch")
	}
	outside := writeFile(t, filepath.Join(t.TempDir(), "messages.json"), `{"sessionId":"`+clineTestID+`","messages":[{"role":"user","content":"hello"}]}`)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if _, err := readClineTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted transcript outside provider root")
	}
}

func TestClineTerminalImportAndFirstOpenRefresh(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := clineFixture(t, root, cwd, `{"sessionId":"`+clineTestID+`","messages":[{"role":"user","content":"first"}]}`)
	store := &importStore{}
	svc := New(store, "", nil)
	svc.home = canonical(cwd)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessCline: root}
	svc.now = func() time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) }
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %+v, %v", results, err)
	}
	rec := store.sessions[0]
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != clineTestID || !rec.NeedsImportResume() {
		t.Fatalf("record = %+v", rec)
	}
	writeFile(t, path, `{"sessionId":"`+clineTestID+`","messages":[{"role":"user","content":"first"},{"role":"assistant","content":"later"}]}`)
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	wantTexts(t, store.messages, "user: first", "assistant: later")
}

func TestClineRootOverrides(t *testing.T) {
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CLINE_DIR", "")
	if got := clineRoot("/home/example"); got != filepath.Join("/home/example", ".cline", "data") {
		t.Fatal(got)
	}
	t.Setenv("CLINE_DIR", "/config/cline")
	if got := clineRoot("/home/example"); got != filepath.Join("/config/cline", "data") {
		t.Fatal(got)
	}
	t.Setenv("CLINE_DATA_DIR", "/state/cline")
	if got := clineRoot("/home/example"); got != "/state/cline" {
		t.Fatal(got)
	}
}
