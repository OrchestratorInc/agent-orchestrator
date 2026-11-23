package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type recordingGrants struct {
	created []domain.SessionID
	check   func(id domain.SessionID)
	err     error
}

func (g *recordingGrants) SessionCreated(_ context.Context, sessionID string) error {
	if g.check != nil {
		g.check(domain.SessionID(sessionID))
	}
	g.created = append(g.created, domain.SessionID(sessionID))
	return g.err
}

func TestSpawn_ReportsTheNewSessionBeforeItsAgentStarts(t *testing.T) {
	m, st, rt, _ := newManager()
	grants := &recordingGrants{check: func(id domain.SessionID) {
		if _, exists := st.sessions[id]; !exists {
			t.Errorf("reported %s before its row exists", id)
		}
		if rt.created != 0 {
			t.Errorf("reported %s after %d runtime(s) started", id, rt.created)
		}
	}}
	m.SetSessionGrants(grants)
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", ClientRequestID: "draft-1", ClientRequestHash: "v1:first"}
	rec, _, _, err := m.Spawn(ctx, cfg)
	if err != nil || len(grants.created) != 1 || grants.created[0] != rec.ID || rt.created != 1 {
		t.Fatalf("spawn = %q, reported = %v, runtimes = %d, err = %v", rec.ID, grants.created, rt.created, err)
	}
	if replay, _, _, err := m.Spawn(ctx, cfg); err != nil || replay.ID != rec.ID || len(grants.created) != 1 {
		t.Fatalf("a replayed request returns an existing session and must not report it again: %q, reported = %v, err = %v", replay.ID, grants.created, err)
	}
}

func TestSpawn_RollsBackWhenTheGrantCannotBeRecorded(t *testing.T) {
	m, st, rt, _ := newManager()
	m.SetSessionGrants(&recordingGrants{err: errors.New("disk full")})
	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ErrSpawnCreate) || rt.created != 0 {
		t.Fatalf("spawn err = %v, runtimes = %d, want a create failure and no runtime", err, rt.created)
	}
	for id, rec := range st.sessions {
		if !rec.IsTerminated {
			t.Errorf("session %s survived a spawn whose grant was not recorded", id)
		}
	}
}
