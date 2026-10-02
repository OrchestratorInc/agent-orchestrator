package accountsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestManagementClientOAuthLifecycle(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	var calls []string
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.RequestURI())
		if req.Header.Get("Authorization") != "Bearer management-secret" {
			t.Fatal("missing management authentication")
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/ao/internal/oauth/start":
			body, _ := io.ReadAll(req.Body)
			var input map[string]string
			if json.Unmarshal(body, &input) != nil || len(input) != 2 || input["mode"] != "device" || input["provider"] != "codex" {
				t.Fatalf("start body = %s", body)
			}
			return managementJSONResponse(req, http.StatusOK, `{"provider":"codex","mode":"device","state":"opaque-state","authorizationUrl":"https://auth.example.test/start","userCode":"ABCD-EFGH","expiresAt":"`+expiresAt.Format(time.RFC3339)+`"}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/ao/internal/oauth/status":
			if req.URL.Query().Get("state") != "opaque-state" {
				t.Fatal("status omitted state")
			}
			return managementJSONResponse(req, http.StatusOK, `{"status":"completed"}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/ao/internal/oauth/session":
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		default:
			return managementJSONResponse(req, http.StatusNotFound, `{}`), nil
		}
	})
	client := NewManagementClient(
		staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true},
		&http.Client{Transport: transport},
	)

	session, err := client.StartOAuth(context.Background(), ProviderCodex, OAuthModeDevice)
	if err != nil {
		t.Fatalf("StartOAuth() error = %v", err)
	}
	if session.Provider != ProviderCodex || session.Mode != OAuthModeDevice || session.UserCode != "ABCD-EFGH" || session.State != "opaque-state" || session.AuthorizationURL != "https://auth.example.test/start" || !session.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("StartOAuth() = %#v", session)
	}
	status, err := client.GetOAuthStatus(context.Background(), session.State)
	if err != nil || status.State != OAuthCompleted {
		t.Fatalf("GetOAuthStatus() = %#v, %v", status, err)
	}
	if err = client.CancelOAuth(context.Background(), session.State); err != nil {
		t.Fatalf("CancelOAuth() error = %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestManagementClientOAuthValidationAndSafeMappings(t *testing.T) {
	t.Parallel()

	client := NewManagementClient(
		staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true},
		&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/ao/internal/oauth/start":
				return managementJSONResponse(req, http.StatusOK, `{"provider":"codex","mode":"callback","state":"opaque","authorizationUrl":"http://unsafe.example.test","expiresAt":"2026-09-20T12:00:00Z"}`), nil
			case "/ao/internal/oauth/status":
				return managementJSONResponse(req, http.StatusOK, `{"status":"failed","error":"upstream-secret-error"}`), nil
			default:
				return managementJSONResponse(req, http.StatusNotFound, `{}`), nil
			}
		})},
	)
	if _, err := client.StartOAuth(context.Background(), Provider("gemini"), OAuthModeCallback); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("unsupported StartOAuth() error = %v", err)
	}
	if _, err := client.StartOAuth(context.Background(), ProviderCodex, OAuthModeCallback); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("unsafe URL StartOAuth() error = %v", err)
	}
	status, err := client.GetOAuthStatus(context.Background(), "opaque")
	if err != nil || status.State != OAuthFailed || status.FailureCode != "authentication_failed" {
		t.Fatalf("failed GetOAuthStatus() = %#v, %v", status, err)
	}
	if strings.Contains(status.FailureCode, "upstream") {
		t.Fatal("OAuth failure exposed upstream error")
	}
	if err := client.CancelOAuth(context.Background(), ""); err != nil {
		t.Fatalf("empty-state cancellation should be idempotent: %v", err)
	}
}

func TestManagementClientOAuthExpiredMapping(t *testing.T) {
	t.Parallel()

	client := NewManagementClient(
		staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true},
		&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return managementJSONResponse(req, http.StatusOK, `{"status":"expired"}`), nil
		})},
	)
	status, err := client.GetOAuthStatus(context.Background(), "opaque")
	if !errors.Is(err, ErrOAuthExpired) || status.State != OAuthExpired {
		t.Fatalf("GetOAuthStatus() = %#v, %v, want expired", status, err)
	}
}

func managementJSONResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
