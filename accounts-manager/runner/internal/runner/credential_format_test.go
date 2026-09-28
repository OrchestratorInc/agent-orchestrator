package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialFormatRejectsSignInTokenInKeyForm(t *testing.T) {
	for provider := range oauthProviders {
		t.Run(provider, func(t *testing.T) {
			var calls atomic.Int32
			vault := newTestVault(t)
			runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil), checkTransport: runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return successfulCredentialCheck(request)
			})}
			handler := &credentialHTTP{key: "management", runtime: runtime}
			for range 2 {
				payload, err := json.Marshal(credentialCreate{OperationID: "wrong-method", Provider: provider, Key: "sk-ant-oat01-synthetic-only"})
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, credentialPath+"/api-key", strings.NewReader(string(payload)))
				request.Header.Set("Authorization", "Bearer management")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusUnprocessableEntity {
					t.Errorf("token input status=%d, want rejected before verification", response.Code)
				}
				if strings.Contains(response.Body.String(), "synthetic-only") {
					t.Error("token material crossed the response boundary")
				}
			}
			stored, err := vault.List(t.Context())
			vault.mu.Lock()
			operations := len(vault.state.Operations)
			vault.mu.Unlock()
			if err != nil || calls.Load() != 0 || len(stored) != 0 || len(runtime.manager.List()) != 0 || operations != 0 {
				t.Errorf("wrong-method input had side effects: calls=%d records=%d published=%d error=%v", calls.Load(), len(stored), len(runtime.manager.List()), err)
			}
		})
	}
}

func TestCredentialFormatLegacyProjectionPreservesStoredProof(t *testing.T) {
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
	if err := vault.Begin(t.Context(), "legacy-format", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth, err := runtime.completeObserved(t.Context(), "legacy-format", &coreauth.Auth{
		Provider: "codex", Label: "Existing account", Status: coreauth.StatusActive,
		Attributes: map[string]string{"api_key": "sk-ant-oat01-synthetic-only"},
	}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := vault.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	record := (&credentialHTTP{runtime: runtime}).record(t.Context(), auth)
	if record.AccountType != "access_token" {
		t.Errorf("stored sign-in token format=%q, want access_token", record.AccountType)
	}
	if record.SupportsQuota || record.Verification != "verified" || record.Generation == 0 || record.AuthIndex != auth.Index {
		t.Error("format projection invented quota permission or changed stored identity/proof")
	}
	if record.Unavailable || !vault.admitVerified(t.Context(), auth) {
		t.Error("format projection changed prior readiness or admission without migration")
	}
	after, err := vault.List(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Error("format projection mutated the vault")
	}
	encoded, err := json.Marshal(record)
	if err != nil || strings.Contains(string(encoded), "synthetic-only") {
		t.Error("format projection exposed secret material")
	}
	root := vault.root.Name()
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openCredentialVault(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.List(t.Context())
	if err != nil || !reflect.DeepEqual(before, stored) || !reopened.admitVerified(t.Context(), auth) {
		t.Error("projection changed stored proof across reopen")
	}
}
