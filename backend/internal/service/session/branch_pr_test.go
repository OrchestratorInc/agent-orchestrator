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

// signedIn answers AuthenticatedIdentityForProvider with one account, so a
// branch lookup can tell the session's own PRs from another author's.
type signedIn struct {
	identity ports.SCMIdentity
	err      error
	calls    int
}

func (f *signedIn) AuthenticatedIdentityForProvider(context.Context, string, string) (ports.SCMIdentity, error) {
	f.calls++
	return f.identity, f.err
}

func alice() *signedIn { return &signedIn{identity: ports.SCMIdentity{Login: "alice", Human: true}} }

type branchListSCM struct {
	fakeSCM
	*signedIn
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
	scm := &branchListSCM{signedIn: alice(), pulls: []ports.SCMPRObservation{
		{HTMLURL: "https://github.com/acme/repo/pull/7", SourceBranch: "ao/demo-1/fix", HeadRepo: "acme/repo", BaseRepo: "acme/repo", Author: "alice"},
		{HTMLURL: "https://github.com/acme/repo/pull/8", SourceBranch: "ao/demo-2/fix", HeadRepo: "acme/repo", BaseRepo: "acme/repo", Author: "alice"},
		{HTMLURL: "https://github.com/acme/repo/pull/9", SourceBranch: "ao/demo-1/fix", HeadRepo: "mallory/repo", BaseRepo: "acme/repo", Author: "alice"},
		{HTMLURL: "https://github.com/acme/repo/pull/6", SourceBranch: "ao/demo-1/fix", Closed: true, Author: "alice"},
	}}
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return now }})

	found, err := svc.FindBranchPRs(context.Background(), "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found.Own, ",") != "https://github.com/acme/repo/pull/7" || len(found.OtherAuthor) != 0 {
		t.Fatalf("found = %+v, want only the open same-repo PR from the session branch", found)
	}
	if scm.repo.Repo != "acme/repo" || scm.branch != "ao/demo-1/fix" {
		t.Fatalf("listed %+v branch %q, want the canonical repo filtered to the session branch", scm.repo, scm.branch)
	}
}

// A PR another developer opened on a branch of the same name is returned
// apart from the session's own, the same authorship rule the SCM observer
// applies, so the caller never attaches it on its own.
func TestFindBranchPRsSeparatesAnotherAuthorsPR(t *testing.T) {
	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Branch: "fix"}}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
	scm := &branchListSCM{signedIn: alice(), pulls: []ports.SCMPRObservation{
		{HTMLURL: "https://github.com/acme/repo/pull/7", SourceBranch: "fix", HeadRepo: "acme/repo", BaseRepo: "acme/repo", Author: "bob"},
		{HTMLURL: "https://github.com/acme/repo/pull/8", SourceBranch: "fix", HeadRepo: "acme/repo", BaseRepo: "acme/repo", Author: "Alice"},
		{HTMLURL: "https://github.com/acme/repo/pull/9", SourceBranch: "fix", HeadRepo: "acme/repo", BaseRepo: "acme/repo"},
	}}
	found, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found.Own, ",") != "https://github.com/acme/repo/pull/8" {
		t.Fatalf("own = %v, want only the signed-in account's PR", found.Own)
	}
	if strings.Join(found.OtherAuthor, ",") != "https://github.com/acme/repo/pull/7,https://github.com/acme/repo/pull/9" {
		t.Fatalf("other author = %v, want another author's PR and one with no author", found.OtherAuthor)
	}
	if scm.calls != 1 {
		t.Fatalf("identity resolved %d times, want once per provider and host", scm.calls)
	}
}

