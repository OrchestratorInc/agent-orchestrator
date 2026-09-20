package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAutoSpawnGateAppliesToAgentRequestedSpawnsOnly(t *testing.T) {
	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Config: domain.ProjectConfig{DefaultBranch: "main"}}
	errLow := errors.New("low memory")
	fc := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st})
	svc.SetAutoSpawnGate(func(context.Context) error { return errLow })

	_, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "proj", Kind: domain.KindWorker, ParentSessionID: "orch"})
	if !errors.Is(err, errLow) {
		t.Fatalf("agent-requested spawn err = %v, want the gate's", err)
	}
	if fc.spawnCalls != 0 {
		t.Fatal("gated spawn must not reach the manager")
	}

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "proj", Kind: domain.KindWorker}); errors.Is(err, errLow) {
		t.Fatal("a user's own spawn must never be gated")
	}
}
