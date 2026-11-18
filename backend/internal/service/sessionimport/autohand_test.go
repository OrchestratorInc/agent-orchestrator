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

const autohandTestID = "11111111-2222-4333-8444-555555555555-1790000000000"

func autohandFixture(t *testing.T, root, id, messages string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", id)
	metadata, err := json.Marshal(map[string]string{"sessionId": id, "projectPath": filepath.Join(root, "project"), "model": "example/model", "createdAt": "2026-10-01T10:00:00Z", "lastActiveAt": "2026-10-01T10:01:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "metadata.json"), string(metadata))
	return writeFile(t, filepath.Join(dir, "conversation.jsonl"), messages)
}

func TestAutohandDiscoveryAndRefresh(t *testing.T) {
	root := t.TempDir()
	path := autohandFixture(t, root, autohandTestID, `{"role":"system","content":"private instructions"}
{"role":"user","content":"<environment_context>noise</environment_context>"}
{"role":"user","content":"build a greeting","timestamp":"2026-10-01T10:00:00Z"}
{"role":"tool","content":"secret tool output"}
{"role":"assistant","content":"Hello","timestamp":"2026-10-01T10:02:00Z"}
{"role":`)
	groups, err := discoverAutohand(context.Background(), root)
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %+v, err = %v", groups, err)
	}
	if groups[0].nativeID() != autohandTestID || len(groups[0].paths) != 2 {
		t.Fatalf("group = %+v", groups[0])
	}
	for _, paths := range [][]string{groups[0].paths, {path}} {
		got, err := readTranscript(context.Background(), domain.HarnessAutohand, root, paths...)
		if err != nil {
			t.Fatal(err)
		}
		wantTexts(t, got.messages, "user: build a greeting", "assistant: Hello")
		if got.cwd != filepath.Join(root, "project") || got.title != "build a greeting" || got.nativeID != autohandTestID || got.last.Format("15:04") != "10:02" {
			t.Fatalf("transcript = %+v", got)
		}
	}
}

func TestAutohandRejectsMismatchedMetadataAndEscapingLink(t *testing.T) {
	root := t.TempDir()
	path := autohandFixture(t, root, autohandTestID, `{"role":"user","content":"hello"}`)
	meta := filepath.Join(filepath.Dir(path), "metadata.json")
	original, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, meta, strings.Replace(string(original), autohandTestID, "other", 1))
	if _, err := readAutohandTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted mismatched identity")
	}
	outside := writeFile(t, filepath.Join(t.TempDir(), "metadata.json"), string(original))
	if err := os.Remove(meta); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, meta); err != nil {
		t.Skip(err)
	}
	if _, err := readAutohandTranscript(context.Background(), root, path); err == nil {
		t.Fatal("accepted metadata outside provider root")
	}
}

func TestAutohandOversizedToolRecordDoesNotHidePrompt(t *testing.T) {
	oldRecord, oldString := maxRecordBytes, maxSkimString
	maxRecordBytes, maxSkimString = 256, 128
	t.Cleanup(func() { maxRecordBytes, maxSkimString = oldRecord, oldString })
	root := t.TempDir()
	path := autohandFixture(t, root, autohandTestID, `{"role":"tool","content":"`+strings.Repeat("x", 2048)+`"}`+"\n"+`{"role":"user","content":"keep this prompt"}`)
	got, err := readAutohandTranscript(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: keep this prompt")
}

func TestAutohandRootAndCanceledDiscovery(t *testing.T) {
	t.Setenv("AUTOHAND_HOME", "")
	if got := autohandRoot("/home/example"); got != filepath.Join("/home/example", ".autohand") {
		t.Fatal(got)
	}
	t.Setenv("AUTOHAND_HOME", "/state/autohand")
	if got := autohandRoot("/home/example"); got != "/state/autohand" {
		t.Fatal(got)
	}
	root := t.TempDir()
	autohandFixture(t, root, autohandTestID, `{"role":"user","content":"hello"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverAutohand(ctx, root); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestAutohandImportsTerminalAndRefreshesSidecar(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := autohandFixture(t, root, autohandTestID, `{"role":"user","content":"first prompt"}`)
	meta, err := json.Marshal(map[string]string{
		"sessionId": autohandTestID, "projectPath": cwd,
		"createdAt": "2026-10-01T10:00:00Z", "lastActiveAt": "2026-10-01T10:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(filepath.Dir(path), "metadata.json"), string(meta))
	store := &importStore{}
	svc := New(store, "", nil)
	svc.home = canonical(cwd)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessAutohand: root}
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
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != autohandTestID || !rec.NeedsImportResume() {
		t.Fatalf("record = %+v", rec)
	}
	writeFile(t, path, `{"role":"user","content":"first prompt"}`+"\n"+`{"role":"assistant","content":"new reply"}`)
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	wantTexts(t, store.messages, "user: first prompt", "assistant: new reply")
}

func TestAutohandUndatedActivityUsesFileTimeWithoutReplacingNativeTime(t *testing.T) {
	native := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	modified := native.Add(time.Hour)
	for _, dated := range []bool{false, true} {
		root := t.TempDir()
		path := autohandFixture(t, root, autohandTestID, `{"role":"user","content":"undated turn"}`)
		metadata := map[string]string{"sessionId": autohandTestID, "projectPath": filepath.Join(root, "project")}
		want := modified
		if dated {
			metadata["createdAt"] = native.Format(time.RFC3339)
			want = native
		}
		data, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(filepath.Dir(path), "metadata.json"), string(data))
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		got, err := readAutohandTranscript(context.Background(), root, path)
		if err != nil || !got.last.Equal(want) || len(got.messages) != 1 || !got.messages[0].At.Equal(want) {
			t.Fatalf("dated=%v transcript=%+v err=%v, want activity %v", dated, got, err, want)
		}
	}
}
