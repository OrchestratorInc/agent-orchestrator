package githubapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

type installOAuthStore struct {
	Store
	attempt          domain.GitHubInstallAttempt
	installStateHash []byte
	oauthStateHash   []byte
}

func (store *installOAuthStore) ValidateGitHubInstallState(_ context.Context, _ []byte) error {
	return nil
}

func (store *installOAuthStore) BeginGitHubOAuth(
	_ context.Context,
	installStateHash []byte,
	installation domain.GitHubInstallation,
	oauthStateHash, verifierCiphertext, verifierNonce []byte,
	expiresAt time.Time,
) (domain.GitHubInstallAttempt, error) {
	store.installStateHash = append([]byte(nil), installStateHash...)
	store.oauthStateHash = append([]byte(nil), oauthStateHash...)
	store.attempt = domain.GitHubInstallAttempt{
		Phase:                       "oauth",
		PendingGitHubInstallationID: installation.GitHubInstallationID,
		OAuthVerifierCiphertext:     verifierCiphertext,
		OAuthVerifierNonce:          verifierNonce,
		ExpiresAt:                   expiresAt,
	}
	return store.attempt, nil
}

func (store *installOAuthStore) GitHubOAuthAttempt(_ context.Context, _ []byte) (domain.GitHubInstallAttempt, error) {
	return store.attempt, nil
}

func (store *installOAuthStore) CompleteGitHubInstallation(
	_ context.Context,
	_ []byte,
	installation domain.GitHubInstallation,
) (domain.GitHubInstallation, error) {
	installation.ID = "installation-record"
	return installation, nil
}

func TestCompleteInstallationOAuthUsesInstallationStateWithoutPKCE(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.Path {
		case "/app/installations/42":
			body = `{"id":42,"account":{"id":7,"login":"octocat","type":"User"},"repository_selection":"selected","permissions":{},"events":[]}`
		case "/login/oauth/access_token":
			body = `{"access_token":"github-token"}`
		case "/user/installations":
			body = `{"installations":[{"id":42}]}`
		case "/user":
			body = `{"id":7}`
		default:
			t.Fatalf("unexpected GitHub request: %s", request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})
	client := &Client{
		appID: 1, clientID: "client-id", clientSecret: "client-secret",
		privateKey: privateKey, publicURL: "https://cloud.example.test",
		apiBaseURL: "https://api.github.test", webBaseURL: "https://github.test",
		httpClient: &http.Client{Transport: transport}, now: time.Now,
	}
	store := &installOAuthStore{}
	service, err := NewService(
		store, client, bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32),
		"webhook-secret", time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	installation, err := service.CompleteInstallationOAuth(context.Background(), "install-state", "oauth-code", 42)
	if err != nil {
		t.Fatalf("complete installation OAuth: %v", err)
	}
	if installation.ID != "installation-record" {
		t.Fatalf("installation ID = %q, want installation-record", installation.ID)
	}
	if !bytes.Equal(store.installStateHash, store.oauthStateHash) {
		t.Fatal("direct installation authorization did not retain the installation state")
	}
	verifier, err := Decrypt(
		bytes.Repeat([]byte{1}, 32), store.attempt.OAuthVerifierCiphertext,
		store.attempt.OAuthVerifierNonce, []byte("42"),
	)
	if err != nil {
		t.Fatalf("decrypt verifier: %v", err)
	}
	if len(verifier) != 0 {
		t.Fatalf("verifier = %q, want empty", verifier)
	}
}

// installOAuthConnectStore adds what offering and confirming the user's other
// installations after the callback reaches: the AO side of the offer, keeping
// the user's authorization, binding, and a user-scoped sync that reads that
// authorization back. Confirmed connects run in the background, so it locks.
type installOAuthConnectStore struct {
	installOAuthStore
	onlyMember bool
	linked     *domain.GitHubUserConnection

	mu         sync.Mutex
	completed  []domain.GitHubInstallation
	saved      *postgres.GitHubUserConnectionInput
	bound      []domain.GitHubInstallation
	reconciled [][]domain.GitHubRepository
}

func (store *installOAuthConnectStore) GitHubOAuthAttempt(ctx context.Context, hash []byte) (domain.GitHubInstallAttempt, error) {
	attempt, err := store.installOAuthStore.GitHubOAuthAttempt(ctx, hash)
	attempt.OrgID, attempt.InitiatingUserID = "org-1", "user-1"
	return attempt, err
}

func (store *installOAuthConnectStore) CompleteGitHubInstallation(
	ctx context.Context, hash []byte, installation domain.GitHubInstallation,
) (domain.GitHubInstallation, error) {
	store.mu.Lock()
	store.completed = append(store.completed, installation)
	store.mu.Unlock()
	return store.installOAuthStore.CompleteGitHubInstallation(ctx, hash, installation)
}

func (store *installOAuthConnectStore) GitHubConnectOthersTarget(context.Context, string, string) (string, bool, error) {
	return "member@example.com", store.onlyMember, nil
}

