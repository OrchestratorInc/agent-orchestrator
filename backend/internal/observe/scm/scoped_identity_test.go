package scm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeScopedIdentityResolver struct {
	identities map[string]ports.SCMIdentity
	errs       map[string]error
	calls      []string
}

func (r *fakeScopedIdentityResolver) AuthenticatedIdentityForProvider(_ context.Context, provider, host string) (ports.SCMIdentity, error) {
	r.calls = append(r.calls, provider)
	key := identityKey(provider, host)
	if err, ok := r.errs[key]; ok {
		return ports.SCMIdentity{}, err
	}
	return r.identities[key], nil
}

type hostAwareProvider struct {
	*fakeProvider
}

func (p *hostAwareProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	if strings.Contains(remote, "gitlab.com") {
		return glRepo, true
	}
	if strings.Contains(remote, "gitlab.internal") {
		return glSelfRepo, true
	}
	return p.fakeProvider.ParseRepository(remote)
}

var (
	glRepo     = ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "o", Name: "r", Repo: "o/r"}
	glSelfRepo = ports.SCMRepo{Provider: "gitlab", Host: "gitlab.internal", Owner: "o", Name: "r", Repo: "o/r"}
)

func testStoreWithTwoGitLabSessions() *fakeStore {
	return &fakeStore{
		sessions: []domain.SessionRecord{
			{ID: "gl-com", ProjectID: "gl-com-proj", Metadata: domain.SessionMetadata{Branch: "feat"}},
			{ID: "gl-int", ProjectID: "gl-int-proj", Metadata: domain.SessionMetadata{Branch: "feat"}},
		},
		projects: map[string]domain.ProjectRecord{
			"gl-com-proj": {ID: "gl-com-proj", RepoOriginURL: "https://gitlab.com/o/r.git"},
			"gl-int-proj": {ID: "gl-int-proj", RepoOriginURL: "https://gitlab.internal/o/r.git"},
		},
		prs:    map[domain.SessionID][]domain.PullRequest{},
		checks: map[string][]domain.PullRequestCheck{},
	}
}

func testStoreWithTwoSessions() *fakeStore {
	return &fakeStore{
		sessions: []domain.SessionRecord{
			{ID: "gh-1", ProjectID: "gh-proj", Metadata: domain.SessionMetadata{Branch: "feat"}},
			{ID: "gl-1", ProjectID: "gl-proj", Metadata: domain.SessionMetadata{Branch: "feat"}},
		},
		projects: map[string]domain.ProjectRecord{
			"gh-proj": {ID: "gh-proj", RepoOriginURL: "https://github.com/o/r.git"},
			"gl-proj": {ID: "gl-proj", RepoOriginURL: "https://gitlab.com/o/r.git"},
		},
		prs:    map[domain.SessionID][]domain.PullRequest{},
		checks: map[string][]domain.PullRequestCheck{},
	}
}

func TestPoll_TwoGitLabHostsIdentityResolution(t *testing.T) {
	store := testStoreWithTwoGitLabSessions()
	provider := &hostAwareProvider{fakeProvider: &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(glRepo, 0):     {ETag: "v2"},
			prKey(glSelfRepo, 0): {ETag: "v2"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(glRepo, 0): {
				{URL: "https://gitlab.com/o/r/-/merge_requests/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1", Author: "other"},
				{URL: "https://gitlab.com/o/r/-/merge_requests/2", Number: 2, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha2", Author: "comuser"},
			},
			prKey(glSelfRepo, 0): {
				{URL: "https://gitlab.internal/o/r/-/merge_requests/3", Number: 3, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha3", Author: "other"},
				{URL: "https://gitlab.internal/o/r/-/merge_requests/4", Number: 4, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha4", Author: "internaluser"},
			},
		},
		observations: map[string]ports.SCMObservation{
			prKey(glRepo, 2):     testObsGitLab(2),
			prKey(glSelfRepo, 4): testObsGitLabHost(4),
		},
	}}
	scoped := &fakeScopedIdentityResolver{
		identities: map[string]ports.SCMIdentity{
			identityKey("gitlab", "gitlab.com"):      {Login: "comuser", Human: true},
			identityKey("gitlab", "gitlab.internal"): {Login: "internaluser", Human: true},
		},
	}
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:                  func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:                   time.Hour,
		Logger:                 quietSlog(),
		CacheMax:               128,
		ScopedIdentityResolver: scoped,
	})
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	fetched := fetchedNumbers(provider.fetchBatches)

	if !fetched[2] || fetched[1] {
		t.Fatalf("gitlab.com fetched = %v, want only MR #2 (comuser's MR)", fetched)
	}

	if !fetched[4] || fetched[3] {
		t.Fatalf("gitlab.internal fetched = %v, want only MR #4 (internaluser's MR)", fetched)
	}

	for _, w := range store.writes {
		if w.pr.Number == 1 || w.pr.Number == 3 {
			t.Fatalf("foreign author's MR %d was persisted", w.pr.Number)
		}
	}
}

