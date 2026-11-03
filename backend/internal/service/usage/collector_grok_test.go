package usage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestCollectorRegistersGrokUsageByNativeSessionID catches binding a Grok
// session's usage by workspace instead of by the session ID Grok's hooks
// report, and reading another session's usage.json from the same workspace.
func TestCollectorRegistersGrokUsageByNativeSessionID(t *testing.T) {
	const nativeID = "01a149b7-193c-7ae1-9e08-661fcfb1e60d"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessGrok, nativeID, false)
	sessions := t.TempDir()
	workspace := filepath.Join(sessions, "%2Fwork%2Frepo")
	usagePath := filepath.Join(workspace, nativeID, "usage.json")
	writeUsageFixture(t, usagePath, `{"sessionId":"`+nativeID+`","session":{}}`)
	writeUsageFixture(t, filepath.Join(workspace, "01a149b8-3536-7512-aa31-0907f5c78e26", "usage.json"), `{}`)
	collector := NewCollector(store, SourceRoots{GrokSessions: sessions}, nil)

	mustNoError(t, collector.ReactivateSession(context.Background(), session.ID, session.Metadata.RuntimeLaunchID))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceGrokSession ||
		sources[0].NativeSessionID != nativeID || sources[0].ArtifactPath != canonicalUsagePath(t, usagePath) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestCollectorRejectsGrokUsageOutsideSessionDirectory(t *testing.T) {
	const nativeID = "01a149b7-193c-7ae1-9e08-661fcfb1e60d"
	binding := domain.UsageBindingRecord{Harness: domain.HarnessGrok, NativeRootID: nativeID}
	other := filepath.Join(t.TempDir(), "01a149b8-3536-7512-aa31-0907f5c78e26", "usage.json")
	if err := validateSourceAttribution(
		context.Background(), binding, domain.UsageSourceGrokSession, nativeID, "", other, "",
	); err == nil {
		t.Fatal("accepted another session's usage.json")
	}
}
