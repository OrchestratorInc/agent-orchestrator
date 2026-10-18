package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

const (
	scopeTestUserID       = "user-1"
	scopeTestGitHubUserID = 501
	scopeTestUserToken    = "ghu_member"
)

// userScopeStore answers the store calls user-scoped sync, grants and connects
// reach. A nil connection means the user has not authorized GitHub.
type userScopeStore struct {
	Store

	mu            sync.Mutex
	connection    *domain.GitHubUserConnection
	reconciled    [][]domain.GitHubRepository
	bound         []domain.GitHubInstallation
	bindErr       error
	failureMarked int
	// othersJoined: the organization has gained another active member, so the
	// user is no longer its only one.
	othersJoined bool
	syncs        int
	deleted      int
}

func (s *userScopeStore) GitHubUserScopeIntact(_ context.Context, orgID, userID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return orgID != "" && userID == scopeTestUserID && !s.othersJoined, nil
}

func (s *userScopeStore) DeleteGitHubUserConnection(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted++
	s.connection = nil
	return nil
}

func (s *userScopeStore) UpdateGitHubUserConnection(
	_ context.Context, userID string, input postgres.GitHubUserConnectionInput,
) (domain.GitHubUserConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := domain.GitHubUserConnection{
		UserID: userID, GitHubUserID: input.GitHubUserID,
		AccessTokenCiphertext: input.AccessTokenCiphertext, AccessTokenNonce: input.AccessTokenNonce,
		AccessTokenExpiresAt: input.AccessTokenExpiresAt, RefreshTokenCiphertext: input.RefreshTokenCiphertext,
		RefreshTokenNonce: input.RefreshTokenNonce, RefreshTokenExpiresAt: input.RefreshTokenExpiresAt,
		UpdatedAt: time.Now(),
	}
	s.connection = &updated
	return updated, nil
}

func (s *userScopeStore) GitHubUserConnection(context.Context, string) (domain.GitHubUserConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection == nil {
		return domain.GitHubUserConnection{}, postgres.ErrNotFound
	}
	return *s.connection, nil
}

func (s *userScopeStore) BeginGitHubRepositorySync(context.Context, domain.GitHubInstallation) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncs++
	return 1, nil
}

func (s *userScopeStore) ReconcileGitHubRepositories(
	_ context.Context, _ string, _ domain.GitHubInstallation, _ int64, repositories []domain.GitHubRepository,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconciled = append(s.reconciled, repositories)
	return nil
}

func (s *userScopeStore) MarkGitHubSyncFailure(context.Context, string, domain.GitHubInstallation, int64, string) error {
	s.failureMarked++
	return nil
}

func (s *userScopeStore) BindGitHubInstallation(
	_ context.Context, _ domain.Principal, orgID string, installation domain.GitHubInstallation,
) (domain.GitHubInstallation, error) {
	if s.bindErr != nil {
		return domain.GitHubInstallation{}, s.bindErr
	}
	installation.ID = "inst-" + installation.AccessScope
	installation.OrgID = orgID
	s.bound = append(s.bound, installation)
	return installation, nil
}

func (s *userScopeStore) GitHubRepositoryAccessScope(context.Context, string, string) (string, string, error) {
	return domain.GitHubAccessScopeUser, scopeTestUserID, nil
}

// connectedUser is a stored GitHub user connection whose access token decrypts
// to scopeTestUserToken under the test service's all-zero credential key.
func connectedUser(t *testing.T) *domain.GitHubUserConnection {
	t.Helper()
	ciphertext, nonce, err := Encrypt(make([]byte, 32), []byte(scopeTestUserToken),
		githubUserTokenAssociatedData(scopeTestUserID, scopeTestGitHubUserID, "access"))
	if err != nil {
		t.Fatal(err)
	}
	return &domain.GitHubUserConnection{
		UserID: scopeTestUserID, GitHubUserID: scopeTestGitHubUserID,
		AccessTokenCiphertext: ciphertext, AccessTokenNonce: nonce,
	}
}

