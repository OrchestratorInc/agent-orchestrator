package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testBaseURL = "http://127.0.0.1:3001"

type echoInput struct {
	Text string `json:"text"`
}

type fakeServer struct {
	*httptest.Server
	needToken   bool
	minToken    atomic.Int32
	tokenCalls  atomic.Int32
	registered  atomic.Int32
	lastRefresh atomic.Value
}

func newFakeServer(t *testing.T, needToken bool) *fakeServer {
	t.Helper()
	fake := &fakeServer{needToken: needToken}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echo the text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
		})
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	writeJSON := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		number, _ := strconv.Atoi(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer access-"))
		if fake.needToken && (!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-") || int32(number) < fake.minToken.Load()) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+fake.URL+`/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		streamable.ServeHTTP(w, r)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"resource": fake.URL + "/mcp", "authorization_servers": []string{fake.URL}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer": fake.URL, "authorization_endpoint": fake.URL + "/authorize", "token_endpoint": fake.URL + "/token",
			"registration_endpoint": fake.URL + "/register", "response_types_supported": []string{"code"},
			"grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		fake.registered.Add(1)
		var meta map[string]any
		_ = json.NewDecoder(r.Body).Decode(&meta)
		meta["client_id"] = "client-1"
		writeJSON(w, http.StatusCreated, meta)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fake.lastRefresh.Store(r.Form.Get("refresh_token"))
		n := fake.tokenCalls.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "access-" + strconv.Itoa(int(n)), "token_type": "Bearer", "refresh_token": "refresh-" + strconv.Itoa(int(n)), "expires_in": 3600,
		})
	})
	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	return fake
}

func signInState(t *testing.T, page string) string {
	t.Helper()
	parsed, err := url.Parse(page)
	if err != nil || parsed.Query().Get("state") == "" || parsed.Query().Get("code_challenge") == "" {
		t.Fatalf("sign-in page = %q, %v", page, err)
	}
	if got := parsed.Query().Get("redirect_uri"); got != testBaseURL+callbackPath {
		t.Fatalf("redirect_uri = %q", got)
	}
	return parsed.Query().Get("state")
}

func TestSignInSavesTheTokenAndConnects(t *testing.T) {
	fake := newFakeServer(t, true)
	dir := t.TempDir()
	svc := New(dir, testBaseURL)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "fake", Transport: TransportHTTP, URL: fake.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	if tested, err := svc.Test(ctx, created.ID); err != nil || tested.Status != StatusNeedsSignIn || tested.StatusStep != "sign-in" {
		t.Fatalf("test before sign-in = %+v, %v", tested, err)
	}
	page, err := svc.StartSignIn(ctx, created.ID)
	if err != nil || !strings.HasPrefix(page, fake.URL+"/authorize?") {
		t.Fatalf("page = %q, %v", page, err)
	}
	done, err := svc.CompleteSignIn(ctx, signInState(t, page), "code-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusConnected || !done.SignedIn || len(done.Tools) != 1 || done.Tools[0] != "echo" {
		t.Fatalf("after sign-in = %+v", done)
	}
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil || !strings.Contains(string(raw), "refresh-1") || !strings.Contains(string(raw), "client-1") {
		t.Fatalf("the token is not in the file: %v", err)
	}
	reopened, err := New(dir, testBaseURL).Test(ctx, created.Key)
	if err != nil || reopened.Status != StatusConnected {
		t.Fatalf("after a restart = %+v, %v", reopened, err)
	}
	if out, err := svc.SignOut(ctx, created.ID); err != nil || out.SignedIn || out.Status != StatusNeedsSignIn {
		t.Fatalf("sign out = %+v, %v", out, err)
	}
}

