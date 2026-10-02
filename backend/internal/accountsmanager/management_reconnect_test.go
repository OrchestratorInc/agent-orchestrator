package accountsmanager

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestManagementReconnectTargetsAndSafeFailures(t *testing.T) {
	for _, code := range []int{200, 404, 409, 422} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			client := NewManagementClient(staticEndpointSource{endpoint: Endpoint{BaseURL: "http://127.0.0.1:12345", ManagementToken: "management-secret"}, ready: true}, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				var input struct {
					Provider, Mode, TargetRef string
					Generation                uint64
				}
				if json.NewDecoder(req.Body).Decode(&input) != nil || input.Provider != "codex" || input.Mode != "device" || input.TargetRef != "private-ref" || input.Generation != 3 {
					t.Fatal("reconnect target lost")
				}
				return managementJSONResponse(req, code, `{"provider":"codex","mode":"device","targetRef":"private-ref","state":"operation","authorizationUrl":"https://provider.example/login","userCode":"ABCD-EFGH","expiresAt":"2027-01-01T00:00:00Z"}`), nil
			})})
			session, err := client.ReconnectOAuth(t.Context(), ProviderCodex, OAuthModeDevice, "private-ref", 3)
			want := map[int]error{404: ErrCredentialNotFound, 409: ErrOAuthBusy, 422: ErrOperationUnsupported}[code]
			if !errors.Is(err, want) || (err == nil && session.TargetRef != "private-ref") {
				t.Fatalf("reconnect=%v want=%v", err, want)
			}
		})
	}
	for _, failure := range []string{"identity_mismatch", "credential_changed", "storage_unavailable", "private-token"} {
		raw, _ := json.Marshal(map[string]string{"provider": "codex", "mode": "callback", "targetRef": "private-ref", "state": "operation", "status": "failed", "failureCode": failure, "expiresAt": "2027-01-01T00:00:00Z"})
		event, err := decodeOAuthEvent(raw)
		want := failure
		if want == "private-token" {
			want = "authentication_failed"
		}
		if err != nil || event.FailureCode != want || event.TargetRef != "private-ref" {
			t.Fatal("unsafe reconnect event projection")
		}
	}
}
