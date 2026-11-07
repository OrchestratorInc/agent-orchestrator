package sessionsearch_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
