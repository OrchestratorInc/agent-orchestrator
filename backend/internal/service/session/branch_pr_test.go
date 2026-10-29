package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type branchListSCM struct {
	fakeSCM
	pulls  []ports.SCMPRObservation
	repo   ports.SCMRepo
	branch string
}

func (f *branchListSCM) ListOpenPRsByBranch(_ context.Context, repo ports.SCMRepo, branch string) ([]ports.SCMPRObservation, error) {
	f.repo, f.branch = repo, branch
	return f.pulls, nil
}

// A PR opened moments ago is found by the session's branch in the project's
// canonical repository; other branches, forks, and closed PRs are not, even
// if a provider returns them.
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
	if scm.repo.Repo != "acme/repo" || scm.branch != "ao/demo-1/fix" {
		t.Fatalf("listed %+v branch %q, want the canonical repo filtered to the session branch", scm.repo, scm.branch)
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

// repoBranchSCM answers ListOpenPRsByBranch per repository, so a test can
// place PRs in a workspace project's child repositories.
type repoBranchSCM struct {
	fakeSCM
	pulls  map[string][]ports.SCMPRObservation
	errs   map[string]error
	listed []string
}

func (f *repoBranchSCM) ListOpenPRsByBranch(_ context.Context, repo ports.SCMRepo, _ string) ([]ports.SCMPRObservation, error) {
	f.listed = append(f.listed, repo.Repo)
	return f.pulls[repo.Repo], f.errs[repo.Repo]
}

// A worker in a workspace project may open its PR in any registered child
// repository, the same ones ClaimPR accepts, so each is looked up. A child
// whose provider cannot list by branch is skipped.
func TestFindBranchPRsLooksInWorkspaceChildRepositories(t *testing.T) {
	st := &workspaceClaimStore{fakeStore: newFakeStore(), repos: []domain.WorkspaceRepoRecord{
		{RepoOriginURL: ""}, {RepoOriginURL: "/local/repo"},
		{RepoOriginURL: "git@github.com:acme/api.git"},
		{RepoOriginURL: "https://github.com/acme/web.git"},
		{RepoOriginURL: "https://github.com/acme/parent.git"},
	}}
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace, RepoOriginURL: "https://github.com/acme/parent"}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Branch: "ao/ws-1", WorkspacePath: "/ws"}}
	scm := &repoBranchSCM{
		pulls: map[string][]ports.SCMPRObservation{
			"acme/web": {{HTMLURL: "https://github.com/acme/web/pull/3", SourceBranch: "ao/ws-1", HeadRepo: "acme/web", BaseRepo: "acme/web"}},
		},
		errs: map[string]error{"acme/api": ports.ErrSCMUnsupported},
	}
	urls, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(urls, ",") != "https://github.com/acme/web/pull/3" {
		t.Fatalf("urls = %v, want the child repository's PR", urls)
	}
	if strings.Join(scm.listed, ",") != "acme/parent,acme/api,acme/web" {
		t.Fatalf("listed %v, want the root once and each child with a parseable origin", scm.listed)
	}

	// Any other listing failure is reported, so the caller does not claim the
	// branch has no PR.
	scm.listed, scm.errs = nil, map[string]error{"acme/api": errors.New("provider down")}
	if _, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "ws-1"); err == nil || !strings.Contains(err.Error(), "acme/api") {
		t.Fatalf("err = %v, want the failed child repository named", err)
	}
}