func TestSignInFinishesFromAPastedAddress(t *testing.T) {
	fake := newFakeServer(t, true)
	svc := New(t.TempDir(), testBaseURL)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "fake", Transport: TransportHTTP, URL: fake.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Create(ctx, Input{Name: "other", Transport: TransportHTTP, URL: "https://other.example.com/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.StartSignIn(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := signInState(t, page)
	for name, address := range map[string]string{
		"another host":       "http://evil.example.com/oauth/mcp/callback?code=c&state=" + state,
		"another path":       "http://127.0.0.1:3001/other?code=c&state=" + state,
		"another state":      "http://127.0.0.1:3001/oauth/mcp/callback?code=c&state=nope",
		"two codes":          "http://127.0.0.1:3001/oauth/mcp/callback?code=c&code=d&state=" + state,
		"a login in the url": "http://u:p@127.0.0.1:3001/oauth/mcp/callback?code=c&state=" + state,
		"not an address":     "://",
	} {
		if _, err := svc.FinishFromAddress(ctx, created.ID, address); errCode(err) != "CONNECTOR_SIGNIN_ADDRESS" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := svc.FinishFromAddress(ctx, other.ID, "127.0.0.1:3001/oauth/mcp/callback?code=c&state="+state); errCode(err) != "CONNECTOR_SIGNIN_ADDRESS" {
		t.Errorf("another connector: err = %v", err)
	}
	done, err := svc.FinishFromAddress(ctx, created.Key, " 127.0.0.1:3001/oauth/mcp/callback?code=code-1&state="+state+" ")
	if err != nil || done.Status != StatusConnected {
		t.Fatalf("pasted address = %+v, %v", done, err)
	}
	if _, err := svc.CompleteSignIn(ctx, state, "code-1", "", ""); errCode(err) != "CONNECTOR_SIGNIN_NOT_FOUND" {
		t.Fatalf("a state works one time: %v", err)
	}
}

func TestSignInRefusalAndServersWithoutSignIn(t *testing.T) {
	ctx := context.Background()
	svc := New(t.TempDir(), testBaseURL)
	open := newFakeServer(t, false)
	plain, err := svc.Create(ctx, Input{Name: "plain", Transport: TransportHTTP, URL: open.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	if page, err := svc.StartSignIn(ctx, plain.ID); err != nil || page != "" {
		t.Fatalf("page = %q, %v", page, err)
	}
	if got, _ := svc.Get(ctx, plain.ID); got.Status != StatusConnected || got.SignedIn {
		t.Fatalf("plain = %+v", got)
	}

	locked := newFakeServer(t, true)
	guarded, err := svc.Create(ctx, Input{Name: "guarded", Transport: TransportHTTP, URL: locked.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.StartSignIn(ctx, guarded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSignIn(ctx, signInState(t, page), "", "", "access_denied"); errCode(err) != "CONNECTOR_SIGNIN_REFUSED" {
		t.Fatalf("refusal: %v", err)
	}
	if got, _ := svc.Get(ctx, guarded.ID); got.Status != StatusNeedsSignIn || got.SignedIn {
		t.Fatalf("after a refusal = %+v", got)
	}

	command, err := svc.Create(ctx, Input{Name: "missing", Transport: TransportStdio, Command: filepath.Join(t.TempDir(), "no-such-command")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartSignIn(ctx, command.ID); errCode(err) != "CONNECTOR_SIGNIN_NOT_SUPPORTED" {
		t.Fatalf("command sign-in: %v", err)
	}
	if tested, err := svc.Test(ctx, command.ID); err != nil || tested.Status != StatusUnavailable || tested.StatusStep != "start" {
		t.Fatalf("missing command = %+v, %v", tested, err)
	}
}

func TestAnExpiredTokenIsRenewedAndSaved(t *testing.T) {
	fake := newFakeServer(t, true)
	dir := t.TempDir()
	svc := New(dir, testBaseURL)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "fake", Transport: TransportHTTP, URL: fake.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.StartSignIn(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSignIn(ctx, signInState(t, page), "code-1", "", ""); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.records[0].OAuth.Expiry = svc.now().Add(-time.Hour)
	delete(svc.sources, created.ID)
	svc.mu.Unlock()
	if tested, err := svc.Test(ctx, created.ID); err != nil || tested.Status != StatusConnected {
		t.Fatalf("after renewal = %+v, %v", tested, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil || !strings.Contains(string(raw), "access-2") || !strings.Contains(string(raw), "refresh-2") {
		t.Fatalf("the renewed token is not in the file: %v", err)
	}
	if got := fake.lastRefresh.Load(); got != "refresh-1" || fake.registered.Load() != 1 {
		t.Fatalf("refresh used %v, registrations = %d", got, fake.registered.Load())
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestSignInAgainOnlyWhenTheServerRefusesTheSavedToken(t *testing.T) {
	fake := newFakeServer(t, true)
	svc := New(t.TempDir(), testBaseURL)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "fake", Transport: TransportHTTP, URL: fake.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.StartSignIn(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSignIn(ctx, signInState(t, page), "code-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if page, err := svc.StartSignIn(ctx, created.ID); err != nil || page != "" {
		t.Fatalf("a start with a working sign-in = %q, %v", page, err)
	}
	if got, _ := svc.Get(ctx, created.ID); !got.SignedIn || got.Status != StatusConnected || fake.registered.Load() != 1 {
		t.Fatalf("after a start with a working sign-in = %+v, registrations %d", got, fake.registered.Load())
	}

	fake.minToken.Store(5)
	page, err = svc.StartSignIn(ctx, created.ID)
	if err != nil || page == "" {
		t.Fatalf("a start after the server refused the token = %q, %v", page, err)
	}
	if got, _ := svc.Get(ctx, created.ID); got.SignedIn || got.Status != StatusNeedsSignIn {
		t.Fatalf("while the new sign-in is open = %+v", got)
	}
	fake.minToken.Store(0)
	done, err := svc.CompleteSignIn(ctx, signInState(t, page), "code-2", "", "")
	if err != nil || !done.SignedIn || done.Status != StatusConnected {
		t.Fatalf("after the new sign-in = %+v, %v", done, err)
	}
}

func TestAnEditOfTheAddressCancelsAnOpenSignIn(t *testing.T) {
	first, second := newFakeServer(t, true), newFakeServer(t, true)
	svc := New(t.TempDir(), testBaseURL)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "fake", Transport: TransportHTTP, URL: first.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.StartSignIn(ctx, created.ID)
	if err != nil || page == "" {
		t.Fatalf("start = %q, %v", page, err)
	}
	if _, err := svc.Update(ctx, created.ID, Input{Name: "fake", Transport: TransportHTTP, URL: second.URL + "/mcp", Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteSignIn(ctx, signInState(t, page), "code-1", "", ""); err == nil {
		t.Fatal("a sign-in for the old address completed on the new one")
	}
	records, err := readFile(svc.path)
	if err != nil || len(records) != 1 || records[0].OAuth != nil || records[0].URL != second.URL+"/mcp" {
		t.Fatalf("records = %+v, %v", records, err)
	}
}

func TestAStatusErrorDoesNotShowTheHiddenPartOfTheAddress(t *testing.T) {
	svc := New(t.TempDir(), testBaseURL)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "down", Transport: TransportHTTP, URL: "http://127.0.0.1:1/mcp?api_key=SECRET-VALUE", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	tested, err := svc.Test(ctx, created.ID)
	if err != nil || tested.Status != StatusUnavailable || tested.StatusError == "" || strings.Contains(tested.StatusError, "SECRET-VALUE") {
		t.Fatalf("test = %+v, %v", tested, err)
	}
	if _, err := svc.StartSignIn(ctx, created.ID); err == nil || strings.Contains(err.Error(), "SECRET-VALUE") {
		t.Fatalf("start = %v", err)
	}
	if got, _ := svc.Get(ctx, created.ID); strings.Contains(got.StatusError, "SECRET-VALUE") {
		t.Fatalf("status after a start = %+v", got)
	}
}

func TestARefusalWithoutASignInChallengeIsNotReportedAsNoSignInNeeded(t *testing.T) {
	refuses := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer refuses.Close()
	svc := New(t.TempDir(), testBaseURL)
	ctx := context.Background()
	created, err := svc.Create(ctx, Input{Name: "closed", Transport: TransportHTTP, URL: refuses.URL + "/mcp", Auth: AuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.StartSignIn(ctx, created.ID)
	if err == nil || page != "" {
		t.Fatalf("start = %q, %v", page, err)
	}
	if got, _ := svc.Get(ctx, created.ID); got.Status != StatusUnavailable || got.SignedIn {
		t.Fatalf("after the refusal = %+v", got)
	}
}
