package sessionsearch_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
)

// A terminal session's transcript is named by the provider's own session id,
// which AO records as agent_session_id, never by the AO session id. These
// fixtures give every session an AO id that differs from its native id; the
// others in this package name files after the AO id, which is how searching
// by the wrong id went unseen.
const nativeUUID = "8f3c2a1e-5b7d-4c9e-a1f0-2d6b8e4c7a90"

func seedNativeTUI(t *testing.T, store interface {
	CreateSession(context.Context, domain.SessionRecord) (domain.SessionRecord, error)
}, harness domain.AgentHarness, nativeID, transcriptPath string) domain.SessionRecord {
	t.Helper()
	session, err := store.CreateSession(context.Background(), domain.SessionRecord{
		ID:        "sess-native-identity",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   harness,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata: domain.SessionMetadata{
			Branch: "feat/native", WorkspacePath: "/tmp/proj",
			AgentSessionID: nativeID, NativeTranscriptPath: transcriptPath,
		},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if string(session.ID) == nativeID {
		t.Fatalf("fixture must give the AO session an id other than its native id")
	}
	return session
}

func TestClaudeTranscriptIsFoundByItsNativeSessionID(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	seedTUIProject(t, f.store, "proj")
	seedNativeTUI(t, f.store, domain.HarnessClaudeCode, nativeUUID, "")
	// Claude names the file by its own session id, in the workspace's project directory.
	writeTranscript(t, root, filepath.Join(strings.ReplaceAll("/tmp/proj", "/", "-"), nativeUUID+".jsonl"),
		claudeLine(t, "user", "user", "the zzNativeNeedle test flakes on CI", "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzNativeNeedle"); len(got.Results) != 1 {
		t.Fatalf("results = %+v, want the transcript named by the native id", got.Results)
	}
}

func TestCodexRolloutIsFoundByItsNativeSessionID(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	seedTUIProject(t, f.store, "proj")
	seedNativeTUI(t, f.store, domain.HarnessCodex, nativeUUID, "")
	writeTranscript(t, root, filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+nativeUUID+".jsonl"),
		`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"`+nativeUUID+`"}}`,
		`{"timestamp":"2026-08-09T12:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","npm test"],"aggregated_output":"error zzRolloutNeedle","exit_code":1,"status":"completed"}}}`,
	)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzRolloutNeedle"); len(got.Results) != 1 {
		t.Fatalf("results = %+v, want the rollout named by the native id", got.Results)
	}
}

// An AO-managed account keeps its own Claude config directory under AO's state
// directory, outside ~/.claude/projects. The path the provider's hooks reported
// is what finds that transcript.
func TestRecordedTranscriptUnderAnAOManagedAccountIsRead(t *testing.T) {
	claudeRoot, stateDir := t.TempDir(), t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: claudeRoot, AOStateDir: stateDir})
	seedTUIProject(t, f.store, "proj")
	accountProjects := filepath.Join(stateDir, "agent-accounts", "claude", "acct-1", "projects")
	path := writeTranscript(t, accountProjects, filepath.Join("-tmp-proj", nativeUUID+".jsonl"),
		claudeLine(t, "user", "user", "a zzAccountNeedle under the managed account", "2026-08-09T12:00:00Z"),
	)
	seedNativeTUI(t, f.store, domain.HarnessClaudeCode, nativeUUID, path)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzAccountNeedle"); len(got.Results) != 1 {
		t.Fatalf("results = %+v, want the recorded transcript", got.Results)
	}
}

// A recorded path is a value in the database, not a licence to read anywhere:
// outside the provider roots and AO's state directory it is ignored, and the
// session falls back to its native id under the provider root.
func TestRecordedTranscriptOutsideTheRootsIsNotRead(t *testing.T) {
	claudeRoot, stateDir, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: claudeRoot, AOStateDir: stateDir})
	seedTUIProject(t, f.store, "proj")
	outside := writeTranscript(t, elsewhere, nativeUUID+".jsonl",
		claudeLine(t, "user", "user", "zzOutsideNeedle must never be read", "2026-08-09T12:00:00Z"),
	)
	writeTranscript(t, claudeRoot, filepath.Join("-tmp-proj", nativeUUID+".jsonl"),
		claudeLine(t, "user", "user", "zzInsideNeedle is the real transcript", "2026-08-09T12:00:00Z"),
	)
	seedNativeTUI(t, f.store, domain.HarnessClaudeCode, nativeUUID, outside)
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzOutsideNeedle"); len(got.Results) != 0 {
		t.Fatalf("read a recorded path outside the roots: %+v", got.Results)
	}
	if got := search(t, f.svc, "zzInsideNeedle"); len(got.Results) != 1 {
		t.Fatalf("results = %+v, want the transcript under the provider root", got.Results)
	}
}

