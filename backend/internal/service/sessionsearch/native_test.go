package sessionsearch_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
)

// writeTranscript writes a JSONL transcript under root and returns its path.
func writeTranscript(t *testing.T, root, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o750); err != nil {
		t.Fatalf("mkdir transcript root: %v", err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func claudeLine(t *testing.T, kind, role, text, timestamp string) string {
	t.Helper()
	return `{"type":"` + kind + `","message":{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]},"timestamp":"` + timestamp + `"}`
}

func seedTUIProject(t *testing.T, store interface {
	UpsertProject(context.Context, domain.ProjectRecord) error
}, projectID string) {
	t.Helper()
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: projectID, Path: "/tmp/" + projectID, RegisteredAt: testNow,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func TestNativeClaudeTranscriptIsIndexedAndReadOnly(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Claude names the project directory after the workspace path.
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	path := writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "the checkout button is misaligned", "2026-08-09T12:00:00Z"),
		claudeLine(t, "assistant", "assistant", "I moved the button into the flex row", "2026-08-09T12:00:05Z"),
		`{"type":"mode","mode":"normal"}`,
	)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}

	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	results := search(t, f.svc, "misaligned")
	if len(results.Results) != 1 {
		t.Fatalf("results = %+v, want the user line", results.Results)
	}
	if results.Results[0].Kind != domain.SessionSearchKindUser {
		t.Fatalf("kind = %q, want user", results.Results[0].Kind)
	}
	if results.Results[0].MessageID != "" {
		t.Fatalf("native result carries a Chat message id %q; it must open the session only", results.Results[0].MessageID)
	}
	if got := search(t, f.svc, "flex"); len(got.Results) != 1 {
		t.Fatalf("assistant line missing: %+v", got.Results)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat transcript after index: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("transcript changed: size %d->%d mtime %v->%v",
			before.Size(), after.Size(), before.ModTime(), after.ModTime())
	}
}

func TestNativeTranscriptIndexesOnlyNewBytes(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	path := writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "first direction", "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "first"); len(got.Results) != 1 {
		t.Fatalf("first pass = %+v", got.Results)
	}

	// Appending must be picked up, and the earlier document must survive.
	appended := claudeLine(t, "assistant", "assistant", "second answer about the parser", "2026-08-09T12:01:00Z")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := file.WriteString(appended + "\n"); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	_ = file.Close()

	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "first"); len(got.Results) != 1 {
		t.Fatalf("earlier document lost after append: %+v", got.Results)
	}
	if got := search(t, f.svc, "parser"); len(got.Results) != 1 {
		t.Fatalf("appended document missing: %+v", got.Results)
	}
}

// A transcript rewritten from the start (a fresh file at the same path) must
// reset the cursor rather than read from a stale offset.
func TestNativeTranscriptResetsWhenTruncated(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "obsolete wording", "2026-08-09T12:00:00Z"),
		claudeLine(t, "assistant", "assistant", "padding so the file is long enough to truncate past", "2026-08-09T12:00:05Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "obsolete"); len(got.Results) != 1 {
		t.Fatalf("first pass = %+v", got.Results)
	}

	writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "replacement wording", "2026-08-09T13:00:00Z"),
	)
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "obsolete"); len(got.Results) != 0 {
		t.Fatalf("truncated transcript text still searchable: %+v", got.Results)
	}
	if got := search(t, f.svc, "replacement"); len(got.Results) != 1 {
		t.Fatalf("replacement text missing: %+v", got.Results)
	}
}

func TestNativeCodexRolloutIsIndexed(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-codex",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	writeTranscript(t, root, filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+string(session.ID)+".jsonl"),
		`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"`+string(session.ID)+`"}}`,
		`{"timestamp":"2026-08-09T12:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"why does the reaper leak goroutines"}]}}}`,
		`{"timestamp":"2026-08-09T12:00:02Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"it never closes the ticker"}]}}}`,
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "goroutines"); len(got.Results) != 1 {
		t.Fatalf("codex user message missing: %+v", got.Results)
	}
	if got := search(t, f.svc, "ticker"); len(got.Results) != 1 {
		t.Fatalf("codex agent message missing: %+v", got.Results)
	}
}

