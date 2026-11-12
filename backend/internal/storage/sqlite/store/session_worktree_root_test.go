package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestUpdateSessionKeepsWorktreeRootSnapshot covers the snapshot's durability:
// the root a worktree was created under is written once and must survive any
// later full-record update. A stale read or a narrow writer that carries no root
// would otherwise erase it, and restore/teardown would then validate the
// existing checkout against the wrong directory.
func TestUpdateSessionKeepsWorktreeRootSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "root-snapshot")

	rec := sampleRecord("root-snapshot")
	rec.Metadata.WorktreeRoot = "/custom/root"
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.Metadata.WorktreeRoot != "/custom/root" {
		t.Fatalf("created root = %q, want /custom/root", created.Metadata.WorktreeRoot)
	}

	// An update that still carries the snapshot must keep it.
	created.DisplayName = "renamed"
	created.UpdatedAt = created.UpdatedAt.Add(1)
	if err := s.UpdateSession(ctx, created); err != nil {
		t.Fatalf("update session: %v", err)
	}
	got, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if got.Metadata.WorktreeRoot != "/custom/root" {
		t.Fatalf("root after carrying update = %q, want /custom/root", got.Metadata.WorktreeRoot)
	}

	// An update from a caller that never knew the root (empty value) must not
	// clear it: the snapshot is creation-time evidence, not a mutable field.
	stale := got
	stale.Metadata.WorktreeRoot = ""
	stale.Metadata.Prompt = "a later prompt"
	stale.UpdatedAt = stale.UpdatedAt.Add(1)
	if err := s.UpdateSession(ctx, stale); err != nil {
		t.Fatalf("update session without root: %v", err)
	}
	got, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("get session after rootless update: ok=%v err=%v", ok, err)
	}
	if got.Metadata.WorktreeRoot != "/custom/root" {
		t.Fatalf("root after rootless update = %q, want the snapshot preserved", got.Metadata.WorktreeRoot)
	}
	// The rest of the update still applies: only the snapshot is write-once.
	if got.Metadata.Prompt != "a later prompt" {
		t.Fatalf("prompt = %q, want the update applied", got.Metadata.Prompt)
	}
}

// TestUpdateSessionWritesWorktreeRootWhenSet covers the other half: a root that
// is present in the update is still written, so a session whose snapshot was
// missing (a legacy row) can gain one.
func TestUpdateSessionWritesWorktreeRootWhenSet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "root-write")

	rec := sampleRecord("root-write")
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.Metadata.WorktreeRoot != "" {
		t.Fatalf("created root = %q, want empty", created.Metadata.WorktreeRoot)
	}
	created.Metadata.WorktreeRoot = "/late/root"
	created.UpdatedAt = created.UpdatedAt.Add(1)
	if err := s.UpdateSession(ctx, created); err != nil {
		t.Fatalf("update session: %v", err)
	}
	got, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if got.Metadata.WorktreeRoot != "/late/root" {
		t.Fatalf("root = %q, want /late/root", got.Metadata.WorktreeRoot)
	}
}

// TestSessionWorktreeRootSnapshotRoundTrips covers the workspace-project half of
// the snapshot: a per-repo row keeps its root across a state-only upsert, which
// is exactly how teardown marks rows unavailable.
func TestSessionWorktreeRootSnapshotRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "row-root")
	rec := sampleRecord("row-root")
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	row := domain.SessionWorktreeRecord{
		SessionID:    created.ID,
		RepoName:     domain.RootWorkspaceRepoName,
		ProjectID:    created.ProjectID,
		Branch:       "ao/work",
		WorktreePath: "/custom/root/row-root/work",
		WorktreeRoot: "/custom/root",
		State:        "active",
	}
	if err := s.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatalf("upsert worktree: %v", err)
	}
	// A state-only update that names no root must keep the recorded one.
	row.State = "unavailable"
	row.WorktreeRoot = ""
	if err := s.UpsertSessionWorktree(ctx, row); err != nil {
		t.Fatalf("upsert worktree state: %v", err)
	}
	got, ok, err := s.GetSessionWorktree(ctx, created.ID, domain.RootWorkspaceRepoName)
	if err != nil || !ok {
		t.Fatalf("get worktree: ok=%v err=%v", ok, err)
	}
	if got.WorktreeRoot != "/custom/root" {
		t.Fatalf("row root = %q, want /custom/root preserved", got.WorktreeRoot)
	}
}
