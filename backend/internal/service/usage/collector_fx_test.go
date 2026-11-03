package usage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestCollectorRegistersFXUsageByNativeSessionID catches binding an fx
// session's usage by anything other than the session ID Herdr reported.
func TestCollectorRegistersFXUsageByNativeSessionID(t *testing.T) {
	const nativeID = "hPP5JgsicDzo"
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessFX, nativeID, false)
	sessions := t.TempDir()
	usagePath := filepath.Join(sessions, nativeID, "usage-v2.json")
	writeUsageFixture(t, usagePath, `{"session_id":"`+nativeID+`","snapshot":{"models":[]}}`)
	writeUsageFixture(t, filepath.Join(sessions, "K8N2QX5pmQBw", "usage-v2.json"), `{}`)
	collector := NewCollector(store, SourceRoots{FXSessions: sessions}, nil)

	mustNoError(t, collector.ReactivateSession(context.Background(), session.ID, session.Metadata.RuntimeLaunchID))
	bindings, err := store.ListUsageBindingsForSession(context.Background(), session.ID)
	if err != nil || len(bindings) != 1 || bindings[0].NativeRootID != nativeID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceFXSession ||
		sources[0].NativeSessionID != nativeID || sources[0].ArtifactPath != canonicalUsagePath(t, usagePath) {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestCollectorRejectsFXUsageOutsideSessionDirectory(t *testing.T) {
	const nativeID = "hPP5JgsicDzo"
	binding := domain.UsageBindingRecord{Harness: domain.HarnessFX, NativeRootID: nativeID}
	other := filepath.Join(t.TempDir(), "K8N2QX5pmQBw", "usage-v2.json")
	if err := validateSourceAttribution(
		context.Background(), binding, domain.UsageSourceFXSession, nativeID, "", other, "",
	); err == nil {
		t.Fatal("accepted another session's usage-v2.json")
	}
}
