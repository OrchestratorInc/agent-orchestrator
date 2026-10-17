package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type branchListSCM struct {
	fakeSCM
	pulls        []ports.SCMPRObservation
	repo         ports.SCMRepo
	updatedAfter time.Time
}

func (f *branchListSCM) ListPRsByRepo(_ context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error) {
	f.repo, f.updatedAfter = repo, updatedAfter
	return f.pulls, nil
}

// A PR opened moments ago is found by the session's branch in the project's
// canonical repository; other branches, forks, and closed PRs are not.
func TestFindBranchPRsMatchesOpenPRsFromTheSessionBranch(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Branch: "ao/demo-1/fix", WorkspacePath: "/ws"}}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/alice/repo", Config: domain.ProjectConfig{CanonicalRepoURL: "https://github.com/acme/repo"}}
	scm := &branchListSCM{pulls: []ports.SCMPRObservation{
		{HTMLURL: "https://github.com/acme/repo/pull/7", SourceBranch: "ao/demo-1/fix", HeadRepo: "acme/repo", BaseRepo: "acme/repo"},
		{HTMLURL: "https://github.com/acme/repo/pull/8", SourceBranch: "ao/demo-2/fix", HeadRepo: "acme/repo", BaseRepo: "acme/repo"},
		{HTMLURL: "https://github.com/acme/repo/pull/9", SourceBranch: "ao/demo-1/fix", HeadRepo: "mallory/repo", BaseRepo: "acme/repo"},
		{HTMLURL: "https://github.com/acme/repo/pull/6", SourceBranch: "ao/demo-1/fix", Closed: true},
	}}
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return now }})

	urls, err := svc.FindBranchPRs(context.Background(), "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(urls, ",") != "https://github.com/acme/repo/pull/7" {
		t.Fatalf("urls = %v, want only the open same-repo PR from the session branch", urls)
	}
	if scm.repo.Repo != "acme/repo" || !scm.updatedAfter.Equal(now.Add(-recentBranchPRWindow)) {
		t.Fatalf("listed %+v since %v, want the canonical repo's recently updated PRs", scm.repo, scm.updatedAfter)
	}
}

func TestFindBranchPRsWithoutBranchOrListingFindsNothing(t *testing.T) {
	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
	scm := &branchListSCM{pulls: []ports.SCMPRObservation{{HTMLURL: "https://github.com/acme/repo/pull/7", SourceBranch: ""}}}
	if urls, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "demo-1"); err != nil || len(urls) != 0 {
		t.Fatalf("no branch: urls=%v err=%v", urls, err)
	}
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Branch: "b"}}
	if urls, err := NewWithDeps(Deps{Store: st, SCM: fakeSCM{}}).FindBranchPRs(context.Background(), "demo-1"); err != nil || len(urls) != 0 {
		t.Fatalf("provider cannot list: urls=%v err=%v", urls, err)
	}
}
