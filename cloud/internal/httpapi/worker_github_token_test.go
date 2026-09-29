package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// pushGrantBroker returns a configurable App push grant so workerGitHubToken's
// App-first preference can be exercised without a real GitHub App.
type pushGrantBroker struct {
	recordingCheckoutBroker
	push      githubapp.CheckoutGrant
	pushErr   error
	pushCalls int
}

func (b *pushGrantBroker) IssuePushGrant(context.Context, string, string) (githubapp.CheckoutGrant, error) {
	b.pushCalls++
	return b.push, b.pushErr
}

func newGitHubTokenServer(t *testing.T, push githubapp.CheckoutGrant, pushErr error, withPAT bool) (*Server, *pushGrantBroker) {
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
	broker := &pushGrantBroker{push: push, pushErr: pushErr}
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

// The sandbox git credential helper calls /worker/github-token for every fetch
// and push. It must prefer the App push grant over a stored PAT — preferring the
// PAT is exactly what let a rotted PAT (validation_state is a cached snapshot)
// shadow a healthy App installation and fail every push with "Authentication
// failed", even though the App can push. Regression test for that bug.
func TestWorkerGitHubTokenPrefersAppOverPAT(t *testing.T) {
	appPush := githubapp.CheckoutGrant{
		CloneURL:  "https://github.com/octo/widgets.git",
		Token:     "APP_PUSH_TOKEN",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	srv, broker := newGitHubTokenServer(t, appPush, nil, true)
	w := httptest.NewRecorder()
	srv.workerGitHubToken(w, gitHubTokenRequest(t))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if broker.pushCalls != 1 {
		t.Fatalf("IssuePushGrant called %d times, want 1 (App tried first)", broker.pushCalls)
	}
	var resp worker.GitHubTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Token != "APP_PUSH_TOKEN" {
		t.Fatalf("token = %q, want the App token (a PAT must not shadow a healthy App grant)", resp.Token)
	}
}

// When the App path cannot serve the project, fall back to the stored PAT.
func TestWorkerGitHubTokenFallsBackToPAT(t *testing.T) {
	srv, broker := newGitHubTokenServer(t, githubapp.CheckoutGrant{}, postgres.ErrForbidden, true)
	w := httptest.NewRecorder()
	srv.workerGitHubToken(w, gitHubTokenRequest(t))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (PAT fallback); body=%s", w.Code, w.Body.String())
	}
	if broker.pushCalls != 1 {
		t.Fatalf("IssuePushGrant called %d times, want 1", broker.pushCalls)
	}
	var resp worker.GitHubTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Token != grantTestPAT {
		t.Fatalf("token = %q, want the PAT fallback", resp.Token)
	}
}

// No App grant and no PAT: surface the App error (403), not a silent fallback.
func TestWorkerGitHubTokenForbiddenWithoutAppOrPAT(t *testing.T) {
	srv, _ := newGitHubTokenServer(t, githubapp.CheckoutGrant{}, postgres.ErrForbidden, false)
	w := httptest.NewRecorder()
	srv.workerGitHubToken(w, gitHubTokenRequest(t))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}