// Found in review: a continuation keeps the AO session id but starts a new
// native session and transcript. With the old transcript still on disk, the
// cached path won over the new one and the new conversation never became
// searchable. Both ways the change shows up are covered: a newly recorded
// path, and a new native id with no recorded path.
func TestANewNativeSessionReplacesTheCachedTranscript(t *testing.T) {
	const nextUUID = "1d2e3f40-5a6b-4c7d-8e9f-a0b1c2d3e4f5"
	for _, recordPath := range []bool{true, false} {
		t.Run(map[bool]string{true: "recorded path", false: "native id only"}[recordPath], func(t *testing.T) {
			// Resolved, as ~/.claude/projects is: macOS's temp dir is under the
			// /var symlink, and a cached path (stored resolved) would never match
			// an unresolved root, hiding the reuse this test is about.
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatalf("resolve temp dir: %v", err)
			}
			f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
			ctx := context.Background()
			seedTUIProject(t, f.store, "proj")
			first := writeTranscript(t, root, filepath.Join("-tmp-proj", nativeUUID+".jsonl"),
				claudeLine(t, "user", "user", "zzFirstTranscript before the continuation", "2026-08-09T12:00:00Z"),
			)
			recorded := ""
			if recordPath {
				recorded = first
			}
			session := seedNativeTUI(t, f.store, domain.HarnessClaudeCode, nativeUUID, recorded)
			if _, err := f.svc.IndexAll(ctx); err != nil {
				t.Fatalf("IndexAll: %v", err)
			}
			if len(search(t, f.svc, "zzFirstTranscript").Results) != 1 {
				t.Fatalf("the first transcript was not indexed")
			}

			// The continuation: a new native session and transcript; the old file stays.
			next := writeTranscript(t, root, filepath.Join("-tmp-proj", nextUUID+".jsonl"),
				claudeLine(t, "user", "user", "zzNextTranscript after the continuation", "2026-08-09T13:00:00Z"),
			)
			record, found, err := f.store.GetSession(ctx, session.ID)
			if err != nil || !found {
				t.Fatalf("GetSession: found=%v err=%v", found, err)
			}
			record.Metadata.AgentSessionID = nextUUID
			record.Metadata.NativeTranscriptPath = ""
			if recordPath {
				record.Metadata.NativeTranscriptPath = next
			}
			if err := f.store.UpdateSession(ctx, record); err != nil {
				t.Fatalf("UpdateSession: %v", err)
			}
			if _, err := f.svc.Sweep(ctx); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if got := search(t, f.svc, "zzNextTranscript").Results; len(got) != 1 || got[0].SessionID != string(session.ID) {
				t.Fatalf("results = %s, want the new transcript under the same session", describe(got))
			}
		})
	}
}

// Found in review: a transcript that stops growing was skipped before its
// title was rebuilt, so renaming an idle or ended session left the old name
// searchable and the new one unfindable.
func TestRenamedNativeSessionIsFoundByItsNewName(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	seedTUIProject(t, f.store, "proj")
	session := seedNativeTUI(t, f.store, domain.HarnessClaudeCode, nativeUUID, "")
	if _, err := f.store.RenameSession(ctx, session.ID, "Zzoldname investigation", testNow); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	writeTranscript(t, root, filepath.Join(strings.ReplaceAll("/tmp/proj", "/", "-"), nativeUUID+".jsonl"),
		claudeLine(t, "user", "user", "look at the cache", "2026-08-09T12:00:00Z"),
	)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if _, err := f.store.RenameSession(ctx, session.ID, "Zznewname investigation", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zznewname").Results; len(got) != 1 || got[0].SessionID != string(session.ID) {
		t.Fatalf("the new name is not searchable: %s", describe(got))
	}
	if got := search(t, f.svc, "zzoldname").Results; len(got) != 0 {
		t.Fatalf("the old name is still searchable: %s", describe(got))
	}
}