// A path outside the configured provider root must never be read, even when a
// transcript file happens to carry the session id.
func TestNativeTranscriptOutsideRootIsIgnored(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: outside},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	writeTranscript(t, outside, string(session.ID)+".jsonl",
		claudeLine(t, "user", "user", "secret outside text", "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "secret"); len(got.Results) != 0 {
		t.Fatalf("read a transcript outside the configured root: %+v", got.Results)
	}
}

func TestSweepSkipsUnchangedSources(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "unchanged content", "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	written, err := f.svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if written != 0 {
		t.Fatalf("unchanged sweep wrote %d documents, want 0", written)
	}
	// The already-indexed document is still there.
	if got := search(t, f.svc, "unchanged"); len(got.Results) != 1 {
		t.Fatalf("document lost after no-op sweep: %+v", got.Results)
	}
}

func TestNativeTranscriptWithPartialTrailingLineIsDeferred(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	path := writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "complete line", "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	// A half-written record must not be indexed as final.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := file.WriteString(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"half wri`); err != nil {
		t.Fatalf("append partial line: %v", err)
	}
	_ = file.Close()
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "writt"); len(got.Results) != 0 {
		t.Fatalf("partial line indexed: %+v", got.Results)
	}
	if got := search(t, f.svc, "complete"); len(got.Results) != 1 {
		t.Fatalf("complete line lost: %+v", got.Results)
	}

	// Completing the line makes it visible on the next pass.
	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("reopen transcript: %v", err)
	}
	if _, err := file.WriteString(`tten now"}]},"timestamp":"2026-08-09T12:00:10Z"}` + "\n"); err != nil {
		t.Fatalf("complete line: %v", err)
	}
	_ = file.Close()
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep after completion: %v", err)
	}
	if got := search(t, f.svc, "half written"); len(got.Results) != 1 {
		t.Fatalf("completed line missing: %+v", got.Results)
	}
}

func TestSidechainAndMetaRecordsAreSkipped(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":[{"type":"text","text":"subagent chatter"}]},"timestamp":"2026-08-09T12:00:00Z"}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"caveat metadata"}]},"timestamp":"2026-08-09T12:00:01Z"}`,
		claudeLine(t, "user", "user", "the real prompt", "2026-08-09T12:00:02Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "chatter"); len(got.Results) != 0 {
		t.Fatalf("sidechain indexed: %+v", got.Results)
	}
	if got := search(t, f.svc, "caveat"); len(got.Results) != 0 {
		t.Fatalf("meta record indexed: %+v", got.Results)
	}
	if got := search(t, f.svc, "prompt"); len(got.Results) != 1 {
		t.Fatalf("real prompt missing: %+v", got.Results)
	}
}

func TestUnreadableTranscriptMarksSourceUnavailableWithoutDroppingDocs(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	ctx := context.Background()
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-native",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{Branch: "feat/native", WorkspacePath: "/tmp/proj"},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := strings.ReplaceAll("/tmp/proj", "/", "-")
	path := writeTranscript(t, root, filepath.Join(key, string(session.ID)+".jsonl"),
		claudeLine(t, "user", "user", "kept content", "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "kept"); len(got.Results) != 1 {
		t.Fatalf("first pass = %+v", got.Results)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "kept"); len(got.Results) != 1 {
		t.Fatalf("missing transcript dropped indexed documents: %+v", got.Results)
	}
}

func TestSearchResponseCarriesCoverageAndNames(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "coverage probe")
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	results := search(t, f.svc, "coverage")
	if len(results.Results) != 1 {
		t.Fatalf("results = %+v", results.Results)
	}
	if results.Coverage.State == "" {
		t.Fatal("coverage state is empty")
	}
	if results.Coverage.IndexedDocs == 0 {
		t.Fatal("coverage reports no indexed documents after indexing")
	}
	if results.Limit == 0 {
		t.Fatal("response does not report its page limit")
	}
	_ = time.Now
}
