package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