// Found in review: OpenCode sessions were matched on their directory alone, so
// two AO sessions in one workspace both indexed the newest provider session.
// Each reads the provider session it recorded.
func TestOpenCodeSessionsSharingADirectoryReadTheirOwnHistory(t *testing.T) {
	ctx := context.Background()
	dataHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataHome, "opencode"), 0o750); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataHome, "opencode", "opencode.db"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT NOT NULL, directory TEXT,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			type TEXT NOT NULL, seq INTEGER NOT NULL, time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO session VALUES ('oc-older', 'Older', '/tmp/proj', 1, 1), ('oc-newer', 'Newer', '/tmp/proj', 2, 2)`,
		`INSERT INTO session_message VALUES
			('m1', 'oc-older', 'user', 1, 1, 1, '{"text":"zzalpha only in the older session"}'),
			('m2', 'oc-newer', 'user', 1, 2, 2, '{"text":"zzbravo only in the newer session"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed opencode: %v", err)
		}
	}
	_ = db.Close()

	f := newFixture(t, sessionsearch.TranscriptRoots{OpenCodeData: dataHome})
	seedTUIProject(t, f.store, "proj")
	ids := map[string]string{}
	for _, native := range []string{"oc-older", "oc-newer"} {
		session, err := f.store.CreateSession(ctx, domain.SessionRecord{
			ID: domain.SessionID("sess-" + native), ProjectID: "proj", Kind: domain.KindWorker,
			Harness: domain.HarnessOpenCode, Mode: domain.SessionModeTUI,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
			Metadata:  domain.SessionMetadata{WorkspacePath: "/tmp/proj", AgentSessionID: native},
			CreatedAt: testNow, UpdatedAt: testNow,
		})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		ids[native] = string(session.ID)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	for word, native := range map[string]string{"zzalpha": "oc-older", "zzbravo": "oc-newer"} {
		got := search(t, f.svc, word).Results
		if len(got) != 1 || got[0].SessionID != ids[native] {
			t.Fatalf("%s = %s, want only %s", word, describe(got), ids[native])
		}
	}
}

// Found in review: a cached miss with an unchanged identity skipped discovery
// for good, so a transcript removed and then restored in place, with nothing
// about the session changed, never became searchable again.
func TestTranscriptRestoredInPlaceIsIndexedAgain(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	seedTUIProject(t, f.store, "proj")
	seedNativeTUI(t, f.store, domain.HarnessClaudeCode, nativeUUID, "")
	name := filepath.Join(strings.ReplaceAll("/tmp/proj", "/", "-"), nativeUUID+".jsonl")
	first := claudeLine(t, "user", "user", "look at the cache", "2026-08-09T12:00:00Z")
	writeTranscript(t, root, name, first)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	writeTranscript(t, root, name, first, claudeLine(t, "assistant", "assistant", "the zzRestoredNeedle is back", "2026-08-09T12:01:00Z"))
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzRestoredNeedle").Results; len(got) != 1 {
		t.Fatalf("restored transcript text = %s, want it indexed", describe(got))
	}
}

// A transcript created after a recorded miss is found once the miss is old
// enough to look again; until then the sweep does not re-glob the tree.
func TestTranscriptCreatedAfterAMissIsFoundOnRecheck(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	base := newFixture(t, sessionsearch.TranscriptRoots{ClaudeProjects: root})
	now := testNow
	svc := sessionsearch.New(sessionsearch.Options{
		Store: base.store, Roots: sessionsearch.TranscriptRoots{ClaudeProjects: root}, Namer: base.store,
		Now: func() time.Time { return now },
	})
	seedTUIProject(t, base.store, "proj")
	seedNativeTUI(t, base.store, domain.HarnessClaudeCode, nativeUUID, "")
	if _, err := svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	writeTranscript(t, root, filepath.Join(strings.ReplaceAll("/tmp/proj", "/", "-"), nativeUUID+".jsonl"),
		claudeLine(t, "user", "user", "the zzLateNeedle appears", "2026-08-09T12:05:00Z"))
	if _, err := svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, svc, "zzLateNeedle").Results; len(got) != 0 {
		t.Fatalf("a recent miss was searched again: %s", describe(got))
	}
	now = now.Add(10 * time.Minute)
	if _, err := svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, svc, "zzLateNeedle").Results; len(got) != 1 {
		t.Fatalf("transcript created after the miss = %s, want it found on recheck", describe(got))
	}
}