// fakeGitHub serves the endpoints user-scoped access touches. Requests made
// with the installation token to list the whole installation fail the test:
// a user-scoped connection must never see more than its user.
type fakeGitHub struct {
	t                 *testing.T
	userRepositories  []int64 // /user/installations/1234/repositories
	userReposStatus   int     // non-zero: answer the user listing with this status
	rateLimited       bool    // mark error answers as GitHub's rate limit
	repositoryCalls   int     // GET /repositories/{id} calls
	permissions       map[int64]map[string]bool
	membershipRole    string
	installationOrgID int64
	// wholeInstallation allows listing the whole installation (an owner's
	// connection); otherwise doing so fails the test.
	wholeInstallation bool
}

func (f *fakeGitHub) server() *httptest.Server {
	encode := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	repository := func(id int64) map[string]any {
		name := map[int64]string{42: "app", 43: "lib", 44: "secret"}[id]
		return map[string]any{
			"id": id, "owner": map[string]any{"id": 900, "login": "acme", "type": "Organization"},
			"name": name, "full_name": "acme/" + name,
			"html_url": "https://github.com/acme/" + name, "clone_url": "https://github.com/acme/" + name + ".git",
			"default_branch": "main", "visibility": "private", "private": true,
			"updated_at": "2026-01-01T00:00:00Z",
		}
	}
	requireUserToken := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+scopeTestUserToken {
			f.t.Errorf("%s %s without the user's token", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/user/installations/1234/repositories", func(w http.ResponseWriter, r *http.Request) {
		if !requireUserToken(w, r) {
			return
		}
		if f.userReposStatus != 0 {
			if f.rateLimited {
				w.Header().Set("X-RateLimit-Remaining", "0")
			}
			encode(w, f.userReposStatus, map[string]any{"message": "nope"})
			return
		}
		repositories := make([]map[string]any, 0, len(f.userRepositories))
		for _, id := range f.userRepositories {
			repositories = append(repositories, repository(id))
		}
		encode(w, http.StatusOK, map[string]any{"total_count": len(repositories), "repositories": repositories})
	})
	mux.HandleFunc("/repositories/", func(w http.ResponseWriter, r *http.Request) {
		if !requireUserToken(w, r) {
			return
		}
		f.repositoryCalls++
		if f.rateLimited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			encode(w, http.StatusForbidden, map[string]any{"message": "API rate limit exceeded"})
			return
		}
		var id int64
		_ = json.Unmarshal([]byte(strings.TrimPrefix(r.URL.Path, "/repositories/")), &id)
		permissions, ok := f.permissions[id]
		if !ok {
			encode(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
		body := repository(id)
		body["permissions"] = permissions
		encode(w, http.StatusOK, body)
	})
	mux.HandleFunc("/user/installations", func(w http.ResponseWriter, r *http.Request) {
		if !requireUserToken(w, r) {
			return
		}
		encode(w, http.StatusOK, map[string]any{"installations": []map[string]any{{
			"id": 1234, "account": map[string]any{"id": f.installationOrgID, "login": "acme", "type": "Organization"},
		}}})
	})
	mux.HandleFunc("/app/installations/1234", func(w http.ResponseWriter, r *http.Request) {
		encode(w, http.StatusOK, map[string]any{
			"id": 1234, "account": map[string]any{"id": f.installationOrgID, "login": "acme", "type": "Organization"},
			"repository_selection": "selected", "permissions": map[string]string{"members": "read", "contents": "write"},
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if requireUserToken(w, r) {
			encode(w, http.StatusOK, map[string]any{"id": scopeTestGitHubUserID, "login": "member"})
		}
	})
	mux.HandleFunc("/user/memberships/orgs/acme", func(w http.ResponseWriter, r *http.Request) {
		if requireUserToken(w, r) {
			encode(w, http.StatusOK, map[string]any{"state": "active", "role": f.membershipRole})
		}
	})
	mux.HandleFunc("/app/installations/1234/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		encode(w, http.StatusCreated, map[string]any{
			"token": "ghs_installation", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		if !f.wholeInstallation {
			f.t.Errorf("listed the whole installation for a user-scoped connection")
		}
		encode(w, http.StatusOK, map[string]any{"repositories": []map[string]any{repository(42), repository(43), repository(44)}})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	return httptest.NewServer(mux)
}

func userScopedInstallation() domain.GitHubInstallation {
	return domain.GitHubInstallation{
		ID: "inst-1", OrgID: "org-1", GitHubInstallationID: 1234, Status: "active",
		AccessScope: domain.GitHubAccessScopeUser, ScopeUserID: scopeTestUserID,
	}
}

func reconciledIDs(repositories []domain.GitHubRepository) []int64 {
	ids := make([]int64, 0, len(repositories))
	for _, repository := range repositories {
		ids = append(ids, repository.GitHubRepositoryID)
	}
	return ids
}

// A user-scoped sync grants exactly what the user can access within the
// installation, listed with the user's own token.
func TestUserScopedSyncGrantsOnlyTheUsersRepositories(t *testing.T) {
	github := &fakeGitHub{t: t, userRepositories: []int64{42, 43}}
	server := github.server()
	defer server.Close()
	store := &userScopeStore{connection: connectedUser(t)}
	service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))

	if err := service.sync(context.Background(), userScopedInstallation()); err != nil {
		t.Fatal(err)
	}
	if len(store.reconciled) != 1 || len(store.reconciled[0]) != 2 ||
		reconciledIDs(store.reconciled[0])[0] != 42 || reconciledIDs(store.reconciled[0])[1] != 43 {
		t.Fatalf("reconciled = %v, want [42 43]", store.reconciled)
	}
}

// When the user's GitHub authorization is gone, or GitHub no longer lets the
// user reach the installation, the sync revokes every grant. A transient
// failure changes nothing.
func TestUserScopedSyncFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		connected  bool
		status     int
		wantErr    bool
		wantRevoke bool
	}{
		{"authorization gone", false, 0, false, true},
		{"github says not found", true, http.StatusNotFound, false, true},
		{"github says unauthorized", true, http.StatusUnauthorized, false, true},
		{"github says forbidden", true, http.StatusForbidden, false, true},
		{"transient github failure", true, http.StatusBadGateway, true, false},
		{"rate limited", true, -http.StatusForbidden, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			github := &fakeGitHub{t: t, userReposStatus: tc.status}
			if tc.status < 0 {
				github.userReposStatus, github.rateLimited = -tc.status, true
			}
			server := github.server()
			defer server.Close()
			store := &userScopeStore{}
			if tc.connected {
				store.connection = connectedUser(t)
			}
			service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
			err := service.sync(context.Background(), userScopedInstallation())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			revoked := len(store.reconciled) == 1 && len(store.reconciled[0]) == 0
			if revoked != tc.wantRevoke || (!tc.wantRevoke && len(store.reconciled) != 0) {
				t.Fatalf("reconciled = %v, want revoke-all %v", store.reconciled, tc.wantRevoke)
			}
		})
	}
}

