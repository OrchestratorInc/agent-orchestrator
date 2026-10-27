package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// cliGrantBroker returns a configurable gh CLI grant without a real GitHub App.
type cliGrantBroker struct {
	recordingCheckoutBroker
	cli      githubapp.CheckoutGrant
	cliErr   error
	cliCalls int
}

func (b *cliGrantBroker) IssueCLIGrant(context.Context, string, string) (githubapp.CheckoutGrant, error) {
	b.cliCalls++
	return b.cli, b.cliErr
}

func newGitHubTokenServer(t *testing.T, cli githubapp.CheckoutGrant, cliErr error, withPAT bool) (*Server, *cliGrantBroker) {
	t.Helper()
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	store := &patServerStore{patErr: postgres.ErrNotFound}
	if withPAT {
		encrypted, nonce, err := cipher.Encrypt([]byte(grantTestPAT), providerSecretAssociatedData("user:"+grantTestOwnerID, githubPATProvider))
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		store = &patServerStore{pat: domain.WorkerGitHubPAT{
			OwnerUserID: grantTestOwnerID, CloneURL: "https://github.com/octo/widgets.git",
			EncryptedSecret: encrypted, Nonce: nonce,
		}}
	}
	broker := &cliGrantBroker{cli: cli, cliErr: cliErr}
	srv := New(Options{
		Store:          store,
		SecretCipher:   cipher,
		CheckoutBroker: broker,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return srv, broker
}

func gitHubTokenRequest(t *testing.T) *http.Request {
	return workerRequest(t, http.MethodPost, "/worker/github-token", "", "worker:git")
}

// The gh CLI wrapper's token is the App's read-and-pull-request grant: the one
// GitHub credential a sandbox still receives (see workerGitHubToken).
func TestWorkerGitHubTokenIssuesTheCLIGrant(t *testing.T) {
	cliGrant := githubapp.CheckoutGrant{
		CloneURL: "https://github.com/octo/widgets.git", Token: "CLI_INSTALLATION_TOKEN",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	srv, broker := newGitHubTokenServer(t, cliGrant, nil, true)
	w := httptest.NewRecorder()
	srv.workerGitHubToken(w, gitHubTokenRequest(t))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if broker.cliCalls != 1 {
		t.Fatalf("IssueCLIGrant called %d times, want 1", broker.cliCalls)
	}
	var resp worker.GitHubTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Token != cliGrant.Token {
		t.Fatalf("token = %q, want the CLI grant", resp.Token)
	}
}

// A session the App cannot serve gets no token: the creator's personal access
// token is never handed to the sandbox, whatever the request names.
func TestWorkerGitHubTokenNeverHandsOutAPAT(t *testing.T) {
	srv, _ := newGitHubTokenServer(t, githubapp.CheckoutGrant{}, postgres.ErrForbidden, true)
	for _, target := range []string{"/worker/github-token", "/worker/github-token?" + url.Values{"repo": {"octo/widgets"}}.Encode()} {
		w := httptest.NewRecorder()
		srv.workerGitHubToken(w, workerRequest(t, http.MethodPost, target, "", "worker:git"))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403; body=%s", target, w.Code, w.Body.String())
		}
		assertNoGitHubCredential(t, w.Body.String(), grantTestPAT)
	}
}

// gitProxyFlagStore records whether the worker was bootstrapped with the git
// proxy capability.
type gitProxyFlagStore struct {
	patServerStore
	gitProxy bool
}

func (s *gitProxyFlagStore) WorkerUsesGitProxy(context.Context, string, string, string, int64) (bool, error) {
	return s.gitProxy, nil
}

// legacyGrantBroker records the legacy push grants it issues.
type legacyGrantBroker struct {
	cliGrantBroker
	legacyRepos []string
}

func (b *legacyGrantBroker) IssueLegacyPushGrant(_ context.Context, _, _, repo string) (githubapp.CheckoutGrant, error) {
	b.legacyRepos = append(b.legacyRepos, repo)
	return githubapp.CheckoutGrant{
		CloneURL: "https://github.com/octo/widgets.git", Token: "LEGACY_WRITE_TOKEN", ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

func newLegacyGrantServer(t *testing.T, gitProxy bool) (*Server, *legacyGrantBroker) {
	t.Helper()
	broker := &legacyGrantBroker{cliGrantBroker: cliGrantBroker{cli: githubapp.CheckoutGrant{
		Token: "CLI_INSTALLATION_TOKEN", ExpiresAt: time.Now().Add(time.Hour),
	}}}
	srv := New(Options{
		Store:          &gitProxyFlagStore{patServerStore: patServerStore{patErr: postgres.ErrNotFound}, gitProxy: gitProxy},
		CheckoutBroker: broker,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return srv, broker
}

// A worker bootstrapped before the git proxy keeps its push token (its running
// credential helper and pull request bridge still ask for one) until its
// sandbox is recreated.
func TestPreProxyWorkersStillGetAPushToken(t *testing.T) {
	srv, broker := newLegacyGrantServer(t, false)
	w := httptest.NewRecorder()
	srv.workerGitHubToken(w, workerRequest(t, http.MethodPost, "/worker/github-token?repo=octo/widgets", "", "worker:git"))
	var token worker.GitHubTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil || w.Code != http.StatusOK || token.Token != "LEGACY_WRITE_TOKEN" {
		t.Fatalf("github-token = %d %s, want the legacy push token", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.workerLegacyPushGrant(w, workerRequest(t, http.MethodPost, "/worker/push-grant", "", "worker:git"))
	var grant map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &grant); err != nil || w.Code != http.StatusOK ||
		grant["token"] != "LEGACY_WRITE_TOKEN" || grant["cloneUrl"] != "https://github.com/octo/widgets.git" {
		t.Fatalf("push-grant = %d %s, want the legacy push grant", w.Code, w.Body.String())
	}
	if strings.Join(broker.legacyRepos, ",") != "octo/widgets," {
		t.Fatalf("legacy grants for %q, want the helper's repository then the bridge's broad grant", broker.legacyRepos)
	}
	if broker.cliCalls != 0 {
		t.Fatalf("a pre-proxy worker got the CLI grant %d times", broker.cliCalls)
	}
}

// A current worker never gets a push token, whatever its request says.
func TestCurrentWorkersNeverGetAPushToken(t *testing.T) {
	srv, broker := newLegacyGrantServer(t, true)
	w := httptest.NewRecorder()
	srv.workerGitHubToken(w, workerRequest(t, http.MethodPost, "/worker/github-token?repo=octo/widgets", "", "worker:git"))
	var token worker.GitHubTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil || token.Token != "CLI_INSTALLATION_TOKEN" {
		t.Fatalf("github-token = %d %s, want the CLI grant", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.workerLegacyPushGrant(w, workerRequest(t, http.MethodPost, "/worker/push-grant", "", "worker:git"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("push-grant for a current worker = %d %s, want 404", w.Code, w.Body.String())
	}
	assertNoGitHubCredential(t, w.Body.String(), "LEGACY_WRITE_TOKEN")
	if len(broker.legacyRepos) != 0 {
		t.Fatalf("a current worker got legacy push grants: %q", broker.legacyRepos)
	}
}

func TestWorkerRequestedRepository(t *testing.T) {
	for raw, want := range map[string]string{
		"octo/app": "octo/app", "octo/app.git": "octo/app", "/octo/app/": "octo/app",
		"": "", "octo": "", "octo/app/extra": "", "octo/ap p": "",
	} {
		r := httptest.NewRequest(http.MethodPost, "/worker/github-token?"+url.Values{"repo": {raw}}.Encode(), nil)
		if got := workerRequestedRepository(r); got != want {
			t.Errorf("workerRequestedRepository(%q) = %q, want %q", raw, got, want)
		}
	}
}
