package proxyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func relayAddress(provider string) string {
	if provider == "codex" {
		return "127.0.0.1:1455"
	}
	return "127.0.0.1:54545"
}
func relayPath(provider string) string {
	if provider == "codex" {
		return "/auth/callback"
	}
	return "/callback"
}
func relayRequest(t *testing.T, provider, method, path string, query url.Values) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+relayAddress(provider)+path+"?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data)
}
func assertRelayClosed(t *testing.T, provider string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", relayAddress(provider), 150*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("callback still accepts connections after completion")
	}
	listener, err := net.Listen("tcp", relayAddress(provider))
	if err != nil {
		t.Fatalf("callback port cannot be reused: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestProviderLoginRelayForwardsOnlyTheMatchingBrowserCallback(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			var mu sync.Mutex
			var callbacks []map[string]string
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("c", 64) {
					t.Error("callback omitted private control credential")
				}
				switch r.URL.Path {
				case "/ao/status":
					return fakeResponse(200, `{"protocol_version":2}`), nil
				case "/v8/management/oauth/auth-url":
					if r.Method != http.MethodGet || r.URL.Query().Get("provider") != provider {
						t.Errorf("wrong login request: %s %s", r.Method, r.URL)
					}
					if r.URL.Query().Has("is_webui") {
						t.Error("enabled upstream all-interface relay")
					}
					if r.Header.Get("X-AO-Login-ID") != "attempt" {
						t.Error("login identity marker lost")
					}
					return fakeResponse(200, `{"state":"private-state","url":"https://provider.test/sign-in"}`), nil
				case "/v8/management/oauth/callback":
					if r.Method != http.MethodPost {
						t.Errorf("callback method=%s", r.Method)
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					mu.Lock()
					callbacks = append(callbacks, body)
					mu.Unlock()
					return fakeResponse(200, `{}`), nil
				default:
					t.Errorf("unexpected private route: %s", r.URL)
					return fakeResponse(404, `{}`), nil
				}
			})
			login, err := c.StartAccountLogin(context.Background(), provider, "attempt")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeRelay(c.root, login.ID) })
			if login.ID != "attempt" || login.Provider != provider || login.Status != "waiting" || login.State != "private-state" {
				t.Fatalf("login=%+v", login)
			}
			cases := []struct{ name, method, path, state string }{
				{"wrong-path", http.MethodGet, "/other", "private-state"},
				{"wrong-state", http.MethodGet, relayPath(provider), "other-state"},
				{"missing-state", http.MethodGet, relayPath(provider), ""},
				{"post-callback", http.MethodPost, relayPath(provider), "private-state"},
			}
			for _, tc := range cases {
				status, body := relayRequest(t, provider, tc.method, tc.path, url.Values{"state": {tc.state}, "code": {"secret-code"}})
				if status != 400 {
					t.Errorf("%s status=%d", tc.name, status)
				}
				if strings.Contains(body, "secret-code") || strings.Contains(body, "private-state") {
					t.Errorf("%s echoed browser credentials", tc.name)
				}
			}
			mu.Lock()
			count := len(callbacks)
			mu.Unlock()
			if count != 0 {
				t.Fatalf("forwarded rejected browser callback: %d", count)
			}
			status, body := relayRequest(t, provider, http.MethodGet, relayPath(provider), url.Values{"state": {"private-state"}, "code": {"secret-code"}})
			if status != 200 || !strings.Contains(body, "return to AO") {
				t.Fatalf("status=%d body=%s", status, body)
			}
			if strings.Contains(body, "secret-code") || strings.Contains(body, "private-state") {
				t.Fatal("successful response exposed OAuth data")
			}
			mu.Lock()
			saved := append([]map[string]string(nil), callbacks...)
			mu.Unlock()
			expected := map[string]string{"provider": provider, "state": "private-state", "code": "secret-code", "error": ""}
			if len(saved) != 1 || !reflect.DeepEqual(saved[0], expected) {
				t.Fatalf("callbacks=%v", saved)
			}
			closeRelay(c.root, login.ID)
			assertRelayClosed(t, provider)
		})
	}
}
func TestProviderLoginRelayReservesCallbackPortBeforeStartingOAuth(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			listener, err := net.Listen("tcp", relayAddress(provider))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			calls := 0
			c := privateClient(t, func(*http.Request) (*http.Response, error) { calls++; return fakeResponse(200, `{}`), nil })
			login, err := c.StartAccountLogin(context.Background(), provider, "blocked")
			if !errors.Is(err, ports.ErrProviderLoginCallbackBusy) {
				t.Fatalf("login=%+v err=%v", login, err)
			}
			if calls != 0 {
				t.Fatal("started upstream OAuth before reserving its callback")
			}
			if _, exists := relayOwners.Load(c.root + "/blocked"); exists {
				t.Fatal("registered a relay without owning its port")
			}
		})
	}
}
func TestProviderLoginRelayReleasesPortWhenStartFails(t *testing.T) {
	cases := []struct {
		name, body     string
		status         int
		transportError bool
	}{
		{"management-rejected", `{"token":"must-not-leak"}`, 503, false},
		{"no-link", `{"state":"state-only"}`, 200, false},
		{"no-state", `{"url":"https://provider.test/login"}`, 200, false},
		{"invalid-json", `{`, 200, false},
		{"transport-failed", ``, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/ao/status" {
					return fakeResponse(200, `{"protocol_version":2}`), nil
				}
				if tc.transportError {
					return nil, errors.New("connection lost")
				}
				return fakeResponse(tc.status, tc.body), nil
			})
			_, err := c.StartAccountLogin(context.Background(), "codex", "failed-attempt")
			if err == nil {
				t.Fatal("failed login reported success")
			}
			if strings.Contains(err.Error(), "must-not-leak") {
				t.Fatal("management error leaked token")
			}
			if _, exists := relayOwners.Load(c.root + "/failed-attempt"); exists {
				t.Fatal("failed login left registered relay")
			}
			assertRelayClosed(t, "codex")
		})
	}
}
func TestProviderLoginStatusClosesOnlyTerminalAttempts(t *testing.T) {
	for _, status := range []string{"wait", "ok", "error", "unexpected"} {
		t.Run(status, func(t *testing.T) {
			calls := []string{}
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/ao/status":
					return fakeResponse(200, `{"protocol_version":2}`), nil
				case "/v8/management/oauth/auth-url":
					return fakeResponse(200, `{"state":"a&b=?","url":"https://provider.test/login"}`), nil
				case "/v8/management/oauth/status":
					if r.URL.Query().Get("state") != "a&b=?" {
						t.Error("OAuth state was not query escaped")
					}
					data, _ := json.Marshal(map[string]string{"status": status, "error": "private-provider-error"})
					return fakeResponse(200, string(data)), nil
				default:
					t.Errorf("unexpected route %s", r.URL)
					return fakeResponse(404, `{}`), nil
				}
			})
			login, err := c.StartAccountLogin(context.Background(), "codex", "status-attempt")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeRelay(c.root, login.ID) })
			value, err := c.AccountLoginStatus(context.Background(), login)
			switch status {
			case "wait":
				if err != nil || value != "waiting" {
					t.Fatalf("status=%s err=%v", value, err)
				}
			case "ok":
				if err != nil || value != "complete" {
					t.Fatalf("status=%s err=%v", value, err)
				}
			case "error":
				if err != nil || value != "failed" {
					t.Fatalf("status=%s err=%v", value, err)
				}
			default:
				if err == nil {
					t.Fatal("unknown upstream status treated as success")
				}
			}
			_, exists := relayOwners.Load(c.root + "/" + login.ID)
			if exists != (status == "wait" || status == "unexpected") {
				t.Fatalf("relay retained=%v status=%s", exists, status)
			}
			if err != nil && strings.Contains(err.Error(), "private-provider-error") {
				t.Fatal("status error leaked provider response")
			}
			if status == "ok" || status == "error" {
				assertRelayClosed(t, "codex")
			}
			if len(calls) != 4 {
				t.Fatalf("management/probe calls=%v", calls)
			}
		})
	}
}
func TestProviderLoginCancellationClosesLocalRelayEvenIfUpstreamFails(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cancels := 0
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/ao/status":
					return fakeResponse(200, `{"protocol_version":2}`), nil
				case "/v8/management/oauth/auth-url":
					return fakeResponse(200, `{"state":"a&b","url":"https://provider.test/login"}`), nil
				case "/v8/management/oauth/session":
					cancels++
					if r.Method != http.MethodDelete || r.URL.Query().Get("state") != "a&b" {
						t.Errorf("cancel=%s %s", r.Method, r.URL)
					}
					return fakeResponse(status, `{"error":"private-error"}`), nil
				default:
					return fakeResponse(404, `{}`), nil
				}
			})
			login, err := c.StartAccountLogin(context.Background(), "codex", "cancel-attempt")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeRelay(c.root, login.ID) })
			err = c.CancelAccountLogin(context.Background(), login)
			if (err != nil) != (status == 503) {
				t.Fatalf("cancel status=%d err=%v", status, err)
			}
			if cancels != 1 {
				t.Fatalf("cancels=%d", cancels)
			}
			assertRelayClosed(t, "codex")
			if _, exists := relayOwners.Load(c.root + "/" + login.ID); exists {
				t.Fatal("cancel left relay owner")
			}
		})
	}
}
func TestProviderLoginCallbackProviderErrorIsForwardedWithoutEcho(t *testing.T) {
	var received map[string]string
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/ao/status":
			return fakeResponse(200, `{"protocol_version":2}`), nil
		case "/v8/management/oauth/auth-url":
			return fakeResponse(200, `{"state":"state","url":"https://provider.test/login"}`), nil
		case "/v8/management/oauth/callback":
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			return fakeResponse(200, `{}`), nil
		default:
			return fakeResponse(404, `{}`), nil
		}
	})
	login, err := c.StartAccountLogin(context.Background(), "claude", "denied-attempt")
	if err != nil {
		t.Fatal(err)
	}
	defer closeRelay(c.root, login.ID)
	status, body := relayRequest(t, "claude", http.MethodGet, "/callback", url.Values{"state": {"state"}, "error": {"access_denied"}, "error_description": {"private detail"}})
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if received["error"] != "access_denied" || received["code"] != "" || received["provider"] != "claude" {
		t.Fatalf("forwarded=%v", received)
	}
	if strings.Contains(body, "access_denied") || strings.Contains(body, "private detail") {
		t.Fatal("callback exposed provider denial details")
	}
}
func TestProviderLoginVerifiedResultUsesExactAttemptAndSafeFields(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/ao/login-result/login-42" {
			t.Errorf("result=%s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("c", 64) {
			t.Error("result omitted private auth")
		}
		return fakeResponse(200, `{"provider":"claude","email":"person@example.test","credential_ref":"person.json","auth_id":"exact-auth"}`), nil
	})
	got, err := c.VerifiedAccountLogin(context.Background(), "login-42")
	if err != nil {
		t.Fatal(err)
	}
	want := ports.VerifiedProviderLogin{Provider: "claude", Email: "person@example.test", CredentialRef: "person.json", AuthID: "exact-auth"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("verified=%+v", got)
	}
}
func TestProviderLoginUnsupportedProviderDoesNotOpenCallbackOrCallHelper(t *testing.T) {
	calls := 0
	c := privateClient(t, func(*http.Request) (*http.Response, error) { calls++; return fakeResponse(200, `{}`), nil })
	for _, provider := range []string{"", "Codex", "openai", "anthropic", "other"} {
		if _, err := c.StartAccountLogin(context.Background(), provider, "id"); err == nil {
			t.Errorf("provider %q accepted", provider)
		}
	}
	if calls != 0 {
		t.Fatalf("unsupported provider contacted helper: %d", calls)
	}
}