// Checkout and push through a user-scoped connection are re-checked live
// against the user's GitHub permissions; a whole-installation connection is
// not.
func TestRequireUserScopedAccess(t *testing.T) {
	github := &fakeGitHub{t: t, permissions: map[int64]map[string]bool{
		42: {"pull": true, "push": true},
		43: {"pull": true},
	}}
	server := github.server()
	defer server.Close()
	store := &userScopeStore{connection: connectedUser(t)}
	service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
	ctx := context.Background()
	scoped := domain.GitHubCheckoutContext{OrgID: "org-1", AccessScope: domain.GitHubAccessScopeUser, ScopeUserID: scopeTestUserID}

	for _, tc := range []struct {
		repository int64
		push       bool
		allowed    bool
	}{
		{42, false, true}, {42, true, true},
		{43, false, true}, {43, true, false},
		{44, false, false}, {44, true, false},
	} {
		err := service.requireUserScopedAccess(ctx, scoped, tc.repository, tc.push)
		if allowed := err == nil; allowed != tc.allowed {
			t.Errorf("repository %d push=%v: err = %v, want allowed %v", tc.repository, tc.push, err, tc.allowed)
		}
		if err != nil && !errors.Is(err, postgres.ErrForbidden) {
			t.Errorf("repository %d: err = %v, want ErrForbidden", tc.repository, err)
		}
	}
	if ids := service.filterUserScopedRepositoryIDs(ctx, scoped, []int64{42, 43, 44}, true); len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("push scope = %v, want [42]", ids)
	}
	if ids := service.filterUserScopedRepositoryIDs(ctx, scoped, []int64{42, 43, 44}, false); len(ids) != 2 || ids[1] != 43 {
		t.Fatalf("checkout scope = %v, want [42 43]", ids)
	}
	if err := service.requireUserScopedAccess(ctx, domain.GitHubCheckoutContext{AccessScope: domain.GitHubAccessScopeInstallation}, 44, true); err != nil {
		t.Fatalf("whole-installation connection was checked: %v", err)
	}

	store.connection = nil
	if err := service.requireUserScopedAccess(ctx, scoped, 42, false); !errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("after the user disconnected GitHub: err = %v, want ErrForbidden", err)
	}
}

