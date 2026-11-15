package store_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A launched task's background rename applies only while the row still
// carries the temporary branch it renamed, so it never overwrites another move.
// A session stopped meanwhile still records the branch Git holds.
func TestSetSessionTaskBranchRequiresTemporaryBranch(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")
	for _, name := range []string{"temporary", "launched", "changed_branch", "terminated"} {
		t.Run(name, func(t *testing.T) {
			rec := sampleRecord("mer")
			rec.Metadata.Branch = "ao/mer-1/root"
			rec.Metadata.WorkspacePath = "/worktrees/mer-1"
			rec.DisplayName = "My chosen title"
			want := false
			switch name {
			case "temporary":
				want = true
			case "launched":
				rec.Metadata.ProviderConversationID, want = "live-provider", true
			case "changed_branch":
				rec.Metadata.Branch = "ao/mer-1/feature"
			case "terminated":
				rec.IsTerminated, want = true, true
			}
			created, err := s.CreateSession(t.Context(), rec)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := s.SetSessionTaskBranch(t.Context(), created.ID, "ao/mer-1/root", "ao/login/root", nil, created.UpdatedAt)
			if err != nil || updated != want {
				t.Fatalf("updated=%v, err=%v, want %v", updated, err, want)
			}
			actual, _, err := s.GetSession(t.Context(), created.ID)
			if err != nil || actual.DisplayName != rec.DisplayName || actual.Metadata.WorkspacePath != rec.Metadata.WorkspacePath {
				t.Fatalf("unrelated session fields changed: %+v, %v", actual, err)
			}
			wantBranch := rec.Metadata.Branch
			if want {
				wantBranch = "ao/login/root"
			}
			if actual.Metadata.Branch != wantBranch {
				t.Fatalf("branch = %q, want %q", actual.Metadata.Branch, wantBranch)
			}
		})
	}
}

// The session row and its repository rows move together, or not at all, and
// each repository row takes the branch its own checkout holds.
func TestSetSessionTaskBranchMovesWorktreeRows(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Metadata.Branch = "ao/mer-1"
	created, err := s.CreateSession(t.Context(), rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{domain.RootWorkspaceRepoName, "child"} {
		if err := s.UpsertSessionWorktree(t.Context(), domain.SessionWorktreeRecord{SessionID: created.ID, RepoName: repo, Branch: "ao/mer-1", WorktreePath: "/w/" + repo, State: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	held := map[string]string{domain.RootWorkspaceRepoName: "ao/login/root", "child": "ao/mer-1"}
	if ok, err := s.SetSessionTaskBranch(t.Context(), created.ID, "ao/other", "ao/login/root", held, created.UpdatedAt); err != nil || ok {
		t.Fatalf("mismatched rename = %v, %v", ok, err)
	}
	rows, _ := s.ListSessionWorktrees(t.Context(), created.ID)
	for _, row := range rows {
		if row.Branch != "ao/mer-1" {
			t.Fatalf("rejected rename moved a row: %+v", row)
		}
	}
	if ok, err := s.SetSessionTaskBranch(t.Context(), created.ID, "ao/mer-1", "ao/login/root", held, created.UpdatedAt); err != nil || !ok {
		t.Fatalf("rename = %v, %v", ok, err)
	}
	rows, _ = s.ListSessionWorktrees(t.Context(), created.ID)
	for _, row := range rows {
		if row.Branch != held[row.RepoName] {
			t.Fatalf("row %s = %q, want %q", row.RepoName, row.Branch, held[row.RepoName])
		}
	}
	// A root an undo put back on the temporary branch has nothing to guard.
	other, err := s.CreateSession(t.Context(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetSessionTaskBranch(t.Context(), other.ID, "ao/mer-1", "ao/mer-1", nil, other.UpdatedAt); err != nil || !ok {
		t.Fatalf("same-branch record = %v, %v", ok, err)
	}
	if got, _, _ := s.GetSession(t.Context(), other.ID); got.TemporaryBranch != "" {
		t.Fatalf("temporary branch = %q", got.TemporaryBranch)
	}
}

// A full-row write from a read taken before the rename puts back neither the
// temporary branch nor the old title, and the rename alone publishes a change.
func TestTaskRenameSurvivesStaleRowWrites(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Metadata.Branch, rec.DisplayName = "ao/mer-1/root", "Provisional"
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionBranchNaming(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	stale, _, err := s.GetSession(ctx, created.ID)
	if err != nil || !stale.BranchNaming {
		t.Fatalf("naming flag = %v, %v", stale.BranchNaming, err)
	}
	seq, _ := s.LatestSeq(ctx)
	if ok, err := s.SetSessionTaskBranch(ctx, created.ID, "ao/mer-1/root", "ao/login/root", nil, created.UpdatedAt); err != nil || !ok {
		t.Fatalf("rename = %v, %v", ok, err)
	}
	if events, err := s.EventsAfter(ctx, seq, 10); err != nil || len(events) != 1 {
		t.Fatalf("rename events = %+v, %v", events, err)
	}
	if ok, err := s.RenameSessionIfDisplayName(ctx, created.ID, "Provisional", "Fix login", created.UpdatedAt); err != nil || !ok {
		t.Fatalf("title = %v, %v", ok, err)
	}
	stale.Metadata.Model = "lifecycle write"
	if err := s.UpdateSession(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetSession(ctx, created.ID)
	if got.Metadata.Branch != "ao/login/root" || got.DisplayName != "Fix login" || got.BranchNaming || got.Metadata.Model != "lifecycle write" {
		t.Fatalf("after a stale write = %+v", got)
	}
	got.Metadata.Branch = ""
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	if cleared, _, _ := s.GetSession(ctx, created.ID); cleared.Metadata.Branch != "" {
		t.Fatalf("a deliberate branch change was dropped: %q", cleared.Metadata.Branch)
	}
	if err := s.SetSessionBranchNaming(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearSessionBranchNaming(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted, _, _ := s.GetSession(ctx, created.ID); restarted.BranchNaming {
		t.Fatal("a restart left branch naming on")
	}
}
