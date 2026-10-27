package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

const testOwnerUserToken = "ghu_ownerusertoken"

// authorStubStore is a session whose creator may have authorized the GitHub
// App, recording the author of the pull request the service records.
type authorStubStore struct {
	pullRequestStubStore
	creator        string
	connection     *domain.GitHubUserConnection
	recordedAuthor string
}

func (s *authorStubStore) SessionCreatorUserID(context.Context, string, string) (string, error) {
	if s.creator == "" {
		return "", postgres.ErrNotFound
	}
	return s.creator, nil
}

func (s *authorStubStore) GitHubUserConnection(_ context.Context, userID string) (domain.GitHubUserConnection, error) {
	if s.connection == nil || userID != s.creator {
		return domain.GitHubUserConnection{}, postgres.ErrNotFound
	}
	return *s.connection, nil
}

func (s *authorStubStore) CreatePullRequestRecord(
	ctx context.Context, orgID, sessionID string, provider, repository, author string, number int,
	url, head, base, headSHA, title string, additions, deletions, changedFiles int,
) (domain.PullRequest, error) {
	s.recordedAuthor = author
	return s.pullRequestStubStore.CreatePullRequestRecord(ctx, orgID, sessionID, provider, repository, author,
		number, url, head, base, headSHA, title, additions, deletions, changedFiles)
}

// ownerConnection is the creator's stored, encrypted GitHub user token, valid
// for hours so no refresh happens.
func ownerConnection(t *testing.T, userID string) *domain.GitHubUserConnection {
	t.Helper()
	ciphertext, nonce, err := Encrypt(make([]byte, 32), []byte(testOwnerUserToken),
		githubUserTokenAssociatedData(userID, 42, "access"))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(8 * time.Hour)
	return &domain.GitHubUserConnection{
		UserID: userID, GitHubUserID: 42, GitHubLogin: "octocat",
		AccessTokenCiphertext: ciphertext, AccessTokenNonce: nonce, AccessTokenExpiresAt: &expires,
	}
}