func TestNativeCredentialImportUsesCLIProxyManagement(t *testing.T) {
	calls := 0
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.URL.Path {
		case "/ao/status":
			return fakeResponse(200, `{"protocol_version":2}`), nil
		case "/v8/management/credentials":
			if r.Method != http.MethodPost || r.URL.Query().Get("name") != "ao-import-attempt.json" || r.Header.Get("X-AO-Login-ID") != "import-attempt" {
				t.Fatalf("native import request=%s %s query=%s login=%q", r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-AO-Login-ID"))
			}
			var value map[string]any
			if err := json.NewDecoder(r.Body).Decode(&value); err != nil || value["type"] != "codex" {
				t.Fatalf("import body=%v err=%v", value, err)
			}
			return fakeResponse(200, `{}`), nil
		case "/ao/login-result/import-attempt":
			return fakeResponse(200, `{"provider":"codex","email":"a@example.test","credential_ref":"ao-import-attempt.json","auth_id":"auth-a"}`), nil
		default:
			return fakeResponse(404, `{}`), nil
		}
	})
	login, err := c.StartAccountLoginMode(context.Background(), "codex", "import-attempt", "import", ports.ProviderLoginInput{CredentialJSON: `{"type":"codex","email":"a@example.test","access_token":"secret"}`})
	if err != nil || login.Status != "waiting" {
		t.Fatalf("login=%+v err=%v", login, err)
	}
	status, err := c.AccountLoginStatus(context.Background(), login)
	if err != nil || status != "complete" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	if calls != 3 {
		t.Fatalf("native calls=%d, want 3", calls)
	}
}

