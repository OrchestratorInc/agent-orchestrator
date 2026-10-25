package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/githubapp"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
)

// checkoutGrantBroker returns a configurable App checkout grant, without a
// real GitHub App.
type checkoutGrantBroker struct {
	recordingCheckoutBroker
	checkout      githubapp.CheckoutGrant
	checkoutErr   error
	checkoutCalls int
}

func (b *checkoutGrantBroker) IssueCheckoutGrant(context.Context, string, string) (githubapp.CheckoutGrant, error) {
	b.checkoutCalls++
	return b.checkout, b.checkoutErr
}

const grantTestOwnerID = "11111111-1111-1111-1111-111111111111"
const grantTestPAT = "ghp_CHECKOUTtestPAT00000000000000000"

func newCheckoutGrantServer(t *testing.T, checkout githubapp.CheckoutGrant, checkoutErr error, withPAT bool) (*Server, *checkoutGrantBroker) {
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
	broker := &checkoutGrantBroker{checkout: checkout, checkoutErr: checkoutErr}
	srv := New(Options{
		Store:          store,
		SecretCipher:   cipher,
		CheckoutBroker: broker,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return srv, broker
}

func checkoutGrantRequest(t *testing.T) *http.Request {
	return workerRequest(t, http.MethodPost, "/worker/checkout-grant", "", "worker:git")
}

// The checkout grant names the repository only: the App installation token the
// broker minted to confirm the grant stays on the control plane, since the
// clone goes through the git proxy.
func TestWorkerCheckoutGrantCarriesNoCredential(t *testing.T) {
	appGrant := githubapp.CheckoutGrant{
		CloneURL:  "https://github.com/octo/widgets.git",
		Token:     "APP_INSTALLATION_TOKEN",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	srv, broker := newCheckoutGrantServer(t, appGrant, nil, true)
	w := httptest.NewRecorder()
	srv.workerCheckoutGrant(w, checkoutGrantRequest(t))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if broker.checkoutCalls != 1 {
		t.Fatalf("IssueCheckoutGrant called %d times, want 1", broker.checkoutCalls)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp) != 1 || resp["cloneUrl"] != appGrant.CloneURL {
		t.Fatalf("response = %v, want the clone URL alone", resp)
	}
	assertNoGitHubCredential(t, w.Body.String(), appGrant.Token, grantTestPAT)
}

// A session the App cannot serve gets no credential at all: the creator's
// stored personal access token is never handed to the sandbox.
func TestWorkerCheckoutGrantNeverHandsOutAPAT(t *testing.T) {
	srv, broker := newCheckoutGrantServer(t, githubapp.CheckoutGrant{}, postgres.ErrForbidden, true)
	w := httptest.NewRecorder()
	srv.workerCheckoutGrant(w, checkoutGrantRequest(t))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	if broker.checkoutCalls != 1 {
		t.Fatalf("IssueCheckoutGrant called %d times, want 1", broker.checkoutCalls)
	}
	assertNoGitHubCredential(t, w.Body.String(), grantTestPAT)

	noBroker := New(Options{Store: &patServerStore{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w = httptest.NewRecorder()
	noBroker.workerCheckoutGrant(w, checkoutGrantRequest(t))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status without a broker = %d, want 503; body=%s", w.Code, w.Body.String())
	}
}

// assertNoGitHubCredential fails when a worker-facing response carries any
// GitHub credential: the given secrets, or anything shaped like a GitHub token.
func assertNoGitHubCredential(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(body, secret) {
			t.Fatalf("a GitHub credential reached the worker: %s", body)
		}
	}
	for _, prefix := range []string{"ghp_", "ghs_", "ghu_", "gho_", "ghr_", "github_pat_", "x-access-token"} {
		if strings.Contains(body, prefix) {
			t.Fatalf("a GitHub credential (%s...) reached the worker: %s", prefix, body)
		}
	}
	if strings.Contains(strings.ToLower(body), `"token"`) {
		t.Fatalf("a worker-facing response carries a token field: %s", body)
	}
}
