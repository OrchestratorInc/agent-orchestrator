package sessionimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
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
	if got.fingerprint != memcode.NativeHistoryFingerprint([]byte(memcodeImportFixture)) {
		t.Fatal("fresh native read did not fingerprint the selected bytes")
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
	// Main refreshes an unprepared archive from current native history. The
	// native preparer independently refuses bytes that no longer match selection.
	if _, err := memcode.New().PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{
		NativeSessionID: rec.Metadata.AgentSessionID, ConfigDir: canonical(project),
		SourceCWD: canonical(project), LaunchCWD: t.TempDir(), TranscriptPath: path,
		SourceFingerprint: rec.Metadata.ImportSource.Fingerprint,
	}); err == nil {
		t.Fatal("changed pinned source was prepared for native resume")
	}
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

func TestMemcodeDiscoveryRefusesUndecodableHiddenBlocks(t *testing.T) {
	for _, block := range []string{`1`, `{"type":"text","text":1}`, `{"type":"tool_result","content_blocks":[1]}`, `{"type":"image","source":{"data":1}}`} {
		project := t.TempDir()
		body := `{"session_id":"sess_01234567","messages":[{"role":"user","content":[{"type":"text","text":"valid prompt"}]},{"role":"assistant","content":[` + block + `]}]}`
		path := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), body)
		if _, err := readMemcodeTranscript(context.Background(), project, path); err == nil {
			t.Fatalf("preview accepted native-incompatible hidden block %s", block)
		}
		st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
		svc := New(st, "", chatSupported)
		svc.roots = map[domain.AgentHarness]string{}
		preview, err := svc.Scan(context.Background())
		if err != nil || len(preview.Candidates) != 0 {
			t.Fatal("scan offered a native-incompatible conversation", preview, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != body {
			t.Fatal("discovery changed native history", err)
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

// Discovery returns metadata only; Scan's recency filter and cache decide
// which histories are parsed, so stale and unchanged files are never read.
func TestMemcodeRescanSkipsOldAndUnchangedHistories(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), memcodeImportFixture)
	old := writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_89abcdef", "messages.json"),
		strings.Replace(strings.Replace(memcodeImportFixture, "sess_01234567", "sess_89abcdef", 1), "2026-11-01", "2026-08-01", 1))
	stale := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	previous := harnessImporters[domain.HarnessMemcode]
	t.Cleanup(func() { harnessImporters[domain.HarnessMemcode] = previous })
	var mu sync.Mutex
	var reads []string
	counting := previous
	counting.read = func(ctx context.Context, root string, paths ...string) (transcript, error) {
		mu.Lock()
		reads = append(reads, filepath.Base(filepath.Dir(paths[0])))
		mu.Unlock()
		return previous.read(ctx, root, paths...)
	}
	harnessImporters[domain.HarnessMemcode] = counting
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{}
	svc.now = func() time.Time { return time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) }
	for range 2 {
		preview, err := svc.Scan(context.Background())
		if err != nil || len(preview.Candidates) != 1 {
			t.Fatalf("scan = %+v, %v", preview, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reads) != 1 || reads[0] != "sess_01234567" {
		t.Fatalf("memcode histories parsed = %v; want only the recent one, once", reads)
	}
}

// An unsupported installed CLI must fail on the import row, naming the
// supported version, instead of creating a session that cannot open.
func TestImportRefusesUnsupportedCLIVersionOnItsRow(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_01234567", "messages.json"), memcodeImportFixture)
	writeFile(t, filepath.Join(project, ".memcode", "sessions", "sess_89abcdef", "messages.json"), strings.Replace(memcodeImportFixture, "sess_01234567", "sess_89abcdef", 1))
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{}
	svc.now = func() time.Time { return time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) }
	const message = "memcode: native import requires verified version 1.2.3"
	var checks []domain.AgentHarness
	unsupported := errors.New(message)
	svc.SetImportVersionCheck(func(_ context.Context, harness domain.AgentHarness) error {
		checks = append(checks, harness)
		return unsupported
	})
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 2 {
		t.Fatalf("scan = %+v, %v", preview, err)
	}
	ids := []string{preview.Candidates[0].ID, preview.Candidates[1].ID}
	results, err := svc.Import(context.Background(), ids)
	if err != nil || len(results) != 2 {
		t.Fatalf("import = %+v, %v", results, err)
	}
	for _, r := range results {
		if r.Status != "failed" || r.Error != message || r.SessionID != "" {
			t.Fatalf("unsupported version row = %+v", r)
		}
	}
	if len(st.sessions) != 0 {
		t.Fatalf("unsupported version created sessions: %+v", st.sessions)
	}
	if len(checks) != 1 || checks[0] != domain.HarnessMemcode {
		t.Fatalf("version checks = %v; want one per harness per import", checks)
	}
	unsupported = nil
	results, err = svc.Import(context.Background(), ids)
	if err != nil || len(results) != 2 || results[0].Status != "created" || results[1].Status != "created" {
		t.Fatalf("supported version import = %+v, %v", results, err)
	}
}
