package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
)

func TestClientTimeZoneMiddlewareRecordsHeader(t *testing.T) {
	dir := t.TempDir()
	h := clientTimeZoneMiddleware(clienttz.NewRecorder(context.Background(), dir))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil)
	req.Header.Set(clienttz.Header, "Pacific/Chatham")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := clienttz.Read(context.Background(), dir); got != "Pacific/Chatham" {
		t.Fatalf("recorded zone = %q, want Pacific/Chatham", got)
	}

	// Requests without the header leave the recorded zone alone.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
	if got := clienttz.Read(context.Background(), dir); got != "Pacific/Chatham" {
		t.Fatalf("recorded zone = %q after headerless request", got)
	}
}

func TestClientTimeZoneMiddlewareNilRecorder(t *testing.T) {
	h := clientTimeZoneMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestClientTimeZoneMiddlewareIgnoresUntrustedLANRequests(t *testing.T) {
	dir := t.TempDir()
	h := clientTimeZoneMiddleware(clienttz.NewRecorder(context.Background(), dir))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	send := func(method, path string, credential requestscope.Credential, zone string) {
		ctx := requestscope.WithLAN(context.Background())
		if credential.Kind != "" {
			ctx = requestscope.WithCredential(ctx, credential)
		}
		req := httptest.NewRequest(method, path, nil).WithContext(ctx)
		req.Header.Set(clienttz.Header, zone)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	// The unauthenticated identity probe and a pairing-code request are ignored.
	send(http.MethodGet, identityProbePath, requestscope.Credential{}, "Pacific/Chatham")
	send(http.MethodPost, devicePairPath, requestscope.Credential{Kind: requestscope.CredentialPairingCode}, "Pacific/Chatham")
	if got := clienttz.Read(context.Background(), dir); got != "" {
		t.Fatalf("untrusted LAN request recorded %q", got)
	}

	// An authenticated device is trusted.
	send(http.MethodPost, "/api/v1/sessions", requestscope.Credential{Kind: requestscope.CredentialDevice, DeviceID: "d1"}, "Asia/Kolkata")
	if got := clienttz.Read(context.Background(), dir); got != "Asia/Kolkata" {
		t.Fatalf("authenticated LAN request recorded %q, want Asia/Kolkata", got)
	}
}