// Found in review: the OpenCode cursor was kept when the resolved provider
// session changed, so a recorded id replacing a directory match read the new
// session from the old one's position and kept the old one's history.
func TestOpenCodeSourceRestartsWhenItsProviderSessionChanges(t *testing.T) {
	ctx := context.Background()
	dataHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataHome, "opencode"), 0o750); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataHome, "opencode", "opencode.db"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer func() { _ = db.Close() }()
	exec := func(statement string) {
		t.Helper()
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed opencode: %v", err)
		}
	}
	exec(`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT NOT NULL, directory TEXT,
		time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`)
	exec(`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
		type TEXT NOT NULL, seq INTEGER NOT NULL, time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL, data TEXT NOT NULL)`)
	// Session A is the only one in the directory, so the AO session, whose own
	// provider id OpenCode does not have yet, falls back to it.
	exec(`INSERT INTO session VALUES ('oc-a', 'A', '/tmp/proj', 1, 1)`)
	exec(`INSERT INTO session_message VALUES ('a1', 'oc-a', 'user', 1, 1, 1, '{"text":"zzalpha from session a"}')`)

	f := newFixture(t, sessionsearch.TranscriptRoots{OpenCodeData: dataHome})
	seedTUIProject(t, f.store, "proj")
	session, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID: "sess-opencode-b", ProjectID: "proj", Kind: domain.KindWorker,
		Harness: domain.HarnessOpenCode, Mode: domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{WorkspacePath: "/tmp/proj", AgentSessionID: "oc-b"},
		CreatedAt: testNow, UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzalpha").Results; len(got) != 1 {
		t.Fatalf("directory fallback = %s, want session a indexed", describe(got))
	}

	// The session's own provider session appears; its first message is at the
	// position session a was read to.
	exec(`INSERT INTO session VALUES ('oc-b', 'B', '/tmp/proj', 2, 2)`)
	exec(`INSERT INTO session_message VALUES ('b1', 'oc-b', 'user', 1, 2, 2, '{"text":"zzbravo from session b"}')`)
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzbravo").Results; len(got) != 1 || got[0].SessionID != string(session.ID) {
		t.Fatalf("session b = %s, want it read from the start", describe(got))
	}
	if got := search(t, f.svc, "zzalpha").Results; len(got) != 0 {
		t.Fatalf("session a's history is still indexed under this session: %s", describe(got))
	}
}

// Found in review: a legacy rollout whose item stream starts past the head
// the format probe reads was read on with the legacy parser, which skips item
// records, and the cursor moved past them for good.
func TestCodexItemStreamStartingPastTheProbeIsIndexed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	seedTUIProject(t, f.store, "proj")
	seedNativeTUI(t, f.store, domain.HarnessCodex, nativeUUID, "")
	name := filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+nativeUUID+".jsonl")
	lines := []string{`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"` + nativeUUID + `"}}`}
	// More than the probe's 256 KiB of legacy response_item records.
	filler := strings.Repeat("legacy context ", 70)
	lines = append(lines, `{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the zzLegacyNeedle flake"}]}}`)
	for range 320 {
		lines = append(lines, `{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"`+filler+`"}]}}`)
	}
	writeTranscript(t, root, name, lines...)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	// The turn in which the item stream starts writes its user message in both
	// streams, the response_item first, as Codex does.
	lines = append(lines,
		`{"timestamp":"2026-08-09T12:05:00Z","type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-08-09T12:05:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"run the zzTurnNeedle suite"}]}}`,
		`{"timestamp":"2026-08-09T12:05:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"run the zzTurnNeedle suite"}]}}}`,
		`{"timestamp":"2026-08-09T12:05:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","npm test"],"aggregated_output":"error zzLateItemNeedle","exit_code":1,"status":"completed"}}}`,
	)
	writeTranscript(t, root, name, lines...)
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzLateItemNeedle").Results; len(got) != 1 {
		t.Fatalf("item record past the probe = %s, want it indexed", describe(got))
	}
	// The legacy-only messages from before the item stream stay searchable.
	if got := search(t, f.svc, "zzLegacyNeedle").Results; len(got) != 1 {
		t.Fatalf("legacy message before the item stream = %s, want it still indexed", describe(got))
	}
	// The message written in both streams is indexed once.
	if got := search(t, f.svc, "zzTurnNeedle").Results; len(got) != 1 || got[0].SessionMatches != 1 {
		t.Fatalf("message in both streams = %s, want one match", describe(got))
	}
}

