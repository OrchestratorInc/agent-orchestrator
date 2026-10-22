package store_test

import (
	"testing"
)

// A launched task's background rename applies only while the row still
// carries the temporary branch it renamed, so it never overwrites another move.
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
				rec.IsTerminated = true
			}
			created, err := s.CreateSession(t.Context(), rec)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := s.SetSessionTaskBranch(t.Context(), created.ID, "ao/mer-1/root", "ao/login/root", created.UpdatedAt)
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
