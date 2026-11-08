package usage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type countingPiSessionStore struct {
	collectorStore
	listCalls int
}

func (s *countingPiSessionStore) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	s.listCalls++
	return s.collectorStore.ListAllSessions(ctx)
}

func TestDefaultSourceRootsIncludesPiSessions(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "ao-data")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")

	got, err := DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.PiSessions != filepath.Join(dataDir, "pi", "sessions") {
		t.Fatalf("Pi sessions = %q, want %q", got.PiSessions, filepath.Join(dataDir, "pi", "sessions"))
	}
	if want := filepath.Join(home, ".pi", "agent", "sessions"); got.PiAgentSessions != want {
		t.Fatalf("Pi agent sessions = %q, want %q", got.PiAgentSessions, want)
	}
}

func TestCollectorBindsPiTranscriptsInPiAgentFolder(t *testing.T) {
	ctx := context.Background()
	store := collectorTestStore(t)
	agentRoot := t.TempDir()
	collector := NewCollector(store, SourceRoots{PiSessions: t.TempDir(), PiAgentSessions: agentRoot}, nil)
	assertSource := func(sessionID domain.SessionID, path string) {
		t.Helper()
		bindings, err := store.ListUsageBindingsForSession(ctx, sessionID)
		if err != nil || len(bindings) != 1 {
			t.Fatalf("bindings=%+v err=%v", bindings, err)
		}
		sources, err := store.ListUsageSourcesForBinding(ctx, bindings[0].ID)
		if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourcePiSession ||
			sources[0].ArtifactPath != canonicalUsagePath(t, path) {
			t.Fatalf("sources=%+v err=%v", sources, err)
		}
	}

	const chatID = "pi-chat-session"
	chat := collectorTestChatSession(t, store, domain.HarnessPi, chatID, false)
	chatPath := filepath.Join(agentRoot, "--repo--", "2026-11-06T10-00-00-000Z_"+chatID+".jsonl")
	writeUsageFixture(t, chatPath, `{"type":"session","id":"`+chatID+`","cwd":"/repo","version":3}`+"\n")
	mustNoError(t, collector.ReconcilePath(ctx, chatPath))
	assertSource(chat.ID, chatPath)

	const restoredID = "pi-restored-session"
	restored := collectorTestSession(t, store, domain.HarnessPi, restoredID, false)
	restoredPath := filepath.Join(agentRoot, "--repo--", "2026-11-06T10-05-00-000Z_"+restoredID+".jsonl")
	writeUsageFixture(t, restoredPath, `{"type":"session","id":"`+restoredID+`","cwd":"/repo","version":3}`+"\n")
	mustNoError(t, collector.RecordHook(ctx, restored.ID, HookSignal{
		Harness: domain.HarnessPi, Event: "session-start", NativeSessionID: restoredID, TranscriptPath: restoredPath,
	}))
	assertSource(restored.ID, restoredPath)
}