// Found in review: the coverage note listed a harness found only on a deleted
// session while another unsupported source remained.
func TestCoverageNoteSkipsHarnessesOfDeletedSessions(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	seedTUIProject(t, f.store, "proj")
	gone := seedTUI(t, f.store, "proj", "sess-aider", domain.HarnessAider)
	seedTUI(t, f.store, "proj", "sess-gemini", domain.HarnessGemini)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	gone.IsTerminated = true
	if err := f.store.UpdateSession(ctx, gone); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if deleted, err := f.store.DeleteTerminatedSession(ctx, gone.ID); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	coverage, err := f.svc.Coverage(ctx)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if strings.Join(coverage.UnindexedHarnesses, ",") != "gemini" {
		t.Fatalf("unindexed harnesses = %v, want only the kept session's", coverage.UnindexedHarnesses)
	}
}

// Found in review: the item-stream checks matched the text "item_completed",
// so a legacy message that only mentioned it switched the rollout to the item
// stream and every legacy message stopped being searchable.
func TestCodexMessageMentioningAnItemRecordStaysLegacy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	seedTUIProject(t, f.store, "proj")
	seedNativeTUI(t, f.store, domain.HarnessCodex, nativeUUID, "")
	name := filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+nativeUUID+".jsonl")
	message := func(role, text string) string {
		kind := "output_text"
		if role == "user" {
			kind = "input_text"
		}
		return `{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"` + role +
			`","content":[{"type":"` + kind + `","text":"` + text + `"}]}}`
	}
	lines := []string{
		`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"` + nativeUUID + `"}}`,
		message("user", "why is zzHeadNeedle slow"),
		// In the head the format probe reads.
		message("assistant", "item_completed"),
	}
	writeTranscript(t, root, name, lines...)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	// Appended to the indexed legacy rollout, where the unread part is scanned.
	lines = append(lines, message("assistant", "item_completed"), message("user", "and zzTailNeedle too"))
	writeTranscript(t, root, name, lines...)
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for _, word := range []string{"zzHeadNeedle", "zzTailNeedle"} {
		if got := search(t, f.svc, word).Results; len(got) != 1 {
			t.Fatalf("%s = %s, want the legacy message still indexed", word, describe(got))
		}
	}
}

// A source an earlier reader switched to the item stream on a message's text
// is read again, so its legacy messages come back.
func TestCodexSourceFalselySwitchedByAnEarlierReaderIsReadAgain(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, sessionsearch.TranscriptRoots{CodexSessions: root})
	seedTUIProject(t, f.store, "proj")
	session := seedNativeTUI(t, f.store, domain.HarnessCodex, nativeUUID, "")
	path := writeTranscript(t, root, filepath.Join("2026", "08", "09", "rollout-2026-08-09T12-00-00-"+nativeUUID+".jsonl"),
		`{"timestamp":"2026-08-09T12:00:00Z","type":"session_meta","payload":{"id":"`+nativeUUID+`"}}`,
		`{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why is zzRecoveredNeedle slow"}]}}`,
		`{"timestamp":"2026-08-09T12:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"item_completed"}]}}`,
	)
	// The indexer records the path with symlinks resolved (the temp dir is one on macOS).
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve transcript path: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat transcript: %v", err)
	}
	// What the earlier reader left: an item-stream source read to the end, with
	// none of the legacy messages indexed.
	sourceKey := sessionsearch.NativeSourceKey(domain.HarnessCodex, string(session.ID))
	if err := f.store.UpsertSessionSearchSource(ctx, domain.SessionSearchSource{
		SourceKey: sourceKey, Kind: domain.SessionSearchSourceNative, SessionID: string(session.ID),
		ProjectID: "proj", Harness: string(domain.HarnessCodex), Path: path,
		CursorValue: info.Size(), FileSize: info.Size(), Fingerprint: "native-v5+items",
		State: domain.SessionSearchSourceActive, UpdatedAt: testNow,
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzRecoveredNeedle").Results; len(got) != 1 {
		t.Fatalf("legacy message of a falsely switched source = %s, want it indexed again", describe(got))
	}
}