// authorPullRequestServer opens pull requests as whoever the bearer token
// belongs to: the owner's user token is "octocat", anything else the App's
// bot. refuseUser makes GitHub answer the owner's token with that status.
func authorPullRequestServer(t *testing.T, backend *installationRepositoryServer, refuseUser int, authorizations *[]string) *httptest.Server {
	t.Helper()
	inner := backend.handler(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/pulls") {
			inner(w, r)
			return
		}
		authorization := r.Header.Get("Authorization")
		*authorizations = append(*authorizations, authorization)
		var body struct {
			Base  string `json:"base"`
			Draft bool   `json:"draft"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		login := "ao-test[bot]"
		if authorization == "Bearer "+testOwnerUserToken {
			if refuseUser != 0 {
				w.WriteHeader(refuseUser)
				_, _ = w.Write([]byte(`{"message":"refused"}`))
				return
			}
			login = "octocat"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "html_url": "https://github.com/octo/app/pull/7",
			"head": map[string]any{"sha": "abc", "ref": "ao/s1"}, "base": map[string]any{"ref": body.Base},
			"user": map[string]any{"login": login}, "state": "open", "title": "t", "draft": body.Draft,
		})
	}))
}

func newAuthorTestService(t *testing.T, store Store, server *httptest.Server, logs *bytes.Buffer) *Service {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc, err := NewService(store, newAppTestClient(t, server.URL, server.Client()),
		make([]byte, 32), make([]byte, 32), "webhook-secret", time.Hour, logger)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestRaisePullRequestIsAuthoredByTheSessionOwner(t *testing.T) {
	backend := &installationRepositoryServer{repos: []Repository{{ID: 1, FullName: "octo/app"}}}
	var authorizations []string
	server := authorPullRequestServer(t, backend, 0, &authorizations)
	defer server.Close()
	store := &authorStubStore{
		pullRequestStubStore: pullRequestStubStore{checkoutStubStore: checkoutStubStore{primary: primaryContext()}},
		creator:              "user-owner",
	}
	store.connection = ownerConnection(t, store.creator)
	var logs bytes.Buffer
	svc := newAuthorTestService(t, store, server, &logs)

	if _, err := svc.RaisePullRequest(context.Background(), "org-1", "sess-1",
		domain.RaisePullRequest{Title: "t", HeadBranch: "ao/s1"}); err != nil {
		t.Fatalf("RaisePullRequest: %v", err)
	}
	if len(authorizations) != 1 || authorizations[0] != "Bearer "+testOwnerUserToken {
		t.Fatalf("pull request authorizations = %v, want the owner's user token only", authorizations)
	}
	if store.recordedAuthor != "octocat" {
		t.Fatalf("recorded author = %q, want the owner", store.recordedAuthor)
	}
	// The owner's token opened it, so no installation write token was minted.
	if backend.mintedPermissions["pull_requests"] != "" {
		t.Fatalf("minted an installation write token: %v", backend.mintedPermissions)
	}
	if strings.Contains(logs.String(), testOwnerUserToken) {
		t.Fatal("the owner's GitHub token was logged")
	}
}

func TestRaisePullRequestFallsBackToTheAppWithoutAnOwnerAuthorization(t *testing.T) {
	for name, store := range map[string]*authorStubStore{
		"no recorded creator":      {},
		"creator never authorized": {creator: "user-owner"},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &installationRepositoryServer{repos: []Repository{{ID: 1, FullName: "octo/app"}}}
			var authorizations []string
			server := authorPullRequestServer(t, backend, 0, &authorizations)
			defer server.Close()
			store.pullRequestStubStore = pullRequestStubStore{checkoutStubStore: checkoutStubStore{primary: primaryContext()}}
			var logs bytes.Buffer
			svc := newAuthorTestService(t, store, server, &logs)

			if _, err := svc.RaisePullRequest(context.Background(), "org-1", "sess-1",
				domain.RaisePullRequest{Title: "t", HeadBranch: "ao/s1"}); err != nil {
				t.Fatalf("RaisePullRequest: %v", err)
			}
			if len(authorizations) != 1 || authorizations[0] != "Bearer ghs_testinstallationtoken" {
				t.Fatalf("pull request authorizations = %v, want the installation token", authorizations)
			}
			if store.recordedAuthor != "ao-test[bot]" {
				t.Fatalf("recorded author = %q, want the App", store.recordedAuthor)
			}
		})
	}
}

// GitHub refusing the owner's authorization (revoked, or no access to this
// repository) opens the pull request as the App instead of failing it.
func TestRaisePullRequestFallsBackToTheAppWhenGitHubRefusesTheOwner(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		backend := &installationRepositoryServer{repos: []Repository{{ID: 1, FullName: "octo/app"}}}
		var authorizations []string
		server := authorPullRequestServer(t, backend, status, &authorizations)
		store := &authorStubStore{
			pullRequestStubStore: pullRequestStubStore{checkoutStubStore: checkoutStubStore{primary: primaryContext()}},
			creator:              "user-owner",
		}
		store.connection = ownerConnection(t, store.creator)
		var logs bytes.Buffer
		svc := newAuthorTestService(t, store, server, &logs)

		if _, err := svc.RaisePullRequest(context.Background(), "org-1", "sess-1",
			domain.RaisePullRequest{Title: "t", HeadBranch: "ao/s1"}); err != nil {
			t.Fatalf("status %d: RaisePullRequest: %v", status, err)
		}
		server.Close()
		if len(authorizations) != 2 || authorizations[0] != "Bearer "+testOwnerUserToken ||
			authorizations[1] != "Bearer ghs_testinstallationtoken" {
			t.Fatalf("status %d: authorizations = %v, want the owner then the App", status, authorizations)
		}
		if store.recordedAuthor != "ao-test[bot]" {
			t.Fatalf("status %d: recorded author = %q, want the App", status, store.recordedAuthor)
		}
		if strings.Contains(logs.String(), testOwnerUserToken) {
			t.Fatalf("status %d: the owner's GitHub token was logged", status)
		}
	}
}

// A request GitHub rejects on its merits (an existing pull request, a bad
// branch) would fail as the App too, so it is returned rather than retried.
func TestRaisePullRequestDoesNotRetryARejectedRequestAsTheApp(t *testing.T) {
	backend := &installationRepositoryServer{repos: []Repository{{ID: 1, FullName: "octo/app"}}}
	var authorizations []string
	server := authorPullRequestServer(t, backend, http.StatusUnprocessableEntity, &authorizations)
	defer server.Close()
	store := &authorStubStore{
		pullRequestStubStore: pullRequestStubStore{checkoutStubStore: checkoutStubStore{primary: primaryContext()}},
		creator:              "user-owner",
	}
	store.connection = ownerConnection(t, store.creator)
	var logs bytes.Buffer
	svc := newAuthorTestService(t, store, server, &logs)

	if _, err := svc.RaisePullRequest(context.Background(), "org-1", "sess-1",
		domain.RaisePullRequest{Title: "t", HeadBranch: "ao/s1"}); err == nil {
		t.Fatal("RaisePullRequest succeeded, want GitHub's rejection")
	}
	if len(authorizations) != 1 {
		t.Fatalf("authorizations = %v, want the owner's attempt only", authorizations)
	}
}

// mergeStubStore resolves the repository's installation and its
// whole-installation connection, as the Postgres store does.
type mergeStubStore struct {
	authorStubStore
}

func (*mergeStubStore) GitHubInstallationForRepository(context.Context, string, string) (int64, int64, error) {
	return 1234, 1, nil
}

func (*mergeStubStore) GitHubRepositoryAccessScope(context.Context, string, string) (string, string, error) {
	return domain.GitHubAccessScopeInstallation, "", nil
}

// mergeServer merges as whoever the bearer token is, recording each attempt.
// refuseUser makes GitHub answer the owner's token with that status.
func mergeServer(t *testing.T, refuseUser int, authorizations *[]string) *httptest.Server {
	t.Helper()
	backend := &installationRepositoryServer{repos: []Repository{{ID: 1, FullName: "octo/app"}}}
	inner := backend.handler(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/octo/app/pulls/7/merge" {
			inner(w, r)
			return
		}
		authorization := r.Header.Get("Authorization")
		*authorizations = append(*authorizations, authorization)
		if authorization == "Bearer "+testOwnerUserToken && refuseUser != 0 {
			w.WriteHeader(refuseUser)
			_, _ = w.Write([]byte(`{"message":"refused"}`))
			return
		}
		_, _ = w.Write([]byte(`{"merged":true}`))
	}))
}

func newMergeTestService(t *testing.T, server *httptest.Server, owner bool) *Service {
	t.Helper()
	store := &mergeStubStore{authorStubStore{creator: "user-owner"}}
	if owner {
		store.connection = ownerConnection(t, store.creator)
	}
	var logs bytes.Buffer
	return newAuthorTestService(t, store, server, &logs)
}

// A session's pull request is merged as the session's owner, like it was
// opened; without their authorization, or when GitHub refuses it, the App
// merges it as before; a merge GitHub rejects on its merits is not retried.
func TestMergePullRequestMergesAsTheSessionOwner(t *testing.T) {
	installation := "Bearer ghs_testinstallationtoken"
	owner := "Bearer " + testOwnerUserToken
	for _, test := range []struct {
		name       string
		owner      bool
		refuseUser int
		want       []string
		wantErr    bool
	}{
		{"owner authorized", true, 0, []string{owner}, false},
		{"owner never authorized", false, 0, []string{installation}, false},
		{"GitHub refuses the owner", true, http.StatusForbidden, []string{owner, installation}, false},
		{"owner's authorization revoked", true, http.StatusUnauthorized, []string{owner, installation}, false},
		{"not mergeable", true, http.StatusMethodNotAllowed, []string{owner}, true},
		{"head moved", true, http.StatusConflict, []string{owner}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var authorizations []string
			server := mergeServer(t, test.refuseUser, &authorizations)
			defer server.Close()
			svc := newMergeTestService(t, server, test.owner)
			err := svc.MergePullRequest(context.Background(), "org-1", "sess-1", "octo/app", 7, "abc")
			if (err != nil) != test.wantErr {
				t.Fatalf("MergePullRequest err = %v, want error %v", err, test.wantErr)
			}
			if strings.Join(authorizations, ",") != strings.Join(test.want, ",") {
				t.Fatalf("merge attempts = %v, want %v", authorizations, test.want)
			}
		})
	}
}