func TestCollectorDiscoversPiSessionByHeaderID(t *testing.T) {
	const nativeID = "pi-session-1"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessPi, nativeID, false)
	root := t.TempDir()
	path := filepath.Join(root, "project", "session.jsonl")
	writeUsageFixture(t, path, `{"type":"session","id":"`+nativeID+`","cwd":"/repo","version":3}`+"\n")
	collector := NewCollector(store, SourceRoots{PiSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessPi, Event: "session-start", NativeSessionID: nativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourcePiSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestCollectorDiscoversPiSessionInSessionDirRoot(t *testing.T) {
	const nativeID = "pi-flat-session"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessPi, nativeID, false)
	root := t.TempDir()
	path := filepath.Join(root, "2026-11-04T02-54-10-307Z_"+nativeID+".jsonl")
	writeUsageFixture(t, path, `{"type":"session","id":"`+nativeID+`","cwd":"/repo","version":3}`+"\n")
	collector := NewCollector(store, SourceRoots{PiSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessPi, Event: "session-start", NativeSessionID: nativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].State != domain.UsageBindingActive {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestCollectorReconcilesPiTranscriptCreatedAfterHook(t *testing.T) {
	const nativeID = "pi-late-session"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessPi, nativeID, false)
	root := t.TempDir()
	collector := NewCollector(store, SourceRoots{PiSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessPi, Event: "session-start", NativeSessionID: nativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].LastErrorCode != domain.UsageErrorSourceDiscoveryPending {
		t.Fatalf("binding before transcript = %+v, err=%v", bindings, err)
	}
	path := filepath.Join(root, "project", "session.jsonl")
	writeUsageFixture(t, path, `{"type":"session","id":"`+nativeID+`","cwd":"/repo","version":3}`+"\n")

	mustNoError(t, collector.ReconcileSources(context.Background(), 8))
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourcePiSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources after late transcript = %+v, err=%v", sources, err)
	}
}

func TestCollectorReconcilePiPathMatchesDurableNativeID(t *testing.T) {
	const nativeID = "pi-session-from-header"
	store := collectorTestStore(t)
	workspace := t.TempDir()
	session := collectorTestSession(t, store, domain.HarnessPi, nativeID, false)
	session.Metadata.WorkspacePath = workspace
	mustNoError(t, store.UpdateSession(context.Background(), session))
	root := t.TempDir()
	path := filepath.Join(root, "project", "session.jsonl")
	writeUsageFixture(t, path, `{"type":"session","id":"`+nativeID+`","cwd":"`+workspace+`","version":3}`+"\n")

	collector := NewCollector(store, SourceRoots{PiSessions: root}, nil)
	mustNoError(t, collector.ReconcilePath(context.Background(), path))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
}

func TestCollectorPiBackfillRejectsUnrelatedSameWorkspaceSession(t *testing.T) {
	store := collectorTestStore(t)
	workspace := t.TempDir()
	session := collectorTestSession(t, store, domain.HarnessPi, "pi-current", false)
	session.Metadata.WorkspacePath = workspace
	mustNoError(t, store.UpdateSession(context.Background(), session))

	root := t.TempDir()
	currentPath := filepath.Join(root, "project", "current.jsonl")
	manualPath := filepath.Join(root, "project", "manual.jsonl")
	writeUsageFixture(t, currentPath, `{"type":"session","id":"pi-current","cwd":"`+workspace+`","version":3}`+"\n")
	writeUsageFixture(t, manualPath, `{"type":"session","id":"pi-manual","cwd":"`+workspace+`","version":3}`+"\n")

	collector := NewCollector(store, SourceRoots{PiSessions: root}, nil)
	mustNoError(t, collector.ReconcilePath(context.Background(), manualPath))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("manual same-workspace path created bindings=%+v err=%v", bindings, err)
	}
	mustNoError(t, collector.BackfillActive(context.Background()))
	bindings, err = store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != "pi-current" {
		t.Fatalf("bindings=%+v err=%v, want only current Pi session", bindings, err)
	}
}

func TestReadPiSessionMetaHonorsCanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeUsageFixture(t, path, `{"type":"session","id":"pi-context","timestamp":"2026-08-24T10:00:00Z","cwd":"/repo","version":3}`+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, ok := readPiFamilySessionMeta(ctx, path, piFamilyHarnesses[0]); ok {
		t.Fatal("canceled Pi metadata read succeeded")
	}
}

func TestCollectorReconcilePiPathReturnsValidationAndCancellationErrors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "invalid.txt")
	writeUsageFixture(t, path, `{"type":"session","id":"pi-session","cwd":"/repo"}`+"\n")
	collector := NewCollector(collectorTestStore(t), SourceRoots{PiSessions: root}, nil)
	if err := collector.ReconcilePath(context.Background(), path); err == nil {
		t.Fatal("invalid Pi transcript path was silently accepted")
	}
	if err := collector.ReconcilePath(context.Background(), filepath.Join(root, "missing.jsonl")); err == nil {
		t.Fatal("missing Pi transcript path was silently accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := collector.ReconcilePath(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reconcile error = %v", err)
	}
}

func TestCollectorPiReconcileListsSessionsOnce(t *testing.T) {
	baseStore := collectorTestStore(t)
	store := &countingPiSessionStore{collectorStore: baseStore}
	root := t.TempDir()
	for _, id := range []string{"pi-one", "pi-two"} {
		collectorTestSession(t, baseStore, domain.HarnessPi, id, false)
		path := filepath.Join(root, id+".jsonl")
		writeUsageFixture(t, path, `{"type":"session","id":"`+id+`","cwd":"/repo","version":3}`+"\n")
	}
	collector := NewCollector(store, SourceRoots{PiSessions: root}, nil)
	mustNoError(t, collector.ReconcileSources(context.Background(), -1))
	if store.listCalls != 1 {
		t.Fatalf("session listings = %d, want 1", store.listCalls)
	}
}