// Connecting the installations a user can see binds an organization the user
// does not own user-scoped, and one they own whole.
func TestConnectUserInstallationsScopesByOwnership(t *testing.T) {
	for _, tc := range []struct {
		role      string
		wantScope string
	}{
		{"member", domain.GitHubAccessScopeUser},
		{"admin", domain.GitHubAccessScopeInstallation},
	} {
		t.Run(tc.role, func(t *testing.T) {
			github := &fakeGitHub{t: t, membershipRole: tc.role, installationOrgID: 900, userRepositories: []int64{42},
				wholeInstallation: tc.role == "admin"}
			server := github.server()
			defer server.Close()
			store := &userScopeStore{connection: connectedUser(t)}
			service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
			connected, err := service.ConnectUserInstallations(context.Background(), domain.Principal{UserID: scopeTestUserID}, "org-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(connected) != 1 || len(store.bound) != 1 || store.bound[0].AccessScope != tc.wantScope {
				t.Fatalf("bound = %+v, want one %s connection", store.bound, tc.wantScope)
			}
			if tc.wantScope == domain.GitHubAccessScopeUser && store.bound[0].ScopeUserID != scopeTestUserID {
				t.Fatalf("user-scoped connection has scope user %q", store.bound[0].ScopeUserID)
			}
		})
	}
}

// A user-scoped connection into a team organization is refused with the
// store's reason when nothing else could be connected.
func TestConnectUserInstallationsReportsTheReason(t *testing.T) {
	github := &fakeGitHub{t: t, membershipRole: "member", installationOrgID: 900}
	server := github.server()
	defer server.Close()
	store := &userScopeStore{connection: connectedUser(t), bindErr: postgres.ErrGitHubUserScopeNeedsPersonalOrg}
	service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
	_, err := service.ConnectUserInstallations(context.Background(), domain.Principal{UserID: scopeTestUserID}, "org-1")
	if !errors.Is(err, postgres.ErrGitHubUserScopeNeedsPersonalOrg) {
		t.Fatalf("err = %v, want ErrGitHubUserScopeNeedsPersonalOrg", err)
	}
}

