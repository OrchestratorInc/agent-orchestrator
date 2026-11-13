package usage

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	ompTestNativeID    = "01a048bb-551d-7000-8b7e-1a07961198e0"
	kimchiTestNativeID = "019ffc6e-fcb3-7f68-a162-85693427d059"
	ompTestTitleSlot   = `{"type":"title","v":1,"title":"Fix the flaky watcher test","source":"auto","updatedAt":"2026-08-28T14:17:13.741Z","pad":"          "}` + "\n"
)

func piFamilyTestHeader(nativeID string) string {
	return `{"type":"session","version":3,"id":"` + nativeID + `","timestamp":"2026-08-28T14:17:11.709Z","cwd":"/repo"}` + "\n"
}

func TestDefaultSourceRootsResolvesOMPAndKimchiAgentDirs(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "ao-data")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("KIMCHI_CODING_AGENT_DIR", "")

	got, err := DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.OMPSessions != filepath.Join(home, ".omp", "agent", "sessions") ||
		got.KimchiSessions != filepath.Join(home, ".config", "kimchi", "harness", "sessions") {
		t.Fatalf("default roots = OMP %q, Kimchi %q", got.OMPSessions, got.KimchiSessions)
	}

	ompAgent := filepath.Join(t.TempDir(), "omp-agent")
	t.Setenv("PI_CODING_AGENT_DIR", ompAgent)
	t.Setenv("KIMCHI_CODING_AGENT_DIR", "~/kimchi-agent")
	got, err = DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.OMPSessions != filepath.Join(ompAgent, "sessions") ||
		got.KimchiSessions != filepath.Join(home, "kimchi-agent", "sessions") ||
		got.PiSessions != filepath.Join(dataDir, "pi", "sessions") {
		t.Fatalf("overridden roots = %+v", got)
	}

	t.Setenv("KIMCHI_CODING_AGENT_DIR", filepath.Join("relative", "agent"))
	got, err = DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.KimchiSessions != filepath.Join(home, ".config", "kimchi", "harness", "sessions") {
		t.Fatalf("relative Kimchi override root = %q", got.KimchiSessions)
	}
}

func TestSupportedHarnessIncludesPiForks(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessOMP, domain.HarnessKimchi, domain.HarnessPrimeAgent} {
		if !SupportedHarness(harness) {
			t.Fatalf("SupportedHarness(%q) = false", harness)
		}
	}
}

func TestCollectorDiscoversPrimeAgentSessionInSessionDir(t *testing.T) {
	const nativeID = "01a0ae4c-6550-718c-86e5-6e83f11144a6"
	dataDir := t.TempDir()
	roots, err := DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if roots.PrimeAgentSessions != filepath.Join(dataDir, "agent-runtime", "prime-agent", "sessions") {
		t.Fatalf("prime-agent root = %q", roots.PrimeAgentSessions)
	}
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessPrimeAgent, nativeID, false)
	path := filepath.Join(roots.PrimeAgentSessions, nativeID+".jsonl")
	writeUsageFixture(t, path, piFamilyTestHeader(nativeID))
	collector := NewCollector(store, SourceRoots{PrimeAgentSessions: roots.PrimeAgentSessions}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessPrimeAgent, Event: "session-start", NativeSessionID: nativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourcePrimeAgentSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestCollectorBindsPrimeAgentTranscriptByWorkspace(t *testing.T) {
	const nativeID = "01a1a5fd-b108-772d-b336-2f25529fe6fb"
	started := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	header := func(at time.Time, cwd string) string {
		return `{"type":"session","version":3,"id":"` + nativeID + `","timestamp":"` +
			at.Format(time.RFC3339Nano) + `","cwd":"` + cwd + `"}` + "\n"
	}
	tests := []struct {
		name        string
		transcript  time.Time
		sessions    int
		wantBinding bool
	}{
		{name: "one session in the cwd", transcript: started.Add(2 * time.Second), sessions: 1, wantBinding: true},
		{name: "transcript older than the session", transcript: started.Add(-time.Hour), sessions: 1},
		{name: "two sessions share the cwd", transcript: started.Add(2 * time.Second), sessions: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := collectorTestStore(t)
			workspace := t.TempDir()
			var sessions []domain.SessionRecord
			for range tt.sessions {
				session, err := store.CreateSession(ctx, domain.SessionRecord{
					ProjectID: "usage-test", Kind: domain.KindWorker, Harness: domain.HarnessPrimeAgent,
					Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: started},
					Metadata:  domain.SessionMetadata{WorkspacePath: workspace},
					CreatedAt: started, UpdatedAt: started,
				})
				mustNoError(t, err)
				sessions = append(sessions, session)
			}
			root := t.TempDir()
			path := filepath.Join(root, nativeID+".jsonl")
			writeUsageFixture(t, path, header(tt.transcript, workspace))
			collector := NewCollector(store, SourceRoots{PrimeAgentSessions: root}, nil)

			mustNoError(t, collector.BackfillActive(ctx))
			bindings, err := store.ListUsageBindingsForSession(ctx, sessions[0].ID)
			mustNoError(t, err)
			if !tt.wantBinding {
				if len(bindings) != 0 {
					t.Fatalf("bindings = %+v, want none", bindings)
				}
				return
			}
			if len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
				t.Fatalf("bindings = %+v", bindings)
			}
			sources, err := store.ListUsageSourcesForBinding(ctx, bindings[0].ID)
			if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourcePrimeAgentSession ||
				sources[0].ArtifactPath != canonicalUsagePath(t, path) {
				t.Fatalf("sources = %+v, err=%v", sources, err)
			}
		})
	}
}