func TestPoll_PerProviderIdentityResolution(t *testing.T) {
	store := testStoreWithTwoSessions()
	provider := &hostAwareProvider{fakeProvider: &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(testRepo, 0): {ETag: "v2"},
			prKey(glRepo, 0):   {ETag: "v2"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(testRepo, 0): {
				{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1", Author: "other"},
				{URL: "https://github.com/o/r/pull/2", Number: 2, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha2", Author: "octocat"},
			},
			prKey(glRepo, 0): {
				{URL: "https://gitlab.com/o/r/-/merge_requests/3", Number: 3, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha3", Author: "other"},
				{URL: "https://gitlab.com/o/r/-/merge_requests/4", Number: 4, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha4", Author: "gitlabuser"},
			},
		},
		observations: map[string]ports.SCMObservation{
			prKey(testRepo, 2): testObs(2),
			prKey(glRepo, 4):   testObsGitLab(4),
		},
	}}
	scoped := &fakeScopedIdentityResolver{
		identities: map[string]ports.SCMIdentity{
			identityKey("github", "github.com"): {Login: "octocat", Human: true},
			identityKey("gitlab", "gitlab.com"): {Login: "gitlabuser", Human: true},
		},
	}
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:                  func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:                   time.Hour,
		Logger:                 quietSlog(),
		CacheMax:               128,
		ScopedIdentityResolver: scoped,
	})
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(scoped.calls) != 2 {
		t.Fatalf("identity resolver calls = %v, want both [github gitlab]", scoped.calls)
	}

	fetched := fetchedNumbers(provider.fetchBatches)
	if !fetched[2] || fetched[1] {
		t.Fatalf("github fetched = %v, want only PR #2 (octocat's PR)", fetched)
	}

	if !fetched[4] || fetched[3] {
		t.Fatalf("gitlab fetched = %v, want only PR #4 (gitlabuser's PR)", fetched)
	}

	for _, w := range store.writes {
		if w.pr.Number == 1 || w.pr.Number == 3 {
			t.Fatalf("foreign author's PR %d was persisted", w.pr.Number)
		}
	}
}

func TestPoll_ScopedIdentityPartialFailure(t *testing.T) {
	store := testStoreWithTwoSessions()
	provider := &hostAwareProvider{fakeProvider: &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(testRepo, 0): {ETag: "v2"},
			prKey(glRepo, 0):   {ETag: "v2"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(testRepo, 0): {
				{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1", Author: "octocat"},
			},
			prKey(glRepo, 0): {
				{URL: "https://gitlab.com/o/r/-/merge_requests/3", Number: 3, SourceBranch: "feat", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha3", Author: "gitlabuser"},
			},
		},
		observations: map[string]ports.SCMObservation{
			prKey(testRepo, 1): testObs(1),
			prKey(glRepo, 3):   testObsGitLab(3),
		},
	}}
	scoped := &fakeScopedIdentityResolver{
		identities: map[string]ports.SCMIdentity{
			identityKey("github", "github.com"): {Login: "octocat", Human: true},
		},
		errs: map[string]error{
			identityKey("gitlab", "gitlab.com"): errors.New("gitlab token expired"),
		},
	}
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:                  func() time.Time { return time.Unix(1, 0).UTC() },
		Tick:                   time.Hour,
		Logger:                 quietSlog(),
		CacheMax:               128,
		ScopedIdentityResolver: scoped,
	})
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	fetched := fetchedNumbers(provider.fetchBatches)
	if !fetched[1] {
		t.Fatalf("github PR #1 should be fetched (identity resolved), got fetched=%v", fetched)
	}

	if fetched[3] {
		t.Fatalf("gitlab PR #3 must not be attached without its identity, got fetched=%v", fetched)
	}
	for _, write := range store.writes {
		if write.pr.Provider == "gitlab" {
			t.Fatal("persisted GitLab PR without a matching human identity")
		}
	}

	if len(scoped.calls) != 2 {
		t.Fatalf("identity resolver calls = %v, want both [github gitlab]", scoped.calls)
	}
}

func testObsGitLabHost(num int) ports.SCMObservation {
	o := testObs(num)
	o.Provider = "gitlab"
	o.Host = "gitlab.internal"
	o.PR.URL = "https://gitlab.internal/o/r/-/merge_requests/" + fmt.Sprint(num)
	return o
}

func testObsGitLab(num int) ports.SCMObservation {
	o := testObs(num)
	o.Provider = "gitlab"
	o.Host = "gitlab.com"
	o.PR.URL = "https://gitlab.com/o/r/-/merge_requests/" + fmt.Sprint(num)
	return o
}

func fetchedNumbers(batches [][]ports.SCMPRRef) map[int]bool {
	m := map[int]bool{}
	for _, batch := range batches {
		for _, ref := range batch {
			m[ref.Number] = true
		}
	}
	return m
}