func TestNativeAPIKeyLoginMergesCLIProxyConfig(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/ao/status":
			return fakeResponse(200, `{"protocol_version":2}`), nil
		case "/v0/management/codex-api-key":
			if r.Method == http.MethodGet {
				return fakeResponse(200, `{"codex-api-key":[{"api-key":"old","base-url":"https://old.example"}]}`), nil
			}
			if r.Method != http.MethodPut || r.Header.Get("X-AO-Login-ID") != "key-attempt" {
				t.Fatalf("native key request=%s login=%q", r.Method, r.Header.Get("X-AO-Login-ID"))
			}
			var entries []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&entries); err != nil || len(entries) != 2 || entries[1]["api-key"] != "new" {
				t.Fatalf("merged keys=%v err=%v", entries, err)
			}
			return fakeResponse(200, `{}`), nil
		case "/ao/tag-api-key":
			return fakeResponse(200, `{"auth_id":"auth-key"}`), nil
		case "/ao/login-result/key-attempt":
			return fakeResponse(200, `{"provider":"codex","email":"Team key","credential_ref":"config-index:codex:stable","auth_id":"auth-key"}`), nil
		default:
			return fakeResponse(404, `{}`), nil
		}
	})
	login, err := c.StartAccountLoginMode(context.Background(), "codex", "key-attempt", "api_key", ports.ProviderLoginInput{APIKey: "new", BaseURL: "https://new.example"})
	if err != nil || login.Status != "waiting" {
		t.Fatalf("login=%+v err=%v", login, err)
	}
	if status, err := c.AccountLoginStatus(context.Background(), login); err != nil || status != "complete" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestNativeLoginModesRejectUnsafeInputsBeforeManagement(t *testing.T) {
	calls := 0
	c := privateClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(200, `{"protocol_version":2}`), nil
	})
	cases := []struct {
		name, provider, mode string
		input                ports.ProviderLoginInput
	}{
		{"unknown-provider", "other", "api_key", ports.ProviderLoginInput{APIKey: "key", BaseURL: "https://api.example"}},
		{"claude-device", "claude", "device", ports.ProviderLoginInput{}},
		{"unknown-mode", "codex", "other", ports.ProviderLoginInput{}},
		{"bad-json", "codex", "import", ports.ProviderLoginInput{CredentialJSON: "{"}},
		{"wrong-type", "claude", "import", ports.ProviderLoginInput{CredentialJSON: `{"type":"codex"}`}},
		{"oversized", "codex", "import", ports.ProviderLoginInput{CredentialJSON: strings.Repeat("x", 1<<20+1)}},
		{"empty-key", "codex", "api_key", ports.ProviderLoginInput{BaseURL: "https://api.example"}},
		{"unsafe-url", "claude", "api_key", ports.ProviderLoginInput{APIKey: "secret", BaseURL: "https://user:pass@example.test/?token=secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.StartAccountLoginMode(context.Background(), tc.provider, tc.name, tc.mode, tc.input); err == nil {
				t.Fatal("unsafe native login input was accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid inputs contacted helper %d times", calls)
	}
}

func TestNativeAPIKeyLoginRollsBackWhenAOCannotTagCredential(t *testing.T) {
	deleted := false
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/ao/status":
			return fakeResponse(200, `{"protocol_version":2}`), nil
		case "/v0/management/codex-api-key":
			if r.Method == http.MethodGet {
				return fakeResponse(200, `{"codex-api-key":[]}`), nil
			}
			if r.Method == http.MethodDelete {
				deleted = true
				return fakeResponse(200, `{}`), nil
			}
			return fakeResponse(200, `{}`), nil
		case "/ao/tag-api-key":
			return fakeResponse(404, `{}`), nil
		default:
			return fakeResponse(404, `{}`), nil
		}
	})
	if _, err := c.StartAccountLoginMode(context.Background(), "codex", "rollback", "api_key", ports.ProviderLoginInput{APIKey: "secret", BaseURL: "https://api.example"}); err == nil {
		t.Fatal("tag failure was accepted")
	}
	if !deleted {
		t.Fatal("native credential was not rolled back")
	}
}