func (store *installOAuthConnectStore) GitHubUserScopeIntact(context.Context, string, string) (bool, error) {
	return store.onlyMember, nil
}

func (store *installOAuthConnectStore) SaveGitHubUserConnection(
	_ context.Context, userID string, input postgres.GitHubUserConnectionInput,
) (domain.GitHubUserConnection, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saved = &input
	return domain.GitHubUserConnection{UserID: userID, GitHubUserID: input.GitHubUserID}, nil
}

func (store *installOAuthConnectStore) GitHubUserConnection(_ context.Context, userID string) (domain.GitHubUserConnection, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.linked != nil {
		return *store.linked, nil
	}
	if store.saved == nil {
		return domain.GitHubUserConnection{}, postgres.ErrNotFound
	}
	return domain.GitHubUserConnection{
		UserID: userID, GitHubUserID: store.saved.GitHubUserID,
		AccessTokenCiphertext: store.saved.AccessTokenCiphertext, AccessTokenNonce: store.saved.AccessTokenNonce,
		AccessTokenExpiresAt: store.saved.AccessTokenExpiresAt,
	}, nil
}

func (store *installOAuthConnectStore) BindGitHubInstallation(
	_ context.Context, _ domain.Principal, orgID string, installation domain.GitHubInstallation,
) (domain.GitHubInstallation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	installation.ID = "bound-" + installation.AccountLogin
	installation.OrgID = orgID
	store.bound = append(store.bound, installation)
	return installation, nil
}

func (store *installOAuthConnectStore) BeginGitHubRepositorySync(context.Context, domain.GitHubInstallation) (int64, error) {
	return 1, nil
}

func (store *installOAuthConnectStore) ReconcileGitHubRepositories(
	_ context.Context, _ string, _ domain.GitHubInstallation, _ int64, repositories []domain.GitHubRepository,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reconciled = append(store.reconciled, repositories)
	return nil
}

func (store *installOAuthConnectStore) snapshot() (*postgres.GitHubUserConnectionInput, []domain.GitHubInstallation, [][]domain.GitHubRepository) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.saved, append([]domain.GitHubInstallation(nil), store.bound...),
		append([][]domain.GitHubRepository(nil), store.reconciled...)
}

// newConnectOthersTestService serves the user "octocat" with their own
// account (installation 42, just installed) and a GitHub organization "acme"
// they belong to without owning (installation 99).
func newConnectOthersTestService(t *testing.T, store *installOAuthConnectStore) *Service {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	repository := `{"id":501,"owner":{"id":900,"login":"acme","type":"Organization"},"name":"agent-orchestrator",` +
		`"full_name":"acme/agent-orchestrator","html_url":"https://github.com/acme/agent-orchestrator",` +
		`"clone_url":"https://github.com/acme/agent-orchestrator.git","default_branch":"main","updated_at":"2026-01-01T00:00:00Z"}`
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var body string
		switch request.URL.Path {
		case "/app/installations/42":
			body = `{"id":42,"account":{"id":7,"login":"octocat","type":"User"},"repository_selection":"selected","permissions":{},"events":[]}`
		case "/app/installations/99":
			body = `{"id":99,"account":{"id":900,"login":"acme","type":"Organization"},"repository_selection":"selected","permissions":{"members":"read"},"events":[]}`
		case "/login/oauth/access_token":
			body = `{"access_token":"github-token"}`
		case "/user/installations":
			body = `{"installations":[{"id":42,"account":{"id":7,"login":"octocat","type":"User"}},{"id":99,"account":{"id":900,"login":"acme","type":"Organization"}}]}`
		case "/user":
			body = `{"id":7,"login":"octocat"}`
		case "/user/memberships/orgs/acme":
			body = `{"state":"active","role":"member"}`
		case "/user/installations/99/repositories":
			body = `{"total_count":1,"repositories":[` + repository + `]}`
		default:
			t.Errorf("unexpected GitHub request: %s", request.URL.String())
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewBufferString(`{}`))}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(body)),
		}, nil
	})
	client := &Client{
		appID: 1, clientID: "client-id", clientSecret: "client-secret",
		privateKey: privateKey, publicURL: "https://cloud.example.test",
		apiBaseURL: "https://api.github.test", webBaseURL: "https://github.test",
		httpClient: &http.Client{Transport: transport}, now: time.Now,
	}
	service, err := NewService(
		store, client, bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32),
		"webhook-secret", time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service
}

