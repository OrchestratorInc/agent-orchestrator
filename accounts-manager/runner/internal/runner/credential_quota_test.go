package runner

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialQuotaServesManagedObservation(t *testing.T) {
	for provider := range oauthProviders {
		t.Run(provider, func(t *testing.T) {
			vault := newTestVault(t)
			runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
			var gotPath string
			runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				gotPath = request.URL.Path
				if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer access-vault-secret" {
					t.Error("wrong quota authentication")
				}
				body := `{"five_hour":{"utilization":25,"resets_at":"2030-01-01T00:00:00Z"}}`
				if provider == "codex" {
					body = `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_at":1893456000}}}`
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			auth := vaultFixture()
			auth.Provider = provider
			if err := vault.Begin(t.Context(), "observed", provider, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			auth, err := runtime.completeObserved(t.Context(), "observed", auth, false, true)
			if err != nil {
				t.Fatal(err)
			}
			handler := &credentialHTTP{key: "management", runtime: runtime}
			request := httptest.NewRequest(http.MethodGet, credentialPath+"/quota?ref="+auth.Index, nil)
			request.Header.Set("Authorization", "Bearer management")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 200 {
				t.Fatalf("managed quota route = %d, want 200", response.Code)
			}
			var result struct {
				ObservedAt time.Time `json:"observedAt"`
				Groups     []struct {
					Buckets []struct {
						Remaining float64 `json:"remainingFraction"`
					} `json:"buckets"`
				} `json:"groups"`
			}
			if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.ObservedAt.IsZero() || len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 1 || result.Groups[0].Buckets[0].Remaining != .75 {
				t.Fatal("quota observation was not normalized")
			}
			if !strings.HasSuffix(gotPath, "/usage") {
				t.Fatal("wrong quota destination")
			}
			if strings.Contains(response.Body.String(), "vault-secret") {
				t.Fatal("quota exposed credential")
			}
		})
	}
}

func TestCredentialQuotaMalformedNeverBecomesZeroUsage(t *testing.T) {
	for provider := range oauthProviders {
		for _, data := range []string{`{}`, `null`, `{"rate_limit":{}}`, `{"five_hour":{}}`, `{"five_hour":{"utilization":-1}}`, `{"five_hour":{"utilization":101}}`, `{"rate_limit":{"primary_window":{"used_percent":25,"reset_at":9999999999999}}}`, `{"five_hour":{"utilization":0,"resets_at":"bad"}}`} {
			if _, err := parseCredentialQuota(provider, []byte(data), time.Now()); err == nil {
				t.Fatal("malformed or absent usage accepted")
			}
		}
	}
	for _, test := range []struct{ provider, data string }{
		{"codex", `{"rate_limit":{"primary_window":{"used_percent":0},"secondary_window":{"used_percent":100}}}`},
		{"claude", `{"five_hour":{"utilization":0},"seven_day":{"utilization":100}}`},
	} {
		result, err := parseCredentialQuota(test.provider, []byte(test.data), time.Now())
		if err != nil || len(result.Groups[0].Buckets) != 2 || result.Groups[0].Buckets[0].RemainingFraction != 1 || result.Groups[0].Buckets[1].RemainingFraction != 0 {
			t.Fatal("genuine zero/full usage lost")
		}
	}
}

func TestCredentialQuotaIgnoresNonWindowMetadata(t *testing.T) {
	for _, metadata := range []string{
		`"plan_type":"subscription"`,
		`"metadata":true`,
		`"extra_usage":{"utilization":"not-a-window","is_enabled":false}`,
		`"future_windows":[{"utilization":"not-a-window"}]`,
	} {
		t.Run(metadata, func(t *testing.T) {
			data := `{"five_hour":{"utilization":25,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":{"utilization":40},` + metadata + `}`
			result, err := parseCredentialQuota("claude", []byte(data), time.Now())
			if err != nil {
				t.Fatalf("valid usage rejected because of unrelated metadata: %v", err)
			}
			if len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 2 || result.Groups[0].Buckets[0].RemainingFraction != .75 || result.Groups[0].Buckets[1].RemainingFraction != .6 {
				t.Fatal("metadata affected observed usage")
			}
		})
	}
}

func TestCredentialQuotaCoalescingAndRemoval(t *testing.T) {
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
	if err := vault.Begin(t.Context(), "quota-flight", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth, err := runtime.completeObserved(t.Context(), "quota-flight", vaultFixture(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return successfulCredentialCheck(request)
	})
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			if _, err := runtime.quota(t.Context(), auth); err != nil {
				t.Error(err)
			}
		})
	}
	<-started
	close(release)
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("coalesced requests=%d", calls.Load())
	}
	if _, err := runtime.quota(t.Context(), auth); err != nil || calls.Load() != 1 {
		t.Fatal("fresh quota not cached")
	}
	if err := runtime.Remove(t.Context(), auth.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.quota(t.Context(), auth); err == nil {
		t.Fatal("cached observation survived deletion")
	}
}

func TestCredentialQuotaFailureDoesNotRevokeOrSwitch(t *testing.T) {
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil), checkTransport: runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"secret":"hidden"}`)), Request: request}, nil
	})}
	if err := vault.Begin(t.Context(), "quota-error", "codex", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	auth, err := runtime.completeObserved(t.Context(), "quota-error", vaultFixture(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.quota(t.Context(), auth); err == nil {
		t.Fatal("rate limit reported success")
	}
	if !vault.admitVerified(t.Context(), auth) || len(runtime.manager.List()) != 1 {
		t.Fatal("quota failure changed credential authorization")
	}
}
