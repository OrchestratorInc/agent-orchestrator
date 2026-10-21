package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// pullRequestStubStore records the pull requests the service opens or claims.
type pullRequestStubStore struct {
	checkoutStubStore
	recordedRepository string
	recordedBase       string
	claimed            domain.PullRequest
}

func (s *pullRequestStubStore) CreatePullRequestRecord(
	_ context.Context, _, _ string, _, repository, _ string, number int, url, head, base, _, _ string, _, _, _ int,
) (domain.PullRequest, error) {
	s.recordedRepository, s.recordedBase = repository, base
	return domain.PullRequest{Repository: repository, Number: number, URL: url, SourceBranch: head, TargetBranch: base}, nil
}

func (s *pullRequestStubStore) ClaimPullRequestRecord(_ context.Context, _, _ string, input domain.PullRequest) (domain.PullRequest, error) {
	s.claimed = input
	return input, nil
}

// multiRepoPullRequestServer answers installation tokens and repository
// listing like installationRepositoryServer, plus the pull request endpoints,
// recording which repository each pull request call targets.
func multiRepoPullRequestServer(t *testing.T, backend *installationRepositoryServer, created *[]string) *httptest.Server {
	t.Helper()
	inner := backend.handler(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
			var body struct {
				Base string `json:"base"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			*created = append(*created, r.URL.Path+" base="+body.Base)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 7, "html_url": "https://github.com" + strings.TrimSuffix(r.URL.Path, "s") + "/7",
				"head": map[string]any{"sha": "abc", "ref": "ao/s1"}, "base": map[string]any{"ref": body.Base},
				"user": map[string]any{"login": "ao-bot"}, "state": "open", "title": "t",
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/") && strings.Contains(r.URL.Path, "/pulls/"):
			*created = append(*created, "GET "+r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 9, "html_url": "https://github.com/octo/lib/pull/9", "state": "open", "title": "t",
				"head": map[string]any{"sha": "abc", "ref": "ao/s1"}, "base": map[string]any{"ref": "develop"},
				"user": map[string]any{"login": "dev"},
			})
		default:
			inner(w, r)
		}
	}))
}

func TestRaisePullRequestOpensItInADeclaredAdditionalRepository(t *testing.T) {
	backend := &installationRepositoryServer{
		repos:       []Repository{{ID: 1, FullName: "octo/app"}, {ID: 2, FullName: "octo/lib"}},
		directRepos: map[string]Repository{"octo/lib": {ID: 2, FullName: "octo/lib", DefaultBranch: "develop"}},
	}
	var calls []string
	server := multiRepoPullRequestServer(t, backend, &calls)
	defer server.Close()
	store := &pullRequestStubStore{checkoutStubStore: checkoutStubStore{
		primary: primaryContext(), extras: []domain.RepoRef{{URL: "https://github.com/octo/lib"}},
	}}
	svc := newCheckoutTestService(t, store, newAppTestClient(t, server.URL, server.Client()))

	pr, err := svc.RaisePullRequest(context.Background(), "org-1", "sess-1", domain.RaisePullRequest{
		Title: "t", HeadBranch: "ao/s1", Repository: "OCTO/lib",
	})
	if err != nil {
		t.Fatalf("RaisePullRequest: %v", err)
	}
	if pr.Repository != "octo/lib" || store.recordedRepository != "octo/lib" || store.recordedBase != "develop" {
		t.Fatalf("pr = %+v, recorded %q base %q", pr, store.recordedRepository, store.recordedBase)
	}
	if len(calls) != 1 || calls[0] != "/repos/octo/lib/pulls base=develop" {
		t.Fatalf("pull request calls = %v", calls)
	}
	if len(backend.mintedRepoIDs) != 1 || backend.mintedRepoIDs[0] != 2 {
		t.Fatalf("token scoped to %v, want the additional repository", backend.mintedRepoIDs)
	}

	// The main repository keeps its recorded default branch and needs no lookup.
	calls = nil
	if _, err := svc.RaisePullRequest(context.Background(), "org-1", "sess-1", domain.RaisePullRequest{Title: "t", HeadBranch: "ao/s1"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "/repos/octo/app/pulls base=main" || store.recordedRepository != "octo/app" {
		t.Fatalf("main pull request calls = %v recorded %q", calls, store.recordedRepository)
	}

	// A repository the project does not declare is refused before any GitHub call.
	calls = nil
	_, err = svc.RaisePullRequest(context.Background(), "org-1", "sess-1", domain.RaisePullRequest{Title: "t", HeadBranch: "ao/s1", Repository: "octo/secret"})
	if !errors.Is(err, postgres.ErrForbidden) || len(calls) != 0 {
		t.Fatalf("undeclared err = %v calls = %v", err, calls)
	}
}

func TestClaimPullRequestAcceptsADeclaredAdditionalRepository(t *testing.T) {
	backend := &installationRepositoryServer{repos: []Repository{{ID: 1, FullName: "octo/app"}, {ID: 2, FullName: "octo/lib"}}}
	var calls []string
	server := multiRepoPullRequestServer(t, backend, &calls)
	defer server.Close()
	store := &pullRequestStubStore{checkoutStubStore: checkoutStubStore{
		primary: primaryContext(), extras: []domain.RepoRef{{URL: "https://github.com/octo/lib"}},
	}}
	svc := newCheckoutTestService(t, store, newAppTestClient(t, server.URL, server.Client()))

	if _, err := svc.ClaimPullRequest(context.Background(), "org-1", "sess-1", "https://github.com/octo/lib/pull/9"); err != nil {
		t.Fatalf("ClaimPullRequest: %v", err)
	}
	if store.claimed.Repository != "octo/lib" || len(calls) != 1 || calls[0] != "GET /repos/octo/lib/pulls/9" {
		t.Fatalf("claimed %+v calls %v", store.claimed, calls)
	}
	// Another repository's URL is still an invalid reference for this session.
	if _, err := svc.ClaimPullRequest(context.Background(), "org-1", "sess-1", "https://github.com/octo/other/pull/9"); !errors.Is(err, postgres.ErrInvalid) {
		t.Fatalf("undeclared claim err = %v", err)
	}
}

func TestParsePullRequestTarget(t *testing.T) {
	for reference, want := range map[string]struct {
		fullName string
		number   int
	}{
		"12":                                   {"", 12},
		"https://github.com/octo/lib/pull/3":   {"octo/lib", 3},
		" https://github.com/Octo/App/pull/5 ": {"Octo/App", 5},
	} {
		fullName, number, err := ParsePullRequestTarget(reference)
		if err != nil || fullName != want.fullName || number != want.number {
			t.Errorf("%q = %q %d %v", reference, fullName, number, err)
		}
	}
	for _, bad := range []string{"0", "https://gitlab.com/o/r/pull/1", "https://github.com/o/r/issues/1", "https://github.com/o/r/pull/x", "https://github.com/o/r/pull/1?x=1"} {
		if _, _, err := ParsePullRequestTarget(bad); !errors.Is(err, postgres.ErrInvalid) {
			t.Errorf("%q err = %v", bad, err)
		}
	}
	if _, err := parsePullRequestReference("https://github.com/octo/lib/pull/3", "octo/app"); !errors.Is(err, postgres.ErrInvalid) {
		t.Fatalf("other repository reference err = %v", err)
	}
}
