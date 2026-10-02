package accountsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialSummaryVerificationRequiresProof(t *testing.T) {
	t.Parallel()
	observed := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name         string
		verification string
		verifiedAt   time.Time
		want         string
	}{
		{name: "absent", want: "unverified"},
		{name: "unknown", verification: "trusted", verifiedAt: observed, want: "unverified"},
		{name: "missing observation", verification: "verified", want: "unverified"},
		{name: "invalid", verification: "invalid", verifiedAt: observed, want: "invalid"},
		{name: "verified", verification: "verified", verifiedAt: observed, want: "verified"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			summary := summaryFromRawCredential(rawCredentialRecord{
				Verification: tt.verification, VerifiedAt: tt.verifiedAt, Status: "active",
			})
			if summary.Verification != tt.want || summary.Unavailable != (tt.want != "verified") {
				t.Fatalf("verification state: %+v", summary)
			}
			if (tt.want == "verified" && !summary.VerifiedAt.Equal(observed)) ||
				(tt.want != "verified" && !summary.VerifiedAt.IsZero()) {
				t.Fatal("verification observation does not match proof")
			}
		})
	}
}

func TestAddAPIKeyUsesPrivateIdempotentCommand(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != credentialManagementPath+"/api-key" {
			t.Fatalf("unexpected credential request: %s %s", req.Method, req.URL.Path)
		}
		var input struct {
			OperationID string `json:"operationId"`
			Provider    string `json:"provider"`
			Key         string `json:"key"`
			BaseURL     string `json:"baseUrl"`
		}
		if json.NewDecoder(req.Body).Decode(&input) != nil || input.OperationID != "add-key" || input.Key != "new-private-key" || input.Provider != string(ProviderCodex) || input.BaseURL != defaultCodexBaseURL {
			t.Fatal("invalid private create command")
		}
		writes.Add(1)
		return managementJSONResponse(req, http.StatusOK, `{"auth_index":"new-ref","provider":"codex","account_type":"api_key","status":"active"}`), nil
	})
	input := APIKeyInput{OperationID: "add-key", Provider: ProviderCodex, Key: "new-private-key"}
	for range 2 {
		created, err := client.AddAPIKey(context.Background(), input)
		if err != nil || created.Ref != "new-ref" || created.Kind != CredentialAPIKey {
			t.Fatalf("create: %+v %v", created, err)
		}
	}
	if writes.Load() != 2 {
		t.Fatal("unexpected command count")
	}
}

func TestAddAPIKeyValidatesProviderKeyAndBaseURL(t *testing.T) {
	t.Parallel()

	client := NewManagementClient(nil, nil)
	for _, tt := range []struct {
		name  string
		input APIKeyInput
		want  error
	}{
		{name: "unsupported provider", input: APIKeyInput{Provider: Provider("gemini"), Key: "key"}, want: ErrUnsupportedProvider},
		{name: "empty key", input: APIKeyInput{Provider: ProviderCodex}, want: ErrInvalidCredential},
		{name: "oversized key", input: APIKeyInput{Provider: ProviderCodex, Key: strings.Repeat("x", managementAPIKeyLimit+1)}, want: ErrRequestTooLarge},
		{name: "remote HTTP", input: APIKeyInput{Provider: ProviderClaude, Key: "key", BaseURL: "http://example.com"}, want: ErrInvalidCredential},
		{name: "userinfo", input: APIKeyInput{Provider: ProviderClaude, Key: "key", BaseURL: "https://user@example.com"}, want: ErrInvalidCredential},
		{name: "query", input: APIKeyInput{Provider: ProviderClaude, Key: "key", BaseURL: "https://example.com?q=1"}, want: ErrInvalidCredential},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.AddAPIKey(context.Background(), tt.input); !errors.Is(err, tt.want) {
				t.Fatalf("AddAPIKey() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAddAPIKeyRejectsUncommittedResponse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return managementJSONResponse(req, http.StatusOK, `{"provider":"codex","account_type":"api_key"}`), nil
	})
	if _, err := client.AddAPIKey(context.Background(), APIKeyInput{Provider: ProviderCodex, Key: "same-key"}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("create error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("ambiguous creation was automatically repeated")
	}
}

func TestImportCredentialUsesPrivateBoundedDocument(t *testing.T) {
	t.Parallel()
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != credentialManagementPath+"/import" || req.URL.RawQuery != "" {
			t.Fatal("unsafe import target")
		}
		body, _ := io.ReadAll(req.Body)
		var input struct {
			OperationID string          `json:"operationId"`
			Provider    Provider        `json:"provider"`
			Credential  json.RawMessage `json:"credential"`
		}
		if json.Unmarshal(body, &input) != nil || input.OperationID == "" || input.Provider != ProviderClaude || string(input.Credential) != `{"type":"claude","access_token":"private"}` || strings.Contains(string(body), "selected.json") {
			t.Fatal("unsafe import body")
		}
		return managementJSONResponse(req, http.StatusOK, `{"auth_index":"import-ref","provider":"claude","account_type":"oauth","status":"active"}`), nil
	})
	got, err := client.ImportCredential(context.Background(), CredentialImport{Provider: ProviderClaude, Name: "selected.json", JSON: json.RawMessage(`{"type":"claude","access_token":"private"}`)})
	if err != nil || got.Ref != "import-ref" || got.Kind != CredentialOAuth {
		t.Fatalf("import: %+v %v", got, err)
	}
}

func TestImportCredentialRejectsUnsafeInputsBeforeRequest(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	client := NewManagementClient(
		staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true},
		&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls.Add(1)
			return managementJSONResponse(req, http.StatusOK, `{}`), nil
		})},
	)
	inputs := []CredentialImport{
		{Provider: Provider("gemini"), Name: "safe.json", JSON: json.RawMessage(`{"type":"gemini"}`)},
		{Provider: ProviderCodex, Name: "../unsafe.json", JSON: json.RawMessage(`{"type":"codex"}`)},
		{Provider: ProviderCodex, Name: ".oauth-codex-secret.json", JSON: json.RawMessage(`{"type":"codex"}`)},
		{Provider: ProviderCodex, Name: "quota_probe.json", JSON: json.RawMessage(`{"type":"codex"}`)},
		{Provider: ProviderCodex, Name: "safe.txt", JSON: json.RawMessage(`{"type":"codex"}`)},
		{Provider: ProviderCodex, Name: "safe.json", JSON: json.RawMessage(`{"type":"claude"}`)},
		{Provider: ProviderCodex, Name: "safe.json", JSON: json.RawMessage(`[]`)},
	}
	for _, input := range inputs {
		if _, err := client.ImportCredential(context.Background(), input); err == nil {
			t.Fatalf("ImportCredential(%q) unexpectedly succeeded", input.Name)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("requests = %d, want 0", calls.Load())
	}
}
