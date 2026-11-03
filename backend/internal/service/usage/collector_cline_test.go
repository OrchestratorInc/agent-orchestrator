package usage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const clineTestSessionID = "1793093930983_gfzbo"

func writeClineSessionFixture(t *testing.T, root, sessionID, cwd string) string {
	t.Helper()
	dir := filepath.Join(root, sessionID)
	writeUsageFixture(t, filepath.Join(dir, sessionID+".json"),
		`{"version":1,"session_id":"`+sessionID+`","cwd":"`+cwd+`","workspace_root":"`+cwd+`"}`)
	messages := filepath.Join(dir, sessionID+".messages.json")
	writeUsageFixture(t, messages, `{"version":1,"sessionId":"`+sessionID+`","messages":[]}`)
	return messages
}

func TestDefaultSourceRootsResolvesClineSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CLINE_SESSION_DATA_DIR", "")
	dataDir := filepath.Join(home, "ao-data")
	got, err := DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.ClineSessions != filepath.Join(home, ".cline", "data", "sessions") {
		t.Fatalf("Cline sessions = %q", got.ClineSessions)
	}
	t.Setenv("CLINE_DIR", "/opt/cline")
	got, err = DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.ClineSessions != filepath.Join("/opt/cline", "data", "sessions") {
		t.Fatalf("Cline sessions with CLINE_DIR = %q", got.ClineSessions)
	}
	t.Setenv("CLINE_DATA_DIR", "/srv/cline-data")
	got, err = DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.ClineSessions != filepath.Join("/srv/cline-data", "sessions") {
		t.Fatalf("Cline sessions with CLINE_DATA_DIR = %q", got.ClineSessions)
	}
	t.Setenv("CLINE_SESSION_DATA_DIR", "/srv/sessions")
	got, err = DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.ClineSessions != "/srv/sessions" {
		t.Fatalf("Cline sessions with CLINE_SESSION_DATA_DIR = %q", got.ClineSessions)
	}
}

func TestCollectorBindsClineSessionRunInItsWorktree(t *testing.T) {
	store := collectorTestStore(t)
	workspace := t.TempDir()
	session := collectorTestSession(t, store, domain.HarnessCline, clineTestSessionID, false)
	session.Metadata.WorkspacePath = workspace
	mustNoError(t, store.UpdateSession(context.Background(), session))
	root := t.TempDir()
	messages := writeClineSessionFixture(t, root, clineTestSessionID, workspace)
	collector := NewCollector(store, SourceRoots{ClineSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessCline, Event: "session-start", NativeSessionID: clineTestSessionID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceClineSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, messages) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

// TestCollectorRejectsClineSessionFromAnotherWorktree catches #7478: with two
// Cline sessions on one hub, the second session's hooks report its native ID
// under the first AO session. The native ID alone would bill the second
// session's usage to the first.
func TestCollectorRejectsClineSessionFromAnotherWorktree(t *testing.T) {
	store := collectorTestStore(t)
	workspace := t.TempDir()
	session := collectorTestSession(t, store, domain.HarnessCline, "1793093930000_first", false)
	session.Metadata.WorkspacePath = workspace
	mustNoError(t, store.UpdateSession(context.Background(), session))
	root := t.TempDir()
	const otherID = "1793340719019_wylge"
	writeClineSessionFixture(t, root, otherID, t.TempDir())
	collector := NewCollector(store, SourceRoots{ClineSessions: root}, nil)

	_ = collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessCline, Event: "session-start", NativeSessionID: otherID,
	})
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	mustNoError(t, err)
	for _, binding := range bindings {
		sources, err := store.ListUsageSourcesForBinding(context.Background(), binding.ID)
		mustNoError(t, err)
		if len(sources) != 0 {
			t.Fatalf("registered another worktree's Cline session: %+v", sources)
		}
	}
}
