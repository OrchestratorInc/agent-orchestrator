package sessionmanager

import (
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSpawn_DefaultBranchFetchFailureRecordsSpawnWarning(t *testing.T) {
	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: testRoleAgents()}
	ws.fetchErr = errors.New("network unavailable")

	worker, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	warnings := st.sessions[worker.ID].SpawnWarnings
	if len(warnings) != 1 {
		t.Fatalf("spawn warnings = %q, want one warning", warnings)
	}
	if !strings.Contains(warnings[0], "stale local base") || !strings.Contains(warnings[0], "mer") {
		t.Fatalf("warning = %q, want it to name the repo and the stale base", warnings[0])
	}
	if strings.Contains(warnings[0], "network unavailable") || strings.Contains(warnings[0], "fatal:") {
		t.Fatalf("warning leaks git error detail: %q", warnings[0])
	}
}

func TestSpawn_SuccessfulDefaultBranchFetchRecordsNoSpawnWarning(t *testing.T) {
	m, st, _, _ := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: testRoleAgents()}

	worker, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[worker.ID].SpawnWarnings; len(got) != 0 {
		t.Fatalf("spawn warnings = %q, want none", got)
	}
}

func TestSpawn_DisableFetchBeforeSpawnSkipsFetchAndCreatesWorktree(t *testing.T) {
	m, st, _, ws := newManager()
	cfg := testRoleAgents()
	cfg.DisableFetchBeforeSpawn = true
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: cfg}
	ws.fetchErr = errors.New("would fail if attempted")

	worker, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.fetches) != 0 {
		t.Fatalf("fetches = %d, want 0 with DisableFetchBeforeSpawn", len(ws.fetches))
	}
	if ws.lastCfg.Branch != "ao/mer-1/root" {
		t.Fatalf("created branch = %q, want ao/mer-1/root", ws.lastCfg.Branch)
	}
	if got := st.sessions[worker.ID].SpawnWarnings; len(got) != 0 {
		t.Fatalf("spawn warnings = %q, want none when the fetch is disabled", got)
	}
}