// A user-scoped connection stands on one user's GitHub access, so it ends
// once anyone else joins the organization: tokens are refused, the next sync
// revokes every grant, and nothing more is connected there.
func TestUserScopeEndsWhenTheOrganizationGainsAMember(t *testing.T) {
	github := &fakeGitHub{t: t, userRepositories: []int64{42}, membershipRole: "admin", installationOrgID: 900,
		permissions: map[int64]map[string]bool{42: {"pull": true, "push": true}}}
	server := github.server()
	defer server.Close()
	store := &userScopeStore{connection: connectedUser(t), othersJoined: true}
	service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
	ctx := context.Background()
	scoped := domain.GitHubCheckoutContext{OrgID: "org-1", AccessScope: domain.GitHubAccessScopeUser, ScopeUserID: scopeTestUserID}

	if err := service.requireUserScopedAccess(ctx, scoped, 42, false); !errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("checkout after another member joined: err = %v, want ErrForbidden", err)
	}
	if github.repositoryCalls != 0 {
		t.Fatalf("asked GitHub %d times for an organization the user no longer has alone", github.repositoryCalls)
	}
	if err := service.sync(ctx, userScopedInstallation()); err != nil {
		t.Fatal(err)
	}
	if len(store.reconciled) != 1 || len(store.reconciled[0]) != 0 {
		t.Fatalf("reconciled = %v, want every grant revoked", store.reconciled)
	}
	if _, err := service.ConnectUserInstallations(ctx, domain.Principal{UserID: scopeTestUserID}, "org-1"); !errors.Is(err, postgres.ErrGitHubUserScopeNeedsPersonalOrg) {
		t.Fatalf("connecting into a shared organization: err = %v, want ErrGitHubUserScopeNeedsPersonalOrg", err)
	}
	if len(store.bound) != 0 {
		t.Fatalf("bound %+v into a shared organization", store.bound)
	}
}

// A rate-limited access check is an error, not a denial; a positive check is
// reused briefly, a denial never is.
func TestUserRepositoryAccessCacheAndRateLimits(t *testing.T) {
	github := &fakeGitHub{t: t, permissions: map[int64]map[string]bool{42: {"pull": true, "push": true}}}
	server := github.server()
	defer server.Close()
	store := &userScopeStore{connection: connectedUser(t)}
	service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
	ctx := context.Background()
	scoped := domain.GitHubCheckoutContext{OrgID: "org-1", AccessScope: domain.GitHubAccessScopeUser, ScopeUserID: scopeTestUserID}

	for range 3 {
		if err := service.requireUserScopedAccess(ctx, scoped, 42, true); err != nil {
			t.Fatal(err)
		}
	}
	if github.repositoryCalls != 1 {
		t.Fatalf("GitHub asked %d times, want 1 (positive answer reused)", github.repositoryCalls)
	}
	if err := service.requireUserScopedAccess(ctx, scoped, 43, false); !errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("repository without access: err = %v, want ErrForbidden", err)
	}
	github.permissions[43] = map[string]bool{"pull": true}
	if err := service.requireUserScopedAccess(ctx, scoped, 43, false); err != nil {
		t.Fatalf("access granted on GitHub after a denial: err = %v, want allowed at once", err)
	}
	github.rateLimited = true
	err := service.requireUserScopedAccess(ctx, scoped, 44, false)
	if err == nil || errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("rate-limited check: err = %v, want a transient error", err)
	}
}

