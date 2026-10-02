package accountsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestListCredentialModelsReturnsSafeProjection(t *testing.T) {
	t.Parallel()

	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case credentialManagementPath:
			return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"codex-ref","name":"codex.json","provider":"codex"}]}`), nil
		case credentialManagementPath + "/models":
			return managementJSONResponse(req, http.StatusOK, `{"models":[{"id":"gpt-safe","display_name":"GPT Safe","type":"chat","owned_by":"openai","secret":"hidden"}]}`), nil
		default:
			return managementJSONResponse(req, http.StatusNotFound, `{}`), nil
		}
	})
	models, err := client.ListCredentialModels(context.Background(), "codex-ref")
	if err != nil {
		t.Fatalf("ListCredentialModels() error = %v", err)
	}
	want := CredentialModel{ID: "gpt-safe", DisplayName: "GPT Safe", Type: "chat", Owner: "openai"}
	if len(models) != 1 || !reflect.DeepEqual(models[0], want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestCredentialModelsPreserveAdvertisedEfforts(t *testing.T) {
	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == credentialManagementPath {
			return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"account-ref","provider":"codex"}]}`), nil
		}
		return managementJSONResponse(req, http.StatusOK, `{"models":[{"id":"managed-model","thinking":{"levels":["low","high"],"private":"secret-value"}}]}`), nil
	})
	models, err := client.ListCredentialModels(t.Context(), "account-ref")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"Efforts":["low","high"]`) || strings.Contains(string(data), "secret-value") {
		t.Fatalf("safe advertised efforts were not preserved: %s", data)
	}
}

func TestCredentialModelsRejectUnboundedOrUnknownEfforts(t *testing.T) {
	for _, levels := range []string{`["private-token"]`, `[` + strings.Repeat(`"high",`, 16) + `"low"]`} {
		client := managementTestClient(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == credentialManagementPath {
				return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"account-ref","provider":"codex"}]}`), nil
			}
			return managementJSONResponse(req, http.StatusOK, `{"models":[{"id":"managed-model","thinking":{"levels":`+levels+`}}]}`), nil
		})
		if _, err := client.ListCredentialModels(t.Context(), "account-ref"); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("unsafe effort projection: %v", err)
		}
	}
}

func TestCredentialQuotaFetchAndResetCapabilities(t *testing.T) {
	t.Parallel()

	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == credentialManagementPath:
			return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"claude-ref","name":"claude.json","provider":"claude","supports_quota":true}]}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v0/management/quota/providers":
			return managementJSONResponse(req, http.StatusOK, `{"providers":[{"provider":"claude","supported_providers":["claude"],"supports_reset":true,"private":"hidden"}]}`), nil
		case req.Method == http.MethodGet && req.URL.Path == credentialManagementPath+"/quota":
			return managementJSONResponse(req, http.StatusOK, `{"observedAt":"2026-09-28T00:00:00Z","subscription":{"plan":"Pro","tierName":"Max"},"summary":[{"key":"credits_used","label":"Credits used","value":12.5,"unit":"credits"}],"groups":[{"displayName":"Weekly","buckets":[{"window":"weekly","remainingFraction":0.75,"resetTime":"2026-09-27T00:00:00Z"}]}],"raw":"hidden"}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v0/management/quota/reset":
			return managementJSONResponse(req, http.StatusOK, `{"status":"ok"}`), nil
		default:
			return managementJSONResponse(req, http.StatusNotFound, `{}`), nil
		}
	})
	quota, err := client.FetchCredentialQuota(context.Background(), "claude-ref")
	if err != nil {
		t.Fatalf("FetchCredentialQuota() error = %v", err)
	}
	if quota.Subscription == nil || quota.Subscription.Plan != "Pro" || len(quota.Groups) != 1 || quota.Groups[0].Buckets[0].RemainingFraction != 0.75 {
		t.Fatalf("quota = %#v", quota)
	}
	if err = client.ResetCredentialQuota(context.Background(), "claude-ref"); err != nil {
		t.Fatalf("ResetCredentialQuota() error = %v", err)
	}
}

func TestCredentialQuotaUnsupportedAndMalformedResponses(t *testing.T) {
	t.Parallel()

	t.Run("not supported", func(t *testing.T) {
		client := managementTestClient(func(req *http.Request) (*http.Response, error) {
			return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"codex-ref","name":"codex.json","provider":"codex","supports_quota":false}]}`), nil
		})
		if _, err := client.FetchCredentialQuota(context.Background(), "codex-ref"); !errors.Is(err, ErrOperationUnsupported) {
			t.Fatalf("FetchCredentialQuota() error = %v", err)
		}
	})

	t.Run("invalid fraction", func(t *testing.T) {
		client := managementTestClient(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case credentialManagementPath:
				return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"codex-ref","name":"codex.json","provider":"codex","supports_quota":true}]}`), nil
			case credentialManagementPath + "/quota":
				return managementJSONResponse(req, http.StatusOK, `{"observedAt":"2026-09-28T00:00:00Z","groups":[{"buckets":[{"window":"weekly","remainingFraction":2}]}]}`), nil
			default:
				return managementJSONResponse(req, http.StatusNotFound, `{}`), nil
			}
		})
		if _, err := client.FetchCredentialQuota(context.Background(), "codex-ref"); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("FetchCredentialQuota() error = %v", err)
		}
	})
}

func TestListCredentialsPreservesOnlyPassiveQuotaSignals(t *testing.T) {
	t.Parallel()

	client := managementTestClient(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "-api-key") {
			return managementJSONResponse(req, http.StatusOK, `{"codex-api-key":[],"claude-api-key":[]}`), nil
		}
		return managementJSONResponse(req, http.StatusOK, `{"files":[{"auth_index":"codex-ref","provider":"codex","quota":{"observed_at":"2026-09-20T10:00:00Z","signals":{"x-codex-primary-used-percent":"75"},"secret":"hidden"},"model_quotas":{"gpt-safe":{"signals":{"x-codex-secondary-used-percent":"10"},"raw":"hidden"}}}]}`), nil
	})
	credentials, err := client.ListCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].Quota.Signals["x-codex-primary-used-percent"] != "75" || credentials[0].ModelQuota["gpt-safe"].Signals["x-codex-secondary-used-percent"] != "10" {
		t.Fatalf("credentials = %#v", credentials)
	}
	if strings.Contains(credentials[0].Quota.Signals["x-codex-primary-used-percent"], "hidden") {
		t.Fatal("quota projection exposed raw data")
	}
}
