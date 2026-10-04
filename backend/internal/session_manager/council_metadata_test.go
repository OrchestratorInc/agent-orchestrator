package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A council member's group id must survive the spawn path's metadata rebuild,
// otherwise the cohort cannot be grouped for side-by-side comparison.
func TestSpawnPersistsCouncilGroupID(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		Worker: domain.RoleOverride{Harness: domain.HarnessCodex},
	}}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, CouncilGroupID: "council_abc"})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if rec.Metadata.CouncilGroupID != "council_abc" {
		t.Fatalf("spawn metadata councilGroupId = %q, want council_abc", rec.Metadata.CouncilGroupID)
	}
	stored, ok, err := st.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if stored.Metadata.CouncilGroupID != "council_abc" {
		t.Fatalf("stored metadata councilGroupId = %q, want council_abc", stored.Metadata.CouncilGroupID)
	}
}
