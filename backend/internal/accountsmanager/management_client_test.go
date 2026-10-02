package accountsmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type staticEndpointSource struct {
	endpoint Endpoint
	ready    bool
}

func (s staticEndpointSource) Endpoint() (Endpoint, bool) { return s.endpoint, s.ready }

func TestManagementClientRoutingUsesManagementAuthentication(t *testing.T) {
	t.Parallel()

	const managementToken = "management-secret-do-not-expose"
	for _, want := range []RoutingStrategy{RoutingRoundRobin, RoutingWeightedRoundRobin, RoutingFillFirst} {
		t.Run(string(want), func(t *testing.T) {
			t.Parallel()
			authorized := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v0/management/routing/strategy" {
					http.NotFound(w, r)
					return
				}
				authorized = r.Header.Get("Authorization") == "Bearer "+managementToken && r.Header.Get("Accept") == "application/json"
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"strategy": string(want)})
			}))
			defer server.Close()

			client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: managementToken}, ready: true}, server.Client())
			got, err := client.RoutingStrategy(context.Background())
			if err != nil {
				t.Fatalf("RoutingStrategy() error = %v", err)
			}
			if !authorized {
				t.Fatal("request did not use the private management authorization contract")
			}
			if got != want {
				t.Fatalf("RoutingStrategy() = %q, want %q", got, want)
			}
		})
	}
}

func TestManagementClientMintsPrivateRouteCapability(t *testing.T) {
	t.Parallel()
	const managementToken = "management-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ao/internal/routes/token" || r.Header.Get("Authorization") != "Bearer "+managementToken {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input["provider"] != "codex" || input["authIndex"] != "private-ref" || input["sessionId"] != "session-1" || input["accountId"] != "public-id" || input["bindingRevision"] != float64(7) {
			t.Fatalf("route request = %#v", input)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"baseUrl":"`+serverURL(r)+`","token":"opaque-route-token"}`)
	}))
	defer server.Close()

	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: managementToken}, ready: true}, server.Client())
	got, err := client.MintRoute(context.Background(), ProviderCodex, "private-ref", "session-1", "public-id", 7)
	if err != nil {
		t.Fatalf("MintRoute() error = %v", err)
	}
	if got.BaseURL != server.URL || got.Token != "opaque-route-token" {
		t.Fatalf("MintRoute() = %#v", got)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestManagementClientUnavailableDoesNotSendRequest(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: "unused"}}, server.Client())
	_, err := client.RoutingStrategy(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("RoutingStrategy() error = %v, want ErrUnavailable", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("requests = %d, want 0", calls.Load())
	}
}

func TestManagementClientRejectsNonLoopbackEndpoint(t *testing.T) {
	t.Parallel()

	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: "https://example.com", ManagementToken: "secret"}, ready: true}, nil)
	_, err := client.RoutingStrategy(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("RoutingStrategy() error = %v, want ErrUnavailable", err)
	}
}

func TestManagementClientRejectsRedirectWithoutForwardingManagementToken(t *testing.T) {
	t.Parallel()

	var redirectedCalls atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedCalls.Add(1)
		_, _ = io.WriteString(w, `{"strategy":"round-robin"}`)
	}))
	defer redirectTarget.Close()
	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/stolen", http.StatusFound)
	}))
	defer redirectSource.Close()

	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: redirectSource.URL, ManagementToken: "management-secret"}, ready: true}, redirectSource.Client())
	_, err := client.RoutingStrategy(context.Background())
	var statusErr *ManagementStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusFound {
		t.Fatalf("RoutingStrategy() error = %v, want redirect status error", err)
	}
	if redirectedCalls.Load() != 0 {
		t.Fatalf("redirect target calls = %d, want 0", redirectedCalls.Load())
	}
}

func TestManagementClientStatusErrorDoesNotExposeBody(t *testing.T) {
	t.Parallel()

	const responseSecret = "upstream-response-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, responseSecret)
	}))
	defer server.Close()
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: "management-secret"}, ready: true}, server.Client())
	_, err := client.RoutingStrategy(context.Background())
	var statusErr *ManagementStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusTeapot || statusErr.Operation != "read routing strategy" {
		t.Fatalf("RoutingStrategy() error = %#v, want typed status error", err)
	}
	if strings.Contains(err.Error(), responseSecret) {
		t.Fatal("status error exposed the response body")
	}
}

func TestManagementClientRejectsOversizedAndMalformedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "oversized", body: strings.Repeat("x", (1<<20)+1), want: ErrResponseTooLarge},
		{name: "malformed", body: `{"strategy":`, want: ErrInvalidResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: "management-secret"}, ready: true}, server.Client())
			_, err := client.RoutingStrategy(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("RoutingStrategy() error = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), tt.body) {
				t.Fatal("response parsing error exposed the response body")
			}
		})
	}
}

func TestManagementClientListCredentialsFiltersAndRedacts(t *testing.T) {
	t.Parallel()

	const (
		responseToken = "credential-token-secret"
		responsePath  = "/private/auth/codex.json"
		responseName  = "private-codex-file.json"
		metadataValue = "raw-metadata-secret"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(req.URL.Path, "-api-key") {
			_, _ = io.WriteString(w, `{"codex-api-key":[],"claude-api-key":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{
			"observed_at":"2026-09-20T10:11:12Z",
			"files":[
				{"auth_index":"codex-ref","provider":"codex","account_type":"oauth","email":"codex@example.com","status":"active","disabled":false,"token":"`+responseToken+`","path":"`+responsePath+`","name":"`+responseName+`","metadata":{"secret":"`+metadataValue+`"}},
				{"auth_index":"claude-ref","type":"claude","account_type":"api_key","email":"claude@example.com","status":"error","disabled":true},
				{"auth_index":"gemini-ref","provider":"gemini","account_type":"oauth","email":"gemini@example.com","status":"active"},
				{"auth_index":"","provider":"codex","account_type":"oauth","email":"missing-ref@example.com","status":"active"}
			]
		}`)
	}))
	defer server.Close()
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: "management-secret"}, ready: true}, server.Client())
	got, err := client.ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListCredentials() error = %v", err)
	}
	wantObservedAt := time.Date(2026, 9, 20, 10, 11, 12, 0, time.UTC)
	want := []CredentialSummary{
		{Ref: "codex-ref", Provider: ProviderCodex, Kind: "oauth", Email: "codex@example.com", Status: "active", Verification: "unverified", Unavailable: true, ObservedAt: wantObservedAt},
		{Ref: "claude-ref", Provider: ProviderClaude, Kind: "api_key", Email: "claude@example.com", Status: "disabled", Verification: "unverified", Unavailable: true, Disabled: true, ObservedAt: wantObservedAt},
	}
	if len(got) != len(want) {
		t.Fatalf("ListCredentials() count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("ListCredentials()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{responseToken, responsePath, responseName, metadataValue, "gemini@example.com", "missing-ref@example.com"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("credential summaries exposed filtered or private upstream data")
		}
	}
}

func TestManagementClientRejectsUnknownRoutingStrategy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"strategy":"random"}`)
	}))
	defer server.Close()
	client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: "management-secret"}, ready: true}, server.Client())
	_, err := client.RoutingStrategy(context.Background())
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("RoutingStrategy() error = %v, want ErrInvalidResponse", err)
	}
}

func TestManagementClientPropagatesCancellationAndTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	source := staticEndpointSource{endpoint: Endpoint{BaseURL: server.URL, ManagementToken: "management-secret"}, ready: true}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewManagementClient(source, server.Client())
	if _, err := client.RoutingStrategy(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled RoutingStrategy() error = %v, want context.Canceled", err)
	}

	shortClient := server.Client()
	shortClient.Timeout = 20 * time.Millisecond
	client = NewManagementClient(source, shortClient)
	if _, err := client.RoutingStrategy(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed out RoutingStrategy() error = %v, want context deadline", err)
	}

	defaultClient := NewManagementClient(source, &http.Client{})
	if defaultClient.client.Timeout != 5*time.Second {
		t.Fatalf("default client timeout = %s, want 5s", defaultClient.client.Timeout)
	}
}

func TestManagementClientJSONTransportSupportsAllMethods(t *testing.T) {
	t.Parallel()

	const managementToken = "management-secret"
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != method {
					t.Fatalf("method = %q, want %q", req.Method, method)
				}
				if req.Header.Get("Authorization") != "Bearer "+managementToken {
					t.Fatal("missing management authentication")
				}
				if req.Header.Get("Accept") != "application/json" {
					t.Fatal("missing JSON accept header")
				}
				if method != http.MethodGet && req.Header.Get("Content-Type") != "application/json" {
					t.Fatal("missing JSON content type")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
					Request:    req,
				}, nil
			})
			client := NewManagementClient(
				staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: managementToken}, ready: true},
				&http.Client{Transport: transport},
			)
			var got struct {
				OK bool `json:"ok"`
			}
			var body any
			if method != http.MethodGet {
				body = map[string]string{"value": "safe"}
			}
			if err := client.doJSON(context.Background(), "test operation", method, "/test", body, &got); err != nil {
				t.Fatalf("doJSON() error = %v", err)
			}
			if !got.OK {
				t.Fatal("response was not decoded")
			}
		})
	}
}

func TestManagementClientRejectsNonJSONAndOversizedRequest(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    req,
		}, nil
	})
	client := NewManagementClient(
		staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true},
		&http.Client{Transport: transport},
	)
	var dst map[string]any
	if err := client.doJSON(context.Background(), "test operation", http.MethodGet, "/test", nil, &dst); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("non-JSON response error = %v, want ErrInvalidResponse", err)
	}

	oversized := string(bytes.Repeat([]byte("x"), managementImportLimit+1))
	if err := client.doJSON(context.Background(), "test operation", http.MethodPost, "/test", oversized, &dst); !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("oversized request error = %v, want ErrRequestTooLarge", err)
	}
}

func TestManagementErrorsAreStableAndRedacted(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		ErrUnavailable,
		ErrUnsupportedProvider,
		ErrCredentialNotFound,
		ErrCredentialConflict,
		ErrOperationUnsupported,
		ErrOAuthBusy,
		ErrOAuthExpired,
		ErrInvalidResponse,
	} {
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "http://") {
			t.Fatalf("unsafe typed error: %v", err)
		}
	}
}