// Found in review: OpenCode updates a row in place while a reply is written,
// keeping its seq, so a row read early was never read again once finished.
func TestOpenCodeRowUpdatedInPlaceIsIndexedAgain(t *testing.T) {
	ctx := context.Background()
	dataHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataHome, "opencode"), 0o750); err != nil {
		t.Fatalf("mkdir opencode dir: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataHome, "opencode", "opencode.db"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer func() { _ = db.Close() }()
	exec := func(statement string) {
		t.Helper()
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed opencode: %v", err)
		}
	}
	exec(`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT NOT NULL, directory TEXT,
		time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`)
	exec(`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
		type TEXT NOT NULL, seq INTEGER NOT NULL, time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL, data TEXT NOT NULL)`)
	exec(`INSERT INTO session VALUES ('oc-live', 'Live', '/tmp/proj', 1, 1)`)
	exec(`INSERT INTO session_message VALUES ('m1', 'oc-live', 'user', 1, 1, 1, '{"text":"why is the cache cold"}')`)
	exec(`INSERT INTO session_message VALUES ('m2', 'oc-live', 'assistant', 2, 2, 2, '{"text":"zzPartialNeedle so far"}')`)
	// A long reply whose second chunk holds a word, with a tool part, and a row
	// that will end up producing nothing.
	long := strings.Repeat("filler words for the first chunk ", 300) + " zzSecondChunkNeedle"
	exec(`INSERT INTO session_message VALUES ('m3', 'oc-live', 'assistant', 3, 3, 3, '{"text":"` + long +
		`","content":[{"type":"tool","name":"bash","state":{"input":{"command":"grep zzToolNeedle"},"content":"no match"}}]}')`)
	exec(`INSERT INTO session_message VALUES ('m4', 'oc-live', 'assistant', 4, 4, 4, '{"text":"zzVanishingNeedle"}')`)

	f := newFixture(t, sessionsearch.TranscriptRoots{OpenCodeData: dataHome})
	seedTUIProject(t, f.store, "proj")
	if _, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID: "sess-opencode-live", ProjectID: "proj", Kind: domain.KindWorker,
		Harness: domain.HarnessOpenCode, Mode: domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		Metadata:  domain.SessionMetadata{WorkspacePath: "/tmp/proj", AgentSessionID: "oc-live"},
		CreatedAt: testNow, UpdatedAt: testNow,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "zzSecondChunkNeedle").Results; len(got) != 1 {
		t.Fatalf("second chunk before the rewrite = %s, want it indexed", describe(got))
	}
	// The reply finishes: the same row, its seq unchanged. The long reply is
	// rewritten short without its tool part, and the last row empties.
	exec(`UPDATE session_message SET data = '{"text":"the zzFinishedNeedle answer"}', time_updated = 5 WHERE id = 'm2'`)
	exec(`UPDATE session_message SET data = '{"text":"a short reply"}', time_updated = 6 WHERE id = 'm3'`)
	exec(`UPDATE session_message SET data = '{"text":""}', time_updated = 7 WHERE id = 'm4'`)
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzFinishedNeedle").Results; len(got) != 1 {
		t.Fatalf("finished reply = %s, want it indexed", describe(got))
	}
	for _, gone := range []string{"zzPartialNeedle", "zzSecondChunkNeedle", "zzToolNeedle", "zzVanishingNeedle"} {
		if got := search(t, f.svc, gone).Results; len(got) != 0 {
			t.Fatalf("%s = %s, want text the rewritten rows no longer have gone", gone, describe(got))
		}
	}
	if got := search(t, f.svc, "short reply").Results; len(got) != 1 {
		t.Fatalf("rewritten long reply = %s, want its new text", describe(got))
	}
	// Update times are whole numbers: a row can change again within the latest
	// one seen (7), and that change must be read too.
	exec(`UPDATE session_message SET data = '{"text":"zzSameTickNeedle"}' WHERE id = 'm4'`)
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzSameTickNeedle").Results; len(got) != 1 {
		t.Fatalf("change within the latest update time = %s, want it indexed", describe(got))
	}
}