// The installation callback connects only the installation the user chose. It
// offers the user's other GitHub accounts but keeps nothing until the person
// in the browser confirms; the confirmation then connects a GitHub
// organization they belong to without owning, user-scoped, in the background.
func TestInstallationCallbackOffersOtherInstallationsUntilConfirmed(t *testing.T) {
	t.Parallel()
	store := &installOAuthConnectStore{onlyMember: true}
	service := newConnectOthersTestService(t, store)

	installation, offer, err := service.CompleteInstallationOAuthWithOffer(context.Background(), "install-state", "oauth-code", 42)
	if err != nil {
		t.Fatalf("complete installation OAuth: %v", err)
	}
	if installation.ID != "installation-record" {
		t.Fatalf("installation ID = %q", installation.ID)
	}
	if len(store.completed) != 1 || store.completed[0].AccessScope != domain.GitHubAccessScopeInstallation {
		t.Fatalf("completed = %+v, want the chosen installation connected whole", store.completed)
	}
	if offer == nil || offer.GitHubLogin != "octocat" || offer.AOAccount != "member@example.com" ||
		len(offer.Accounts) != 1 || offer.Accounts[0] != "acme" {
		t.Fatalf("offer = %+v, want octocat offering acme to member@example.com", offer)
	}
	if saved, bound, _ := store.snapshot(); saved != nil || len(bound) != 0 {
		t.Fatalf("kept %+v and bound %+v before any confirmation", saved, bound)
	}
	page := string(service.InstallationOfferHTML(*offer))
	for _, want := range []string{"octocat", "member@example.com", "acme", `action="https://cloud.example.test/api/cloud/v1/github/oauth/connect-others"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("offer page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "github-token") {
		t.Fatal("offer page shows the GitHub token in the clear")
	}

	if err := service.ConfirmConnectOthers(context.Background(), offer.Grant); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		saved, bound, reconciled := store.snapshot()
		if saved == nil || saved.GitHubUserID != 7 {
			t.Fatalf("the user's GitHub authorization was not kept: %+v", saved)
		}
		if len(reconciled) == 1 {
			if len(bound) != 1 || bound[0].AccountLogin != "acme" ||
				bound[0].AccessScope != domain.GitHubAccessScopeUser || bound[0].ScopeUserID != "user-1" {
				t.Fatalf("bound = %+v, want acme user-scoped to user-1 (and not the installation just connected)", bound)
			}
			if len(reconciled[0]) != 1 || reconciled[0][0].FullName != "acme/agent-orchestrator" {
				t.Fatalf("reconciled = %+v, want acme/agent-orchestrator", reconciled)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background connect never finished: bound %+v", bound)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Nothing is offered into an organization with other members, nor when a
// different GitHub account is already linked to the AO user.
func TestInstallationCallbackOffersNothingWhenItWouldShareOrReplace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		store *installOAuthConnectStore
	}{
		{"organization with other members", &installOAuthConnectStore{onlyMember: false}},
		{"a different GitHub account linked", &installOAuthConnectStore{onlyMember: true,
			linked: &domain.GitHubUserConnection{UserID: "user-1", GitHubUserID: 8}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newConnectOthersTestService(t, tc.store)
			_, offer, err := service.CompleteInstallationOAuthWithOffer(context.Background(), "install-state", "oauth-code", 42)
			if err != nil {
				t.Fatalf("complete installation OAuth: %v", err)
			}
			if offer != nil {
				t.Fatalf("offer = %+v, want none", offer)
			}
		})
	}
}

// A confirmation acts only on a grant this service sealed, while it is fresh,
// for an organization still the user's alone and the same GitHub account.
func TestConfirmConnectOthersRefuses(t *testing.T) {
	t.Parallel()
	store := &installOAuthConnectStore{onlyMember: true}
	service := newConnectOthersTestService(t, store)
	_, offer, err := service.CompleteInstallationOAuthWithOffer(context.Background(), "install-state", "oauth-code", 42)
	if err != nil || offer == nil {
		t.Fatalf("offer = %+v, err = %v", offer, err)
	}
	expired, err := service.sealConnectOthersGrant(connectOthersGrant{
		OrgID: "org-1", UserID: "user-1", GitHubUserID: 7, AccessToken: "github-token",
		IssuedAt: time.Now().Add(-connectOthersGrantTTL - time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		grant  string
		mutate func()
		want   error
	}{
		{"forged", "bm9uY2U.Y2lwaGVydGV4dA", nil, postgres.ErrInvalid},
		{"tampered", offer.Grant[:len(offer.Grant)-2] + "AA", nil, postgres.ErrInvalid},
		{"expired", expired, nil, ErrConnectOthersExpired},
		{"organization gained a member", offer.Grant, func() { store.onlyMember = false }, postgres.ErrGitHubUserScopeNeedsPersonalOrg},
		{"different GitHub account linked since", offer.Grant, func() {
			store.onlyMember = true
			store.linked = &domain.GitHubUserConnection{UserID: "user-1", GitHubUserID: 8}
		}, postgres.ErrConflict},
	} {
		if tc.mutate != nil {
			tc.mutate()
		}
		if err := service.ConfirmConnectOthers(context.Background(), tc.grant); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if saved, bound, _ := store.snapshot(); saved != nil || len(bound) != 0 {
		t.Fatalf("a refused confirmation kept %+v and bound %+v", saved, bound)
	}
}
