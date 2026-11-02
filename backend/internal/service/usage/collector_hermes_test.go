package usage

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestCollectorRegistersHermesStateFromRecordedHome catches reading a Hermes
// home other than the one AO launched the session against, and binding by
// anything other than the resolved native session ID.
func TestCollectorRegistersHermesStateFromRecordedHome(t *testing.T) {
	const nativeID = "20261103_100000_aaaa"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessHermes, nativeID, false)
	agents := t.TempDir()
	home := filepath.Join(t.TempDir(), "hermes-home")
	dbPath := filepath.Join(home, "state.db")
	writeUsageFixture(t, dbPath, "SQLite format 3\x00")
	writeUsageFixture(t, filepath.Join(agents, string(session.ID), "native-home"), home)
	collector := NewCollector(store, SourceRoots{HermesAgents: agents}, nil)

	roots, err := collector.HermesWatchRoots(context.Background())
	mustNoError(t, err)
	if !slices.Equal(roots, []string{home}) {
		t.Fatalf("watch roots = %v, want %q", roots, home)
	}
	mustNoError(t, collector.ReactivateSession(context.Background(), session.ID, session.Metadata.RuntimeLaunchID))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceHermesState ||
		sources[0].NativeSessionID != nativeID || sources[0].ArtifactPath != canonicalUsagePath(t, dbPath) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestCollectorSkipsHermesWithoutRecordedHome(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessHermes, "20261103_100000_aaaa", false)
	collector := NewCollector(store, SourceRoots{HermesAgents: t.TempDir()}, nil)
	path, err := collector.discoverPath(context.Background(), domain.HarnessHermes, "20261103_100000_aaaa", session.ID)
	mustNoError(t, err)
	if path != "" {
		t.Fatalf("discovered %q without a recorded Hermes home", path)
	}
}
