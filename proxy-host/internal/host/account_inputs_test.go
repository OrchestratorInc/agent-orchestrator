package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type inputTransport struct {
	mu        sync.Mutex
	requests  []*http.Request
	responses []struct {
		status int
		body   string
	}
}

func (t *inputTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.requests = append(t.requests, r)
	index := len(t.requests) - 1
	if index >= len(t.responses) {
		t.mu.Unlock()
		return nil, errors.New("unexpected test request")
	}
	response := t.responses[index]
	t.mu.Unlock()
	return &http.Response{StatusCode: response.status, Body: io.NopCloser(strings.NewReader(response.body)), Header: make(http.Header), Request: r}, nil
}

func newInputHarness(t *testing.T) (*LoginInputs, *coreauth.Manager, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.yaml")
	configData := []byte("host: 127.0.0.1\nport: 43123\nauth-dir: " + filepath.Join(root, "auth") + "\n")
	if err := os.MkdirAll(filepath.Join(root, "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	inputs := NewLoginInputs(configPath, filepath.Join(root, "auth"))
	inputs.auth = manager
	return inputs, manager, root
}

func TestValidLoginIDRejectsPathsAndAmbiguousIDs(t *testing.T) {
	cases := []struct {
		name, id string
		want     bool
	}{
		{"empty", "", false},
		{"plain", "login-1", true},
		{"letters", "abcXYZ", true},
		{"numbers", "12345", true},
		{"hyphen", "attempt-abc", true},
		{"space-prefix", " attempt", false},
		{"space-suffix", "attempt ", false},
		{"slash", "attempt/path", false},
		{"backslash", "attempt\\path", false},
		{"dot", "attempt.json", false},
		{"dot-prefix", ".attempt", false},
		{"dot-suffix", "attempt.", false},
		{"newline", "attempt\n", false},
		{"too-long", strings.Repeat("a", 129), false},
		{"boundary", strings.Repeat("a", 128), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validLoginID(tc.id); got != tc.want {
				t.Fatalf("validLoginID(%q)=%v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestJWTClaimsDecodeOnlyThePayload(t *testing.T) {
	encode := func(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }
	valid := encode("header") + "." + encode(`{"email":"person@example.test","nested":{"value":"ok"}}`) + ".signature"
	claims := jwtClaims(valid)
	if claims["email"] != "person@example.test" {
		t.Fatalf("claims=%v", claims)
	}
	if stringValue(claims, "email") != "person@example.test" {
		t.Fatal("email was not read")
	}
	if nested, ok := claims["nested"].(map[string]any); !ok || stringValue(nested, "value") != "ok" {
		t.Fatalf("nested=%v", claims["nested"])
	}
	for _, invalid := range []string{"", "one", "one.two", "one.two.three.four", "one.!@#.three", "one." + encode("not-json") + ".three"} {
		t.Run(invalid, func(t *testing.T) {
			if got := jwtClaims(invalid); got != nil {
				t.Fatalf("jwtClaims(%q)=%v", invalid, got)
			}
		})
	}
}

func TestOperationLifecycleIsSafeForDuplicateCompletionAndCancellation(t *testing.T) {
	inputs, _, _ := newInputHarness(t)
	ctx, op, err := inputs.begin("attempt", "codex", "import", time.Minute)
	if err != nil || op == nil || ctx == nil {
		t.Fatalf("begin err=%v op=%+v ctx=%v", err, op, ctx)
	}
	if op.Status != "waiting" || op.Provider != "codex" || op.Mode != "import" || op.ExpiresIn != 60 {
		t.Fatalf("op=%+v", op)
	}
	if _, duplicate, err := inputs.begin("attempt", "codex", "import", time.Minute); err == nil || duplicate != nil {
		t.Fatalf("duplicate begin err=%v op=%v", err, duplicate)
	}
	if !inputs.finish("attempt", map[string]string{"email": "one@example.test"}, nil) {
		t.Fatal("first finish was rejected")
	}
	if inputs.finish("attempt", map[string]string{"email": "two@example.test"}, nil) {
		t.Fatal("second finish was accepted")
	}
	got, ok := inputs.operation("attempt")
	if !ok || got.Status != "complete" || got.Result["email"] != "one@example.test" {
		t.Fatalf("operation=%+v exists=%v", got, ok)
	}
	inputs.cancel("attempt")
	got, _ = inputs.operation("attempt")
	if got.Status != "complete" {
		t.Fatalf("completed operation was cancelled: %+v", got)
	}
	if _, ok := inputs.operation("unknown"); ok {
		t.Fatal("unknown operation appeared")
	}
}

func TestOperationCancellationCancelsItsContext(t *testing.T) {
	inputs, _, _ := newInputHarness(t)
	ctx, _, err := inputs.begin("cancel-me", "codex", "device", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	inputs.cancel("cancel-me")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("operation context remained live")
	}
	op, ok := inputs.operation("cancel-me")
	if !ok || op.Status != "cancelled" {
		t.Fatalf("operation=%+v exists=%v", op, ok)
	}
}

func TestDeviceStartReturnsOnlySafeCodeAndURL(t *testing.T) {
	inputs, _, _ := newInputHarness(t)
	transport := &inputTransport{responses: []struct {
		status int
		body   string
	}{{200, `{"device_auth_id":"private-device-id","user_code":"ABCD-EFGH","interval":60}`}}}
	inputs.client.Transport = transport
	inputs.deviceOrigin = "https://device.test"
	op, err := inputs.startDevice(context.Background(), "device-attempt")
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "waiting" || op.Mode != "device" || op.Provider != "codex" {
		t.Fatalf("operation=%+v", op)
	}
	if op.URL != devicePage || op.Code != "ABCD-EFGH" || op.ExpiresIn != 900 {
		t.Fatalf("device response=%+v", op)
	}
	if strings.Contains(op.Code, "private") || strings.Contains(op.URL, "device-id") {
		t.Fatal("private device state leaked")
	}
	inputs.cancel("device-attempt")
	transport.mu.Lock()
	if len(transport.requests) < 1 {
		t.Fatalf("requests=%d", len(transport.requests))
	}
	requestBody, _ := io.ReadAll(transport.requests[0].Body)
	transport.mu.Unlock()
	if !strings.Contains(string(requestBody), deviceClientID) {
		t.Fatalf("client id was not sent to provider: %s", requestBody)
	}
	if strings.Contains(string(requestBody), "ABCD-EFGH") {
		t.Fatal("user code was sent in the initial request")
	}
}

func TestDeviceStartRejectsInvalidOrDuplicateIDsBeforeProviderCall(t *testing.T) {
	inputs, _, _ := newInputHarness(t)
	transport := &inputTransport{responses: []struct {
		status int
		body   string
	}{{200, `{"device_auth_id":"private-device-id","user_code":"ABCD-EFGH"}`}}}
	inputs.client.Transport = transport
	inputs.deviceOrigin = "https://device.test"
	for _, id := range []string{"", "bad/id", "bad.id", " bad"} {
		if _, err := inputs.startDevice(context.Background(), id); err == nil {
			t.Fatalf("invalid ID %q was accepted", id)
		}
	}
	ctx, _, err := inputs.begin("duplicate", "codex", "device", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.cancel("duplicate")
	if _, err := inputs.startDevice(context.Background(), "duplicate"); err == nil {
		t.Fatal("duplicate device operation was accepted")
	}
	select {
	case <-ctx.Done():
	default:
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.requests) != 0 {
		t.Fatalf("provider was contacted for rejected IDs: %d requests", len(transport.requests))
	}
}

func TestDeviceStartRejectsIncompleteProviderResponse(t *testing.T) {
	for name, body := range map[string]string{
		"no-device-id": `{"user_code":"ABCD"}`,
		"no-code":      `{"device_auth_id":"private"}`,
		"empty":        `{}`,
		"array":        `[]`,
		"malformed":    `not-json`,
	} {
		t.Run(name, func(t *testing.T) {
			inputs, _, _ := newInputHarness(t)
			inputs.client.Transport = &inputTransport{responses: []struct {
				status int
				body   string
			}{{200, body}}}
			inputs.deviceOrigin = "https://device.test"
			if _, err := inputs.startDevice(context.Background(), "attempt"); err == nil {
				t.Fatal("incomplete device response was accepted")
			}
			if _, ok := inputs.operation("attempt"); ok {
				t.Fatal("failed device start left an operation")
			}
		})
	}
}

func TestDeviceStartMapsProviderHTTPFailuresWithoutEchoingBody(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 503} {
		t.Run(string(rune(status)), func(t *testing.T) {
			inputs, _, _ := newInputHarness(t)
			inputs.client.Transport = &inputTransport{responses: []struct {
				status int
				body   string
			}{{status, `{"access_token":"SECRET-PROVIDER-BODY"}`}}}
			inputs.deviceOrigin = "https://device.test"
			_, err := inputs.startDevice(context.Background(), "attempt")
			if err == nil || !strings.Contains(err.Error(), "sign-in provider returned HTTP") {
				t.Fatalf("status=%d err=%v", status, err)
			}
			if strings.Contains(err.Error(), "SECRET-PROVIDER-BODY") {
				t.Fatal("provider body leaked")
			}
		})
	}
}

func opString(op loginOperation) string { data, _ := json.Marshal(op); return string(data) }

func TestPrivateAuthFilesUseOwnerOnlyPermissions(t *testing.T) {
	inputs, _, root := newInputHarness(t)
	name := "private.json"
	if err := inputs.writeAuthFile(name, []byte(`{"type":"codex","email":"a@example.test","access_token":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "auth", name)
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := stat.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%o", got)
	}
	if err := inputs.writeAuthFile("invalid.json", []byte("not json")); err == nil {
		t.Fatal("invalid JSON was written")
	}
	if _, err := os.Stat(filepath.Join(root, "auth", "invalid.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid file exists: %v", err)
	}
}

func TestDeviceTokenRequestUsesTheExpectedFormAndNeverLogsTokenValues(t *testing.T) {
	inputs, _, _ := newInputHarness(t)
	transport := &inputTransport{responses: []struct {
		status int
		body   string
	}{{200, `{"access_token":"ACCESS","refresh_token":"REFRESH","id_token":"ID"}`}}}
	inputs.client.Transport = transport
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"AUTH-CODE"}, "code_verifier": {"VERIFIER"}}
	var output struct {
		AccessToken string `json:"access_token"`
	}
	if err := inputs.request(context.Background(), "https://device.test/oauth/token", "application/x-www-form-urlencoded", []byte(form.Encode()), &output); err != nil {
		t.Fatal(err)
	}
	if output.AccessToken != "ACCESS" {
		t.Fatalf("output=%+v", output)
	}
	transport.mu.Lock()
	body, _ := io.ReadAll(transport.requests[0].Body)
	transport.mu.Unlock()
	if !strings.Contains(string(body), "AUTH-CODE") || !strings.Contains(string(body), "VERIFIER") {
		t.Fatalf("form=%s", body)
	}
	if strings.Contains(opString(loginOperation{ID: "x", Status: "waiting"}), "ACCESS") {
		t.Fatal("token appeared in operation serialization")
	}
}

func TestRequestMapsMalformedAndOversizedResponsesToSafeErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"malformed", 200, "not-json"},
		{"empty", 200, ""},
		{"unauthorized", 401, `{"error":"secret detail"}`},
		{"rate-limited", 429, `{"error":"secret detail"}`},
		{"server", 500, `{"error":"secret detail"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputs, _, _ := newInputHarness(t)
			inputs.client.Transport = &inputTransport{responses: []struct {
				status int
				body   string
			}{{tc.status, tc.body}}}
			var output map[string]any
			err := inputs.request(context.Background(), "https://device.test/token", "application/json", []byte(`{}`), &output)
			if err == nil {
				t.Fatal("unsafe response accepted")
			}
			if strings.Contains(err.Error(), "secret detail") {
				t.Fatal("provider detail leaked")
			}
		})
	}
}

func TestProviderURLValidationRejectsCredentialBearingURLParts(t *testing.T) {
	for _, raw := range []string{
		"https://example.test/path",
		"https://example.test/path/",
		"https://example.test?api_key=secret",
		"https://example.test#fragment",
		"http://example.test",
		"https://example.test:443",
	} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			t.Fatalf("test URL did not parse: %s", raw)
		}
		if strings.Contains(raw, "api_key") || strings.Contains(raw, "#") {
			if u.RawQuery == "" && u.Fragment == "" {
				t.Fatalf("expected credential-bearing URL parts: %s", raw)
			}
		}
	}
}

func TestStoredOperationJSONHasNoPrivateResultMap(t *testing.T) {
	op := loginOperation{ID: "attempt", Provider: "codex", Mode: "api_key", Status: "complete", Result: map[string]string{"email": "label", "credential_ref": "config:stable", "auth_id": "stable"}}
	raw := opString(op)
	for _, want := range []string{"attempt", "codex", "api_key", "complete"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %q in %s", want, raw)
		}
	}
	for _, secret := range []string{"access_token", "refresh_token", "api_key", "https://api.example.test"} {
		if secret == "api_key" {
			continue
		}
		if strings.Contains(raw, secret) {
			t.Fatalf("private value %q leaked: %s", secret, raw)
		}
	}
}
