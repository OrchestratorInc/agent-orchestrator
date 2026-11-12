package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestMarkSpawnedKeepsWorktreeRootSnapshot covers the real store path: Spawn hands
// MarkSpawned a metadata value carrying the root the worktree was created under,
// and mergeMetadata must carry it into the persisted record. Dropping it there left
// every spawned session's recorded root empty, so restore and teardown fell back to
// the project's current config instead of the snapshot.
func TestMarkSpawnedKeepsWorktreeRootSnapshot(t *testing.T) {
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "root-snapshot", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "root-snapshot", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		CreatedAt: at, UpdatedAt: at,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: at},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, nil)

	const root = "/custom/worktree root"
	if err := manager.MarkSpawned(ctx, rec.ID, domain.SessionMetadata{
		Branch:          "ao/work",
		WorkspacePath:   root + "/root-snapshot/" + string(rec.ID),
		WorktreeRoot:    root,
		RuntimeHandleID: "handle",
	}); err != nil {
		t.Fatalf("MarkSpawned: %v", err)
	}

	stored, ok, err := store.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("load session: ok=%v err=%v", ok, err)
	}
	if stored.Metadata.WorktreeRoot != root {
		t.Fatalf("persisted worktree root = %q, want %q", stored.Metadata.WorktreeRoot, root)
	}

	// A later spawn/resume that carries no root must preserve the snapshot rather
	// than blanking it (mergeMetadata only overwrites with non-empty values).
	if err := manager.MarkSpawned(ctx, rec.ID, domain.SessionMetadata{
		Branch:          "ao/work",
		WorkspacePath:   root + "/root-snapshot/" + string(rec.ID),
		RuntimeHandleID: "handle-2",
	}); err != nil {
		t.Fatalf("second MarkSpawned: %v", err)
	}
	stored, ok, err = store.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("reload session: ok=%v err=%v", ok, err)
	}
	if stored.Metadata.WorktreeRoot != root {
		t.Fatalf("root after rootless MarkSpawned = %q, want %q preserved", stored.Metadata.WorktreeRoot, root)
	}
	if stored.Metadata.RuntimeHandleID != "handle-2" {
		t.Fatalf("runtime handle = %q, want the second launch applied", stored.Metadata.RuntimeHandleID)
	}
}
