package githubapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"log/slog"
	"net/http"
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

// installOAuthConnectStore adds what connecting the user's other installations
// after the callback reaches: keeping the user's authorization, binding, and a
// user-scoped sync that reads that authorization back.
type installOAuthConnectStore struct {
	installOAuthStore
	saved      *postgres.GitHubUserConnectionInput
	bound      []domain.GitHubInstallation
	reconciled [][]domain.GitHubRepository
}

func (store *installOAuthConnectStore) SaveGitHubUserConnection(
	_ context.Context, userID string, input postgres.GitHubUserConnectionInput,
) (domain.GitHubUserConnection, error) {
	store.saved = &input
	return domain.GitHubUserConnection{UserID: userID, GitHubUserID: input.GitHubUserID}, nil
}

func (store *installOAuthConnectStore) GitHubUserConnection(_ context.Context, userID string) (domain.GitHubUserConnection, error) {
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
	store.reconciled = append(store.reconciled, repositories)
	return nil
}

// Connecting the user's own account also connects a GitHub organization they
// are a member of but do not own, user-scoped to what they can access.
func TestCompleteInstallationOAuthConnectsTheUsersOtherInstallations(t *testing.T) {
	t.Parallel()

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
	store := &installOAuthConnectStore{}
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
		t.Fatalf("installation ID = %q", installation.ID)
	}
	// BeginGitHubOAuth (in installOAuthStore) leaves OrgID/user empty, so the
	// follow-up is skipped there; drive it directly with this attempt's identity.
	service.connectOtherUserInstallations(context.Background(), "org-1", "user-1", 42,
		UserAccessToken{value: "github-token"})
	if store.saved == nil || store.saved.GitHubUserID != 7 {
		t.Fatalf("the user's GitHub authorization was not kept: %+v", store.saved)
	}
	if len(store.bound) != 1 || store.bound[0].AccountLogin != "acme" ||
		store.bound[0].AccessScope != domain.GitHubAccessScopeUser || store.bound[0].ScopeUserID != "user-1" {
		t.Fatalf("bound = %+v, want acme user-scoped to user-1 (and not the installation just connected)", store.bound)
	}
	if len(store.reconciled) != 1 || len(store.reconciled[0]) != 1 || store.reconciled[0][0].FullName != "acme/agent-orchestrator" {
		t.Fatalf("reconciled = %+v, want acme/agent-orchestrator", store.reconciled)
	}
}
