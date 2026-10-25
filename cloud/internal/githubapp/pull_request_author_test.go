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

func newAuthorTestService(t *testing.T, store *authorStubStore, server *httptest.Server, logs *bytes.Buffer) *Service {
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
