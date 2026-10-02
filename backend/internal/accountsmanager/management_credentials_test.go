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
)

func TestListCredentialsProjectsSafeLifecycleAndCooldowns(t *testing.T) {
	t.Parallel()

	const secret = "private-upstream-secret"
	response := `{
		"observed_at":"2026-09-20T10:11:12Z",
		"files":[{
			"auth_index":"codex-ref","provider":"codex","account_type":"oauth","email":"user@example.com",
			"status":"error","disabled":false,"unavailable":true,"supports_quota":true,
			"created_at":"2026-09-18T10:00:00Z","updated_at":"2026-09-19T10:00:00Z","last_refresh":"2026-09-20T09:00:00Z",
			"cooldowns":[{"scope":"model","model_key":"gpt-test","reason":"quota","retry_at":"2026-09-20T11:00:00Z","remaining_seconds":120,"http_status":429,"message":"` + secret + `"}],
			"path":"/private/file.json","account":"` + secret + `","id_token":{"email":"hidden@example.com"}
		},{"auth_index":"other","provider":"gemini","status":"active"}]
	}`
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "-api-key") {
			return managementJSONResponse(req, http.StatusOK, `{"codex-api-key":[],"claude-api-key":[]}`), nil
		}
		return managementJSONResponse(req, http.StatusOK, response), nil
	})
	got, err := client.ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListCredentials() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("credentials = %#v", got)
	}
	credential := got[0]
	if credential.Ref != "codex-ref" || credential.Status != CredentialError || !credential.Unavailable || !credential.QuotaSupported || len(credential.Cooldowns) != 1 {
		t.Fatalf("credential = %#v", credential)
	}
	if cooldown := credential.Cooldowns[0]; cooldown.Model != "gpt-test" || cooldown.HTTPStatus != 429 || cooldown.RemainingSeconds != 120 {
		t.Fatalf("cooldown = %#v", cooldown)
	}
	encoded, _ := json.Marshal(got)
	for _, private := range []string{secret, "/private/file.json", "hidden@example.com"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("summary exposed %q", private)
		}
	}
}

func TestManagedAPIKeysRemainVisibleAndRemovableWithoutSecretReads(t *testing.T) {
	removed := false
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != credentialManagementPath {
			t.Fatal("legacy secret-reading endpoint used")
		}
		if req.Method == http.MethodDelete {
			if req.URL.Query().Get("ref") != "managed-key" || req.Body != nil {
				t.Fatal("removal did not identify only its target")
			}
			removed = true
			return managementJSONResponse(req, http.StatusNoContent, ""), nil
		}
		return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"managed-key","provider":"codex","account_type":"api_key","status":"active","secret":"never-public"}]}`), nil
	})
	credentials, err := client.ListCredentials(context.Background())
	if err != nil || len(credentials) != 1 || credentials[0].Ref != "managed-key" || credentials[0].Kind != CredentialAPIKey {
		t.Fatalf("inventory=%+v err=%v", credentials, err)
	}
	encoded, _ := json.Marshal(credentials)
	if strings.Contains(string(encoded), "never-public") {
		t.Fatal("key leaked into account projection")
	}
	if err := client.RemoveCredential(context.Background(), "managed-key"); err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}

func TestAddAPIKeyReturnsRunnerRegistrationAcknowledgement(t *testing.T) {
	calls := 0
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost || req.URL.Path != credentialManagementPath+"/api-key" {
			t.Fatal("unexpected configuration read or write")
		}
		return managementJSONResponse(req, http.StatusOK, `{"auth_index":"registered","provider":"codex","account_type":"api_key","status":"active"}`), nil
	})
	credential, err := client.AddAPIKey(context.Background(), APIKeyInput{Provider: ProviderCodex, Key: "test-key", BaseURL: "http://127.0.0.1:1/v1"})
	if err != nil || credential.Ref != "registered" || calls != 1 {
		t.Fatalf("registration=%q calls=%d err=%v", credential.Ref, calls, err)
	}
}

func TestCredentialMutationsResolveUniqueReference(t *testing.T) {
	t.Parallel()

	var disabled atomic.Bool
	var refreshed atomic.Bool
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == credentialManagementPath:
			status := "active"
			if refreshed.Load() {
				status = "refreshing"
			}
			return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"target-ref","name":"target.json","provider":"claude","account_type":"oauth","status":"`+status+`","disabled":`+boolJSON(disabled.Load())+`}]}`), nil
		case req.Method == http.MethodPatch && req.URL.Path == credentialManagementPath+"/status":
			body, _ := io.ReadAll(req.Body)
			if string(body) != `{"disabled":true}` || req.URL.Query().Get("ref") != "target-ref" {
				t.Fatalf("disable body = %s", body)
			}
			disabled.Store(true)
			return managementJSONResponse(req, http.StatusOK, `{"status":"ok"}`), nil
		case req.Method == http.MethodPost && req.URL.Path == credentialManagementPath+"/refresh":
			if req.URL.Query().Get("ref") != "target-ref" || req.Body != nil {
				t.Fatal("invalid refresh target")
			}
			refreshed.Store(true)
			return managementJSONResponse(req, http.StatusOK, `{"auth_index":"target-ref","provider":"claude","account_type":"oauth","status":"active","disabled":true}`), nil
		default:
			return managementJSONResponse(req, http.StatusNotFound, `{}`), nil
		}
	})
	if err := client.SetCredentialDisabled(context.Background(), "target-ref", true); err != nil {
		t.Fatalf("SetCredentialDisabled() error = %v", err)
	}
	refreshedCredential, err := client.RefreshCredential(context.Background(), "target-ref")
	if err != nil {
		t.Fatalf("RefreshCredential() error = %v", err)
	}
	if refreshedCredential.Status != CredentialDisabled {
		t.Fatalf("RefreshCredential() = %#v", refreshedCredential)
	}
}

func TestRemoveCredentialHandlesBothKindsAndMissing(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"oauth", "api_key"} {
		t.Run(kind, func(t *testing.T) {
			deleted := false
			client := managementTestClient(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != credentialManagementPath {
					t.Fatal("removal used legacy endpoint")
				}
				if req.Method == http.MethodDelete {
					if req.URL.Query().Get("ref") != "remove-ref" || req.Body != nil {
						t.Fatal("removal changed unrelated credentials")
					}
					deleted = true
					return managementJSONResponse(req, http.StatusNoContent, ""), nil
				}
				if deleted {
					return managementJSONResponse(req, http.StatusOK, `{"files":[]}`), nil
				}
				return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"remove-ref","provider":"codex","account_type":"`+kind+`"}]}`), nil
			})
			for range 2 {
				if err := client.RemoveCredential(context.Background(), "remove-ref"); err != nil {
					t.Fatal(err)
				}
			}
			if !deleted {
				t.Fatal("credential was not removed")
			}
		})
	}
}

func TestCredentialMutationRejectsAmbiguousReference(t *testing.T) {
	t.Parallel()

	var mutations atomic.Int32
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			mutations.Add(1)
		}
		return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"duplicate","name":"a.json","provider":"codex"},{"auth_index":"duplicate","name":"b.json","provider":"claude"}]}`), nil
	})
	if err := client.SetCredentialDisabled(context.Background(), "duplicate", true); !errors.Is(err, ErrCredentialConflict) {
		t.Fatalf("SetCredentialDisabled() error = %v", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("mutations = %d, want 0", mutations.Load())
	}
}

func managementTestClient(roundTrip roundTripFunc) *ManagementClient {
	return NewManagementClient(
		staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true},
		&http.Client{Transport: roundTrip},
	)
}

func boolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