// Without a person's account AO cannot tell whose PR it found, so the lookup
// fails instead of attaching it; with no candidate, no identity is needed.
func TestFindBranchPRsFailsWhenTheAuthorCannotBeChecked(t *testing.T) {
	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Branch: "fix"}}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
	pulls := []ports.SCMPRObservation{{HTMLURL: "https://github.com/acme/repo/pull/7", SourceBranch: "fix", Author: "alice"}}
	for name, id := range map[string]*signedIn{
		"bot account":   {identity: ports.SCMIdentity{Login: "alice", Human: false}},
		"no login":      {identity: ports.SCMIdentity{Human: true}},
		"lookup failed": {err: errors.New("token expired")},
	} {
		scm := &branchListSCM{signedIn: id, pulls: pulls}
		found, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "demo-1")
		if err == nil || !strings.Contains(err.Error(), "cannot check who opened https://github.com/acme/repo/pull/7") || len(found.Own) != 0 {
			t.Fatalf("%s: found=%+v err=%v, want a failed author check", name, found, err)
		}
	}
	scm := &branchListSCM{signedIn: &signedIn{err: errors.New("token expired")}}
	if found, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "demo-1"); err != nil || len(found.Own)+len(found.OtherAuthor) != 0 || scm.calls != 0 {
		t.Fatalf("no candidate: found=%+v err=%v identity calls=%d, want nothing and no identity lookup", found, err, scm.calls)
	}
}

func TestFindBranchPRsWithoutBranchOrListingFindsNothing(t *testing.T) {
	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
	scm := &branchListSCM{signedIn: alice(), pulls: []ports.SCMPRObservation{{HTMLURL: "https://github.com/acme/repo/pull/7", SourceBranch: ""}}}
	if found, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "demo-1"); err != nil || len(found.Own)+len(found.OtherAuthor) != 0 {
		t.Fatalf("no branch: found=%+v err=%v", found, err)
	}
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Branch: "b"}}
	if found, err := NewWithDeps(Deps{Store: st, SCM: fakeSCM{}}).FindBranchPRs(context.Background(), "demo-1"); err != nil || len(found.Own)+len(found.OtherAuthor) != 0 {
		t.Fatalf("provider cannot list: found=%+v err=%v", found, err)
	}
}

// repoBranchSCM answers ListOpenPRsByBranch per repository, so a test can
// place PRs in a workspace project's child repositories.
type repoBranchSCM struct {
	fakeSCM
	*signedIn
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
// whose provider cannot list by branch is skipped, and another author's PR in
// a child repository is kept apart from the session's own.
func TestFindBranchPRsLooksInWorkspaceChildRepositories(t *testing.T) {
	st := &workspaceClaimStore{fakeStore: newFakeStore(), repos: []domain.WorkspaceRepoRecord{
		{RepoOriginURL: ""}, {RepoOriginURL: "/local/repo"},
		{RepoOriginURL: "git@github.com:acme/api.git"},
		{RepoOriginURL: "https://github.com/acme/web.git"},
		{RepoOriginURL: "https://github.com/acme/docs.git"},
		{RepoOriginURL: "https://github.com/acme/parent.git"},
	}}
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace, RepoOriginURL: "https://github.com/acme/parent"}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{Branch: "ao/ws-1", WorkspacePath: "/ws"}}
	scm := &repoBranchSCM{
		signedIn: alice(),
		pulls: map[string][]ports.SCMPRObservation{
			"acme/web":  {{HTMLURL: "https://github.com/acme/web/pull/3", SourceBranch: "ao/ws-1", HeadRepo: "acme/web", BaseRepo: "acme/web", Author: "alice"}},
			"acme/docs": {{HTMLURL: "https://github.com/acme/docs/pull/4", SourceBranch: "ao/ws-1", HeadRepo: "acme/docs", BaseRepo: "acme/docs", Author: "bob"}},
		},
		errs: map[string]error{"acme/api": ports.ErrSCMUnsupported},
	}
	found, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(found.Own, ",") != "https://github.com/acme/web/pull/3" {
		t.Fatalf("own = %v, want the child repository's PR", found.Own)
	}
	if strings.Join(found.OtherAuthor, ",") != "https://github.com/acme/docs/pull/4" {
		t.Fatalf("other author = %v, want another author's PR in a child repository", found.OtherAuthor)
	}
	if strings.Join(scm.listed, ",") != "acme/parent,acme/api,acme/web,acme/docs" {
		t.Fatalf("listed %v, want the root once and each child with a parseable origin", scm.listed)
	}

	// Any other listing failure is reported, so the caller does not claim the
	// branch has no PR.
	scm.listed, scm.errs = nil, map[string]error{"acme/api": errors.New("provider down")}
	if _, err := NewWithDeps(Deps{Store: st, SCM: scm}).FindBranchPRs(context.Background(), "ws-1"); err == nil || !strings.Contains(err.Error(), "acme/api") {
		t.Fatalf("err = %v, want the failed child repository named", err)
	}
}