func TestCollectorBindsDeepSeekSessionLogs(t *testing.T) {
	const nativeID = "bbeef2fa-f748-4ed4-bd69-73f91f71f8dc"
	ctx := context.Background()
	started := time.Now().UTC().Add(-time.Minute)
	header := func(cwd string) string {
		return `{"type":"session","version":4,"id":"` + nativeID + `","createdAt":` +
			strconv.FormatInt(started.Add(2*time.Second).UnixMilli(), 10) + `,"cwd":"` + cwd + `"}` + "\n"
	}
	writeLog := func(t *testing.T, path, content string) {
		t.Helper()
		encoder, err := zstd.NewWriter(nil)
		mustNoError(t, err)
		writeUsageFixture(t, path, string(encoder.EncodeAll([]byte(content), nil)))
	}
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, domain.SessionModeChat} {
		t.Run(string(mode), func(t *testing.T) {
			store := collectorTestStore(t)
			workspace := t.TempDir()
			metadata := domain.SessionMetadata{WorkspacePath: workspace}
			if mode == domain.SessionModeChat {
				metadata = domain.SessionMetadata{ProviderConversationID: nativeID}
			}
			session, err := store.CreateSession(ctx, domain.SessionRecord{
				ProjectID: "usage-test", Kind: domain.KindWorker, Harness: domain.HarnessDeepSeek, Mode: mode,
				Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: started},
				Metadata: metadata, CreatedAt: started, UpdatedAt: started,
			})
			mustNoError(t, err)
			root := t.TempDir()
			dir := filepath.Join(root, "--repo--", nativeID)
			path := filepath.Join(dir, "session.v4.jsonl.zstd")
			writeLog(t, path, header(workspace))
			writeLog(t, filepath.Join(dir, "session.jsonl.zstd"), header(workspace))
			collector := NewCollector(store, SourceRoots{DeepSeekSessions: root}, nil)

			mustNoError(t, collector.BackfillActive(ctx))
			bindings, err := store.ListUsageBindingsForSession(ctx, session.ID)
			if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
				t.Fatalf("bindings = %+v, err=%v", bindings, err)
			}
			sources, err := store.ListUsageSourcesForBinding(ctx, bindings[0].ID)
			if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceDeepSeekSession ||
				sources[0].ArtifactPath != canonicalUsagePath(t, path) {
				t.Fatalf("sources = %+v, err=%v", sources, err)
			}
		})
	}
}