// Listing installations refreshes a stale user-scoped connection in the
// background, once per interval even when polled repeatedly.
func TestUserScopedRefreshRunsOnceInTheBackground(t *testing.T) {
	github := &fakeGitHub{t: t, userRepositories: []int64{42}}
	server := github.server()
	defer server.Close()
	store := &userScopeStore{connection: connectedUser(t)}
	service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
	installation := userScopedInstallation()
	stale := time.Now().Add(-2 * userScopedResyncAfter)
	installation.LastSyncedAt = &stale

	for range 5 {
		service.refreshUserScopedInBackground(context.Background(), installation)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.mu.Lock()
		syncs, reconciled := store.syncs, len(store.reconciled)
		store.mu.Unlock()
		if reconciled == 1 {
			if syncs != 1 {
				t.Fatalf("syncs = %d, want 1", syncs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fresh := time.Now()
	installation.ID, installation.LastSyncedAt = "inst-2", &fresh
	service.refreshUserScopedInBackground(context.Background(), installation)
	time.Sleep(50 * time.Millisecond)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.syncs != 1 {
		t.Fatalf("a fresh connection was refreshed: syncs = %d", store.syncs)
	}
}

// A write through an unknown scope is refused rather than allowed.
func TestRequireUserScopedPushWithoutScopeResolverRefuses(t *testing.T) {
	service := newCompleteOAuthTestService(t, &installOAuthStore{}, newExchangeTestClient(t, "https://github.invalid"))
	if err := service.requireUserScopedPush(context.Background(), "org-1", "acme/app", 42); !errors.Is(err, postgres.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// expiringConnection is a stored connection whose access token needs a
// refresh and whose refresh token decrypts under the test credential key.
func expiringConnection(t *testing.T, updatedAt time.Time) *domain.GitHubUserConnection {
	t.Helper()
	connection := connectedUser(t)
	key := make([]byte, 32)
	refresh, refreshNonce, err := Encrypt(key, []byte("ghr_refresh"),
		githubUserTokenAssociatedData(scopeTestUserID, scopeTestGitHubUserID, "refresh"))
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Minute)
	refreshExpires := time.Now().Add(time.Hour)
	connection.AccessTokenExpiresAt = &expired
	connection.RefreshTokenCiphertext, connection.RefreshTokenNonce = refresh, refreshNonce
	connection.RefreshTokenExpiresAt = &refreshExpires
	connection.UpdatedAt = updatedAt
	return connection
}

// rotatingStore returns the connection as first read, then (from the second
// read on) as another replica left it after refreshing first.
type rotatingStore struct {
	*userScopeStore
	reads int
	// rotatedAfter is how many reads pass before the other replica's write
	// lands; zero means it is already there when this replica looks.
	rotatedAfter int
	rotated      *domain.GitHubUserConnection
}

func (s *rotatingStore) GitHubUserConnection(ctx context.Context, userID string) (domain.GitHubUserConnection, error) {
	s.reads++
	if s.reads > 1+s.rotatedAfter && s.rotated != nil {
		return *s.rotated, nil
	}
	return s.userScopeStore.GitHubUserConnection(ctx, userID)
}

// A refresh that GitHub fails to answer keeps the connection; one it rejects
// because another replica already used the refresh token adopts that
// replica's tokens; only a refresh rejected outright discards it.
func TestGitHubUserTokenRefreshFailures(t *testing.T) {
	wait, poll := githubUserRotationWait, githubUserRotationPoll
	githubUserRotationWait, githubUserRotationPoll = 200*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { githubUserRotationWait, githubUserRotationPoll = wait, poll })
	read := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name         string
		answer       int
		body         string
		rotated      bool
		rotatedAfter int
		wantErr      error
		wantDeleted  int
	}{
		{"github unavailable", http.StatusBadGateway, `{"message":"bad gateway"}`, false, 0, nil, 0},
		{"rotated by another replica", http.StatusOK, `{"error":"bad_refresh_token"}`, true, 0, nil, 0},
		// The winning replica is still checking the account with GitHub when
		// this one's refresh is rejected; its write lands a few polls later.
		{"rotated by another replica, stored late", http.StatusOK, `{"error":"bad_refresh_token"}`, true, 3, nil, 0},
		{"rejected", http.StatusOK, `{"error":"bad_refresh_token"}`, false, 0, postgres.ErrNotFound, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.answer)
				_, _ = w.Write([]byte(tc.body))
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			base := &userScopeStore{connection: expiringConnection(t, read)}
			store := &rotatingStore{userScopeStore: base, rotatedAfter: tc.rotatedAfter}
			if tc.rotated {
				rotated := *connectedUser(t)
				rotated.UpdatedAt = time.Now()
				store.rotated = &rotated
			}
			service := newCompleteOAuthTestService(t, store, newExchangeTestClient(t, server.URL))
			_, token, err := service.githubUserAccessToken(context.Background(), scopeTestUserID)
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			case tc.wantErr == nil && tc.rotated && (err != nil || token != scopeTestUserToken):
				t.Fatalf("token, err = %q, %v; want the other replica's token", token, err)
			case tc.wantErr == nil && !tc.rotated && (err == nil || errors.Is(err, postgres.ErrNotFound)):
				t.Fatalf("err = %v, want a transient error", err)
			}
			if base.deleted != tc.wantDeleted {
				t.Fatalf("connection deleted %d times, want %d", base.deleted, tc.wantDeleted)
			}
		})
	}
}
