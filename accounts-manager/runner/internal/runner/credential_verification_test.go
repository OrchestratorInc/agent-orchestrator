package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialVerificationRejectsInvalidKeyBeforeCommit(t *testing.T) {
	for provider := range oauthProviders {
		t.Run(provider, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"synthetic-private-token"}}`))
			}))
			defer upstream.Close()
			vault := newTestVault(t)
			runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
			handler := &credentialHTTP{key: "management", runtime: runtime}
			payload, err := json.Marshal(credentialCreate{OperationID: "invalid-key", Provider: provider, Key: "synthetic-private-token", BaseURL: upstream.URL})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, credentialPath+"/api-key", strings.NewReader(string(payload)))
			request.Header.Set("Authorization", "Bearer management")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("provider rejected key: got HTTP %d, want 422 without credential publication", response.Code)
			}
			if calls.Load() != 1 {
				t.Fatalf("verification requests = %d, want 1", calls.Load())
			}
			records, err := vault.List(t.Context())
			if err != nil || len(records) != 0 || len(runtime.manager.List()) != 0 {
				t.Fatal("rejected credential was saved or published")
			}
			if strings.Contains(response.Body.String(), "synthetic-private-token") || strings.Contains(response.Body.String(), upstream.URL) {
				t.Fatal("verification failure exposed private material")
			}
		})
	}
}

func TestCredentialVerificationResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"authenticated", 200, `{"data":[{"id":"model"}]}`, nil},
		{"rejected", 401, `{"error":"synthetic-private-token"}`, errCredentialInvalid},
		{"limited permission", 403, `{}`, errCredentialCheckUnavailable},
		{"limited rate", 429, `{}`, errCredentialCheckUnavailable},
		{"outage", 503, `{}`, errCredentialCheckUnavailable},
		{"redirect", 302, `{}`, errCredentialCheckUnavailable},
		{"empty object", 200, `{}`, errCredentialCheckUnavailable},
		{"no models", 200, `{"data":[]}`, errCredentialCheckUnavailable},
		{"malformed model", 200, `{"data":[{}]}`, errCredentialCheckUnavailable},
		{"html", 200, `<html>login</html>`, errCredentialCheckUnavailable},
		{"oversized", 200, strings.Repeat(" ", credentialCheckLimit+1), errCredentialCheckUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			var redirected atomic.Int32
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { redirected.Add(1) }))
			defer trap.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer synthetic-private-token" {
					t.Error("wrong verification boundary")
				}
				w.Header().Set("Location", trap.URL)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer upstream.Close()
			auth := &coreauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "synthetic-private-token", "base_url": upstream.URL + "/v1"}}
			err := (&credentialRuntime{}).verifyCredential(t.Context(), auth)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if redirected.Load() != 0 {
				t.Fatal("verification followed a redirect")
			}
			if err != nil && (strings.Contains(err.Error(), auth.Attributes["api_key"]) || strings.Contains(err.Error(), upstream.URL)) {
				t.Fatal("verification error exposed private data")
			}
		})
	}
}

func TestCredentialVerificationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	runtime := &credentialRuntime{checkTransport: runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	result := make(chan error, 1)
	go func() {
		result <- runtime.verifyCredential(ctx, &coreauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "fixture"}})
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, errCredentialCheckUnavailable) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("verification ignored cancellation")
	}
}

func successfulCredentialCheck(request *http.Request) (*http.Response, error) {
	body := `{"data":[{"id":"model"}]}`
	if strings.HasSuffix(request.URL.Path, "/usage") {
		body = `{"rate_limit":{"primary_window":{"used_percent":25,"reset_at":1900000000}}}`
	}
	if strings.HasSuffix(request.URL.Path, "/profile") {
		body = `{"account":{"uuid":"account"},"organization":{"uuid":"org"}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestCredentialVerificationLifecycle(t *testing.T) {
	for _, change := range []string{"unchanged", "disabled", "removed", "replaced"} {
		t.Run(change, func(t *testing.T) {
			vault := newTestVault(t)
			runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
			if err := vault.Begin(t.Context(), "stored", "codex", time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			auth, err := runtime.Complete(t.Context(), "stored", &coreauth.Auth{Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "fixture"}})
			if err != nil {
				t.Fatal(err)
			}
			if vault.admitVerified(t.Context(), auth) {
				t.Fatal("unverified saved key authorizes requests")
			}
			started, release := make(chan struct{}), make(chan struct{})
			runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				close(started)
				<-release
				return successfulCredentialCheck(request)
			})
			result := make(chan error, 1)
			go func() { _, err := runtime.recheckCredential(t.Context(), auth); result <- err }()
			<-started
			switch change {
			case "disabled":
				err = runtime.SetEnabled(t.Context(), auth.ID, false)
			case "removed":
				err = runtime.Remove(t.Context(), auth.ID)
			case "replaced":
				updated := auth.Clone()
				updated.Attributes["api_key"] = "different-key"
				_, err = vault.Save(t.Context(), updated)
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-result; (err == nil) != (change == "unchanged") {
				t.Fatalf("recheck error=%v", err)
			}
			if vault.admitVerified(t.Context(), auth) != (change == "unchanged") {
				t.Fatal("verification crossed credential lifecycle change")
			}
			if change == "unchanged" {
				root := vault.root.Name()
				if err := vault.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := openCredentialVault(root)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if !reopened.admitVerified(t.Context(), auth) {
					t.Fatal("verified credential lost proof across restart")
				}
			}
		})
	}
}

func TestCredentialVerificationDoesNotSurviveCredentialReplacement(t *testing.T) {
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
	incoming := &coreauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "original"}}
	if err := vault.Begin(t.Context(), "verified-original", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth, err := runtime.completeObserved(t.Context(), "verified-original", incoming, false, true)
	if err != nil || !vault.admitVerified(t.Context(), auth) {
		t.Fatalf("initial proof: %v", err)
	}
	replaced := auth.Clone()
	replaced.Attributes["api_key"] = "replacement"
	if _, err := vault.Save(t.Context(), replaced); err != nil {
		t.Fatal(err)
	}
	if vault.admitVerified(t.Context(), replaced) {
		t.Error("replacement inherited verification of the old credential")
	}
	if _, err := runtime.completeObserved(t.Context(), "verified-original", incoming, false, true); err == nil {
		t.Error("replayed verification of the old credential verified its replacement")
	}
	if vault.admitVerified(t.Context(), replaced) {
		t.Error("replayed observation authorized an unverified replacement")
	}
}