func TestNativeAPIKeyLoginPreservesExistingConfiguration(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, existing := range []string{`null`, `[]`, `[{"api-key":"old","base-url":"https://old.example","models":[{"name":"model","alias":"alias"}],"headers":{"X-Custom":"value"},"priority":7}]`} {
			t.Run(provider+"/"+existing, func(t *testing.T) {
				field := provider + "-api-key"
				var original []map[string]any
				if err := json.Unmarshal([]byte(existing), &original); err != nil {
					t.Fatal(err)
				}
				writes, tags := 0, 0
				c := privateClient(t, func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/ao/status" {
						return fakeResponse(200, `{"protocol_version":2}`), nil
					}
					if r.URL.Path == "/ao/tag-api-key" {
						tags++
						return fakeResponse(200, `{}`), nil
					}
					if r.URL.Path != "/v0/management/"+field {
						t.Fatalf("unexpected path: %s", r.URL.Path)
					}
					if r.Method == http.MethodGet {
						return fakeResponse(200, `{"`+field+`":`+existing+`}`), nil
					}
					if r.Method != http.MethodPut {
						t.Fatalf("unexpected method: %s", r.Method)
					}
					var entries []map[string]any
					if err := json.NewDecoder(r.Body).Decode(&entries); err != nil || len(entries) != len(original)+1 {
						t.Fatalf("entries=%v err=%v", entries, err)
					}
					for i := range original {
						if !reflect.DeepEqual(entries[i], original[i]) {
							t.Fatalf("existing configuration changed: %v", entries[i])
						}
					}
					if !reflect.DeepEqual(entries[len(original)], map[string]any{"api-key": "new", "base-url": "https://new.example"}) {
						t.Fatalf("new key=%v", entries[len(original)])
					}
					writes++
					return fakeResponse(200, `{}`), nil
				})
				login, err := c.StartAccountLoginMode(context.Background(), provider, "attempt", "api_key", ports.ProviderLoginInput{APIKey: "new", BaseURL: " https://new.example/ "})
				if err != nil || writes != 1 || tags != 1 || login.Provider != provider {
					t.Fatalf("login=%+v writes=%d tags=%d err=%v", login, writes, tags, err)
				}
			})
		}
	}
}

func TestNativeLoginStatusDistinguishesLoadingFromFailure(t *testing.T) {
	for _, mode := range []string{"import", "api_key"} {
		for _, tc := range []struct {
			code       int
			body, want string
			wantError  bool
		}{
			{200, `{"auth_id":"ready"}`, "complete", false},
			{404, `{}`, "waiting", false},
			{500, `{}`, "", true},
			{200, `{`, "", true},
		} {
			t.Run(fmt.Sprintf("%s/%d/%s", mode, tc.code, tc.body), func(t *testing.T) {
				c := privateClient(t, func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet || r.URL.Path != "/ao/login-result/attempt" {
						t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					return fakeResponse(tc.code, tc.body), nil
				})
				status, err := c.AccountLoginStatus(context.Background(), ports.ProviderLogin{ID: "attempt", Mode: mode})
				if status != tc.want || (err != nil) != tc.wantError {
					t.Fatalf("status=%q err=%v", status, err)
				}
			})
		}
	}
}
