package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/webhook"
)

type fakeWebhookDelivery struct{ tested string }

func (f *fakeWebhookDelivery) Test(_ context.Context, name string) error {
	f.tested = name
	return nil
}

func (f *fakeWebhookDelivery) Statuses() map[string]webhook.Status {
	return map[string]webhook.Status{"main": {Sent: 2}}
}

func TestWebhookRoutesHideSecrets(t *testing.T) {
	store, err := webhook.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	delivery := &fakeWebhookDelivery{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Webhooks: store, WebhookDelivery: delivery}, httpd.ControlDeps{}))
	defer srv.Close()

	call := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := call(http.MethodPut, "/api/v1/webhooks/main", `{"url":"http://example.com/x"}`); code != http.StatusBadRequest || !strings.Contains(body, "INVALID_WEBHOOK") {
		t.Fatalf("plain http PUT = %d %s", code, body)
	}
	if code, body := call(http.MethodPut, "/api/v1/webhooks/main", `{"url":"https://bots.example/hook/abc?key=zzz","headers":{"Authorization":"Bearer topsecret"}}`); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if code, body := call(http.MethodPatch, "/api/v1/webhooks/main", `{"events":["needs_input","finished"]}`); code != http.StatusOK || !strings.Contains(body, `"needs_input"`) {
		t.Fatalf("PATCH = %d %s", code, body)
	}
	code, body := call(http.MethodGet, "/api/v1/webhooks", "")
	if code != http.StatusOK || !strings.Contains(body, `"host":"bots.example"`) || !strings.Contains(body, `"Authorization"`) || !strings.Contains(body, `"sent":2`) {
		t.Fatalf("GET = %d %s", code, body)
	}
	for _, secret := range []string{"topsecret", "/hook/abc", "zzz"} {
		if strings.Contains(body, secret) {
			t.Fatalf("list leaked %q: %s", secret, body)
		}
	}
	if h, _ := store.Get("main"); h.Headers["Authorization"] != "Bearer topsecret" {
		t.Fatalf("PATCH dropped the header: %+v", h)
	}
	if code, body := call(http.MethodPost, "/api/v1/webhooks/main/test", ""); code != http.StatusOK || !strings.Contains(body, `"ok":true`) || delivery.tested != "main" {
		t.Fatalf("test = %d %s", code, body)
	}
	if code, _ := call(http.MethodDelete, "/api/v1/webhooks/main", ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if code, body := call(http.MethodDelete, "/api/v1/webhooks/main", ""); code != http.StatusNotFound || !strings.Contains(body, "WEBHOOK_NOT_FOUND") {
		t.Fatalf("second DELETE = %d %s", code, body)
	}
	if !httpd.IsLANControlBlockedPathForTest("/api/v1/webhooks/main/test") {
		t.Fatal("webhook routes must not be reachable over the LAN listener")
	}
}
