package accountsmanager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestManagementClientStreamsSafeOAuthEvents(t *testing.T) {
	t.Parallel()
	expiresAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{
		BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret",
	}, ready: true}, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/ao/internal/oauth/events" || req.Header.Get("Authorization") != "Bearer management-secret" {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
		}
		body := ": heartbeat\n\nevent: oauth_session\ndata: {\"provider\":\"codex\",\"state\":\"opaque-state\",\"status\":\"completed\",\"expiresAt\":\"" + expiresAt.Format(time.RFC3339) + "\"}\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})})

	var events []OAuthEvent
	err := client.StreamOAuthEvents(context.Background(), func(event OAuthEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamOAuthEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Provider != ProviderCodex || events[0].State != "opaque-state" || events[0].Status != OAuthCompleted || !events[0].ExpiresAt.Equal(expiresAt) {
		t.Fatalf("events = %#v", events)
	}
}

func TestManagementClientOAuthEventStreamRejectsWrongContentType(t *testing.T) {
	t.Parallel()
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true}, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})})
	if err := client.StreamOAuthEvents(context.Background(), func(OAuthEvent) error { return nil }); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("StreamOAuthEvents() error = %v", err)
	}
}

func TestManagementClientBoundsWholeOAuthEvent(t *testing.T) {
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		body := strings.Repeat("data: "+strings.Repeat("x", 1024)+"\n", managementResponseLimit/1024+1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	err := client.StreamOAuthEvents(context.Background(), func(OAuthEvent) error { return nil })
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("oversized multi-line event = %v", err)
	}
}

func TestDecodeOAuthEventPreservesSafeDeviceInstructions(t *testing.T) {
	t.Parallel()
	expiresAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	event, err := decodeOAuthEvent([]byte(`{"provider":"codex","mode":"device","state":"opaque-state","status":"pending","authorizationUrl":"https://auth.openai.com/codex/device","userCode":"ABCD-EFGH","expiresAt":"` + expiresAt.Format(time.RFC3339) + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Mode != OAuthModeDevice || event.AuthorizationURL != codexDeviceVerificationURLForTest || event.UserCode != "ABCD-EFGH" {
		t.Fatalf("event = %#v", event)
	}
}

const codexDeviceVerificationURLForTest = "https://auth.openai.com/codex/device"

func TestManagementClientPublicIDsAreStableScopedAndOpaque(t *testing.T) {
	t.Parallel()
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true}, nil)
	first, err := client.CredentialPublicID("sensitive-auth-index")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := client.CredentialPublicID("sensitive-auth-index")
	oauth, _ := client.OAuthPublicID("sensitive-auth-index")
	if first != again || first == oauth || strings.Contains(first, "sensitive") || !strings.HasPrefix(first, "amc_") || !strings.HasPrefix(oauth, "amo_") {
		t.Fatalf("unsafe or unstable IDs: %q %q %q", first, again, oauth)
	}
}
