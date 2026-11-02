package usage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const geminiTestSessionID = "5f0c2a8e-1b2c-4d3e-9f40-6a7b8c9d0e1f"

func geminiMetadataLine(sessionID, kind string) string {
	line := `{"sessionId":"` + sessionID + `","projectHash":"abc","startTime":"2026-11-03T10:00:00Z","lastUpdated":"2026-11-03T10:00:00Z"`
	if kind != "" {
		line += `,"kind":"` + kind + `"`
	}
	return line + "}\n"
}

func TestDefaultSourceRootsHonorsGeminiCLIHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_CLI_HOME", "")
	got, err := DefaultSourceRoots(context.Background(), filepath.Join(home, "ao-data"))
	mustNoError(t, err)
	if got.GeminiTmp != filepath.Join(home, ".gemini", "tmp") {
		t.Fatalf("Gemini tmp = %q", got.GeminiTmp)
	}

	custom := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", custom)
	got, err = DefaultSourceRoots(context.Background(), filepath.Join(home, "ao-data"))
	mustNoError(t, err)
	if got.GeminiTmp != filepath.Join(custom, ".gemini", "tmp") {
		t.Fatalf("Gemini tmp with GEMINI_CLI_HOME = %q", got.GeminiTmp)
	}
}

// TestCollectorDiscoversGeminiChatByMetadataSessionID catches binding a chat
// log by its file name alone: the name carries only the first eight
// characters of the session ID, which another session can share.
func TestCollectorDiscoversGeminiChatByMetadataSessionID(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessGemini, geminiTestSessionID, false)
	root := t.TempDir()
	chats := filepath.Join(root, "project", "chats")
	want := filepath.Join(chats, "session-2026-11-03T10-00-5f0c2a8e.jsonl")
	writeUsageFixture(t, want, geminiMetadataLine(geminiTestSessionID, "main"))
	writeUsageFixture(t, filepath.Join(chats, "session-2026-11-03T10-05-5f0c2a8e.jsonl"),
		geminiMetadataLine("5f0c2a8e-0000-4000-8000-000000000000", ""))
	collector := NewCollector(store, SourceRoots{GeminiTmp: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessGemini, Event: "session-start", NativeSessionID: geminiTestSessionID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != geminiTestSessionID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceGeminiSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, want) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

// TestCollectorRejectsGeminiTranscriptOfAnotherSession catches trusting a
// hook's transcript path without reading which session the log belongs to.
func TestCollectorRejectsGeminiTranscriptOfAnotherSession(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessGemini, geminiTestSessionID, false)
	root := t.TempDir()
	collector := NewCollector(store, SourceRoots{GeminiTmp: root}, nil)
	for name, tc := range map[string]struct {
		path string
		line string
	}{
		"other session": {
			path: filepath.Join(root, "project", "chats", "session-2026-11-03T10-00-aaaaaaaa.jsonl"),
			line: geminiMetadataLine("aaaaaaaa-0000-4000-8000-000000000000", ""),
		},
		"subagent log": {
			path: filepath.Join(root, "project", "chats", geminiTestSessionID, geminiTestSessionID+".jsonl"),
			line: geminiMetadataLine(geminiTestSessionID, "subagent"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			writeUsageFixture(t, tc.path, tc.line)
			err := collector.RecordHook(context.Background(), session.ID, HookSignal{
				Harness: domain.HarnessGemini, Event: "session-start",
				NativeSessionID: geminiTestSessionID, TranscriptPath: tc.path,
			})
			bindings, listErr := store.ListUsageBindingsForSession(context.Background(), session.ID)
			mustNoError(t, listErr)
			for _, binding := range bindings {
				sources, sourceErr := store.ListUsageSourcesForBinding(context.Background(), binding.ID)
				mustNoError(t, sourceErr)
				if len(sources) != 0 {
					t.Fatalf("registered %+v (hook err %v)", sources, err)
				}
			}
		})
	}
}

func TestCollectorReconcilesGeminiChatCreatedAfterHook(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessGemini, geminiTestSessionID, false)
	root := t.TempDir()
	collector := NewCollector(store, SourceRoots{GeminiTmp: root}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessGemini, Event: "session-start", NativeSessionID: geminiTestSessionID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].LastErrorCode != domain.UsageErrorSourceDiscoveryPending {
		t.Fatalf("binding before transcript = %+v, err=%v", bindings, err)
	}
	path := filepath.Join(root, "project", "chats", "session-2026-11-03T10-00-5f0c2a8e.jsonl")
	writeUsageFixture(t, path, geminiMetadataLine(geminiTestSessionID, ""))

	mustNoError(t, collector.ReconcileSources(context.Background(), 8))
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceGeminiSession ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) {
		t.Fatalf("sources after late transcript = %+v, err=%v", sources, err)
	}
}
