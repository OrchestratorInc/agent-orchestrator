package usage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCollectorDiscoversGrokUsageSnapshot(t *testing.T) {
	const nativeID = "01a1a16e-c4c7-7c51-92d5-bf8beef81073"
	t.Setenv("GROK_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	roots, err := DefaultSourceRoots(context.Background(), t.TempDir())
	mustNoError(t, err)
	if roots.GrokSessions != filepath.Join(home, ".grok", "sessions") {
		t.Fatalf("Grok root = %q", roots.GrokSessions)
	}
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessGrok, nativeID, false)
	path := filepath.Join(roots.GrokSessions, "%2Frepo", nativeID, "usage.json")
	writeUsageFixture(t, path, `{"sessionId":"`+nativeID+`","turns":[]}`)
	collector := NewCollector(store, SourceRoots{GrokSessions: roots.GrokSessions}, nil)

	mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{
		Harness: domain.HarnessGrok, Event: "session-start", NativeSessionID: nativeID,
	}))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceGrokUsage ||
		sources[0].ArtifactPath != canonicalUsagePath(t, path) || sources[0].FileIdentity != grokSnapshotIdentity {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}
