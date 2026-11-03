package sessionimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const memcodeImportFixture = `{"session_id":"sess_01234567","saved_at":"2026-11-01T10:00:00Z","messages":[{"role":"system","content":[{"type":"text","text":"hidden instructions"}]},{"role":"user","content":[{"type":"text","text":"make a greeting"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"private thought"},{"type":"text","text":"Hello"},{"type":"tool_use","name":"bash","input":{"command":"private command"}}]},{"role":"user","content":[{"type":"tool_result","content":"private result"}]}]}`

func TestMemcodeDiscoversOnlyResumableProjectHistory(t *testing.T) {
	project := t.TempDir()
	path := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), memcodeImportFixture)
	writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_89abcdef", "events.jsonl"), `{"kind":"user_message","text":"recall only"}`)
	groups, err := discoverMemcodeProject(context.Background(), project)
	if err != nil || len(groups) != 1 || groups[0].paths[0] != canonical(path) || groups[0].key != "sess_01234567" {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	got, err := readMemcodeTranscript(context.Background(), project, path)
	if err != nil {
		t.Fatal(err)
	}
	if groups[0].fingerprint == "" || groups[0].fingerprint != got.fingerprint {
		t.Fatalf("native snapshot was not fingerprinted: %+v", groups[0])
	}
	wantTexts(t, got.messages, "user: make a greeting", "assistant: Hello")
	if got.nativeID != groups[0].key || got.cwd != canonical(project) || got.last.Format("15:04") != "10:00" {
		t.Fatalf("native metadata = %+v", got)
	}
}

func TestMemcodeRegisteredProjectNativeTerminalImportAndRefresh(t *testing.T) {
	project, unrelated := t.TempDir(), t.TempDir()
	path := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), memcodeImportFixture)
	writeFile(t, filepath.Join(unrelated, ".memcode", "sessions", "sess_89abcdef", "messages.json"), strings.Replace(memcodeImportFixture, "sess_01234567", "sess_89abcdef", 1))
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].Harness != domain.HarnessMemcode || preview.Candidates[0].ProjectID != "repo" {
		t.Fatalf("registered native scan = %+v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("native import = %+v, %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != "sess_01234567" || rec.Metadata.ImportSource.ConfigDir != canonical(project) || !rec.NeedsImportResume() {
		t.Fatalf("native import record = %+v", rec)
	}
	writeFile(t, path, strings.Replace(memcodeImportFixture, "Hello", "Updated answer", 1))
	if rec.Metadata.ImportSource.Fingerprint == "" {
		t.Fatal("import did not persist its selected snapshot")
	}
	if err := svc.RefreshArchive(context.Background(), rec); !errors.Is(err, errSourceFingerprintChanged) {
		t.Fatalf("changed pinned source was accepted: %v", err)
	}
	// Legacy imports without selection evidence retain archive refresh behavior.
	rec.Metadata.ImportSource.Fingerprint = ""
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	wantTexts(t, st.messages, "user: make a greeting", "assistant: Updated answer")
}

func TestMemcodeImportRejectsSameSizeSourceChangeAfterSelection(t *testing.T) {
	project := t.TempDir()
	path := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), memcodeImportFixture)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %+v, %v", preview, err)
	}
	writeFile(t, path, strings.Replace(memcodeImportFixture, "Hello", "World", 1))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "failed" || len(st.sessions) != 0 {
		t.Fatalf("changed source was registered: %+v, %v", results, err)
	}
}

func TestMemcodeImportRejectsMismatchedAndMalformedWireHistory(t *testing.T) {
	for _, body := range []string{
		strings.Replace(memcodeImportFixture, "sess_01234567", "sess_89abcdef", 1),
		`{"session_id":"sess_01234567","messages":[]}`,
		`{"session_id":"sess_01234567","messages":[{"role":"user","content":"not wire blocks"}]}`,
		`{"session_id":"sess_01234567","messages":[{"role":"invalid","content":[]}]}`,
	} {
		project := t.TempDir()
		path := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), body)
		if _, err := readMemcodeTranscript(context.Background(), project, path); err == nil {
			t.Fatalf("accepted invalid native history: %s", body)
		}
	}
}

func TestMemcodeImportConfinesRepositoryHistory(t *testing.T) {
	project := t.TempDir()
	outside := writeFile(t, filepath.Join(t.TempDir(), "messages.json"), memcodeImportFixture)
	dir := filepath.Join(project, ".memcode", "sessions", "sess_01234567")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "messages.json")
	if err := os.Symlink(outside, path); err != nil {
		t.Skip(err)
	}
	if _, err := readMemcodeTranscript(context.Background(), project, path); err == nil {
		t.Fatal("read escaping transcript")
	}
	groups, err := discoverMemcodeProject(context.Background(), project)
	if err != nil || len(groups) != 0 {
		t.Fatalf("offered escaping transcript: %+v, %v", groups, err)
	}
}
