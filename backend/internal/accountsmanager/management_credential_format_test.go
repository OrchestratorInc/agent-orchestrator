package accountsmanager

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialFormatKeyInputDoesNotReachPrivateTransport(t *testing.T) {
	var calls atomic.Int32
	client := managementTestClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return managementJSONResponse(request, http.StatusOK, `{"auth_index":"wrong-form","provider":"codex","account_type":"api_key"}`), nil
	})
	for _, key := range []string{"sk-ant-oat01-synthetic-only", " sk-ant-oat01-synthetic-only "} {
		_, err := client.AddAPIKey(t.Context(), APIKeyInput{Provider: ProviderCodex, Key: key, OperationID: "wrong-form"})
		if err == nil {
			t.Error("sign-in token accepted by API-key input")
		} else if strings.Contains(err.Error(), "synthetic-only") {
			t.Error("input error disclosed token material")
		}
	}
	if calls.Load() != 0 {
		t.Errorf("private calls=%d, want zero for wrong-method input", calls.Load())
	}
}

func TestCredentialFormatProjectionAcceptsAccessTokenKind(t *testing.T) {
	record := summaryFromRawCredential(rawCredentialRecord{AccountType: "access_token", SupportsQuota: false})
	if record.Kind != CredentialKind("access_token") || record.QuotaSupported {
		t.Errorf("format=%q quota=%t", record.Kind, record.QuotaSupported)
	}
}

func TestCredentialFormatProjectionDoesNotInferQuotaFromTokenKind(t *testing.T) {
	record := summaryFromRawCredential(rawCredentialRecord{AccountType: "access_token", SupportsQuota: true})
	if record.QuotaSupported {
		t.Error("legacy token format inherited contradictory quota permission")
	}
}

func TestCredentialFormatLegacyProjectionPreservesReadinessProof(t *testing.T) {
	record := summaryFromRawCredential(rawCredentialRecord{AccountType: "access_token", Status: "active", Verification: "verified", VerifiedAt: time.Now()})
	if record.Unavailable || record.Disabled || record.Verification != "verified" {
		t.Error("format projection changed prior readiness, durable proof or enablement")
	}
}

func TestCredentialFormatQuotaCommandsRejectLegacyToken(t *testing.T) {
	for name, command := range map[string]func(context.Context, *ManagementClient) error{
		"read": func(ctx context.Context, client *ManagementClient) error {
			_, err := client.FetchCredentialQuota(ctx, "stored-token")
			return err
		},
		"reset": func(ctx context.Context, client *ManagementClient) error {
			return client.ResetCredentialQuota(ctx, "stored-token")
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			client := managementTestClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return managementJSONResponse(request, http.StatusOK, `{"files":[{"auth_index":"stored-token","provider":"codex","account_type":"access_token","supports_quota":true}]}`), nil
			})
			if err := command(t.Context(), client); !errors.Is(err, ErrOperationUnsupported) || calls.Load() != 1 {
				t.Errorf("legacy token quota command: calls=%d error=%v", calls.Load(), err)
			}
		})
	}
}
