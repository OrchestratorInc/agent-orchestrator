package session

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestSpawnedSessionByClientRequestAnswersTheRestartQuestion covers the read the M2-04
// execution coordinator depends on before it decides whether a retry is a replay or a
// new spawn.
//
// The read must be strictly side-effect free: it is called on a path where a wrong answer
// means a duplicate session, and on a path where a mutation would corrupt an in-flight
// spawn.
func TestSpawnedSessionByClientRequestAnswersTheRestartQuestion(t *testing.T) {
	t.Parallel()
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: newFakeStore()})
	// Seed through the same store the service reads, so this asserts the wiring rather
	// than a hand-built map.
	store := svc.store.(*fakeStore)
	store.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", ClientRequestID: "req-1", ClientRequestHash: "hash-1",
		ClientRequestCommitted: true,
	}

	tests := []struct {
		name      string
		requestID string
		wantFound bool
		wantID    domain.SessionID
	}{
		{"known request", "req-1", true, "mer-1"},
		{"unknown request", "req-unknown", false, ""},
		{"empty request is not a lookup", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec, found, err := svc.SpawnedSessionByClientRequest(context.Background(), tt.requestID)
			if err != nil {
				t.Fatalf("SpawnedSessionByClientRequest(%q): %v", tt.requestID, err)
			}
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if found && rec.ID != tt.wantID {
				t.Fatalf("id = %q, want %q", rec.ID, tt.wantID)
			}
		})
	}
}

// TestSpawnedSessionByClientRequestDoesNotMutate proves the read leaves the stored record
// exactly as it was, including the commit flag that decides whether a replay is legal.
func TestSpawnedSessionByClientRequestDoesNotMutate(t *testing.T) {
	t.Parallel()
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: newFakeStore()})
	store := svc.store.(*fakeStore)
	original := domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", ClientRequestID: "req-2", ClientRequestHash: "hash-2",
		// Deliberately uncommitted: a replay of this request is NOT legal, and the read
		// must not make it look committed.
		ClientRequestCommitted: false,
	}
	store.sessions["mer-2"] = original

	rec, found, err := svc.SpawnedSessionByClientRequest(context.Background(), "req-2")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if rec.ClientRequestCommitted {
		t.Fatal("the read reported an uncommitted request as committed; a replay would then be wrongly allowed")
	}
	if after := store.sessions["mer-2"]; after.ClientRequestCommitted != original.ClientRequestCommitted ||
		after.ClientRequestHash != original.ClientRequestHash {
		t.Fatalf("the read mutated the stored record: %+v", after)
	}
}

// TestSpawnedSessionByClientRequestWithoutStore proves a service built without a store
// answers "nothing known" instead of panicking, so a partially wired daemon degrades
// safely.
func TestSpawnedSessionByClientRequestWithoutStore(t *testing.T) {
	t.Parallel()
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}})
	if _, found, err := svc.SpawnedSessionByClientRequest(context.Background(), "req-1"); err != nil || found {
		t.Fatalf("found=%v err=%v, want (false, nil)", found, err)
	}
}