func TestCollectorDiscoversOMPSessionBehindTitleSlot(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessOMP, ompTestNativeID, false)
	root := t.TempDir()
	path := filepath.Join(root, "-repo", "2026-08-28T14-17-11-709Z_"+ompTestNativeID+".jsonl")
	writeUsageFixture(t, path, ompTestTitleSlot+piFamilyTestHeader(ompTestNativeID))
	collector := NewCollector(store, SourceRoots{OMPSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessOMP, Event: "session-start", NativeSessionID: ompTestNativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].Harness != domain.HarnessOMP {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceOMPSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestCollectorPiStillRequiresHeaderOnFirstLine(t *testing.T) {
	const nativeID = "pi-behind-title"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessPi, nativeID, false)
	root := t.TempDir()
	writeUsageFixture(t, filepath.Join(root, "project", "session.jsonl"), ompTestTitleSlot+piFamilyTestHeader(nativeID))
	collector := NewCollector(store, SourceRoots{PiSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessPi, Event: "session-start", NativeSessionID: nativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 0 {
		t.Fatalf("Pi bound a transcript whose first record is not its header: %+v err=%v", sources, err)
	}
}

func TestCollectorDiscoversKimchiSessionsInNestedAndFlatLayouts(t *testing.T) {
	const flatID = "01a17cc6-65b8-7704-a3a5-2d821b9c973c"
	store := collectorTestStore(t)
	nested := collectorTestSession(t, store, domain.HarnessKimchi, kimchiTestNativeID, false)
	flat := collectorTestSession(t, store, domain.HarnessKimchi, flatID, false)
	root := t.TempDir()
	nestedPath := filepath.Join(root, "--repo--", "2026-08-13T18-42-39-923Z_"+kimchiTestNativeID+".jsonl")
	flatPath := filepath.Join(root, "2026-10-27T09-52-19-385Z_"+flatID+".jsonl")
	writeUsageFixture(t, nestedPath, piFamilyTestHeader(kimchiTestNativeID))
	writeUsageFixture(t, flatPath, piFamilyTestHeader(flatID))
	collector := NewCollector(store, SourceRoots{KimchiSessions: root}, nil)

	for _, test := range []struct {
		session domain.SessionRecord
		id      string
		path    string
	}{
		{session: nested, id: kimchiTestNativeID, path: nestedPath},
		{session: flat, id: flatID, path: flatPath},
	} {
		mustNoError(t, collector.RecordHook(context.Background(), test.session.ID, HookSignal{
			Harness: domain.HarnessKimchi, Event: "session-start", NativeSessionID: test.id,
		}))
		bindings, err := store.ListUsageBindingsForSession(context.Background(), test.session.ID)
		if err != nil || len(bindings) != 1 {
			t.Fatalf("%s bindings=%+v err=%v", test.id, bindings, err)
		}
		sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
		if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceKimchiSession ||
			sources[0].ArtifactPath != canonicalUsagePath(t, test.path) {
			t.Fatalf("%s sources=%+v err=%v", test.id, sources, err)
		}
	}
}

func TestCollectorKeepsPiFamilyRootsApart(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessOMP, ompTestNativeID, false)
	ompRoot, kimchiRoot := t.TempDir(), t.TempDir()
	foreign := filepath.Join(kimchiRoot, "--repo--", "2026-08-28T14-17-11-709Z_"+ompTestNativeID+".jsonl")
	writeUsageFixture(t, foreign, piFamilyTestHeader(ompTestNativeID))
	collector := NewCollector(store, SourceRoots{OMPSessions: ompRoot, KimchiSessions: kimchiRoot}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessOMP, Event: "session-start", NativeSessionID: ompTestNativeID,
	}))
	mustNoError(t, collector.ReconcilePath(context.Background(), foreign))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].Harness != domain.HarnessOMP {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 0 {
		t.Fatalf("OMP bound Kimchi's transcript: %+v err=%v", sources, err)
	}
}

func TestCollectorReconcilesKimchiTranscriptCreatedAfterHook(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessKimchi, kimchiTestNativeID, false)
	root := t.TempDir()
	collector := NewCollector(store, SourceRoots{KimchiSessions: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessKimchi, Event: "session-start", NativeSessionID: kimchiTestNativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].LastErrorCode != domain.UsageErrorSourceDiscoveryPending {
		t.Fatalf("binding before transcript = %+v, err=%v", bindings, err)
	}
	path := filepath.Join(root, "--repo--", "2026-08-13T18-42-39-923Z_"+kimchiTestNativeID+".jsonl")
	writeUsageFixture(t, path, piFamilyTestHeader(kimchiTestNativeID))

	mustNoError(t, collector.ReconcileSources(context.Background(), 8))
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceKimchiSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources after late transcript = %+v, err=%v", sources, err)
	}
}

func TestCollectorBackfillActiveBindsOMPSessionByHeaderID(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessOMP, ompTestNativeID, false)
	root := t.TempDir()
	path := filepath.Join(root, "-repo", "2026-08-28T14-17-11-709Z_"+ompTestNativeID+".jsonl")
	writeUsageFixture(t, path, ompTestTitleSlot+piFamilyTestHeader(ompTestNativeID))
	collector := NewCollector(store, SourceRoots{OMPSessions: root}, nil)

	mustNoError(t, collector.BackfillActive(context.Background()))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != ompTestNativeID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceOMPSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestReadPiFamilySessionMetaRejectsOMPTitleSlotWithoutHeader(t *testing.T) {
	omp, _ := piFamilyForHarness(domain.HarnessOMP)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeUsageFixture(t, path, ompTestTitleSlot+ompTestTitleSlot)
	if _, ok := readPiFamilySessionMeta(context.Background(), path, omp); ok {
		t.Fatal("two title slots were read as a session header")
	}
	writeUsageFixture(t, path, ompTestTitleSlot+piFamilyTestHeader(ompTestNativeID))
	if meta, ok := readPiFamilySessionMeta(context.Background(), path, omp); !ok || meta.ID != ompTestNativeID {
		t.Fatalf("meta=%+v ok=%v", meta, ok)
	}
}
