package coderapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const testToken = "test-session-token-value"

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	base, err := ParseDeploymentURL(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(Credentials{URL: base, Token: testToken}, server.Client().Transport)
}

// The session token is a custom header, which Go's redirect handling would
// forward to any origin. A deployment that redirects elsewhere must never
// see the token reach that origin.
func TestClientRefusesCrossOriginRedirectWithoutLeakingToken(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(SessionTokenHeader) != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte(`{"id":"intruder"}`))
	}))
	defer other.Close()
	deployment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer deployment.Close()

	_, err := testClient(t, deployment).Me(context.Background())
	if !errors.Is(err, ErrCrossOriginRedirect) {
		t.Fatalf("Me() error = %v, want ErrCrossOriginRedirect", err)
	}
	if leaked.Load() {
		t.Fatal("session token was sent to another origin")
	}
}

func TestClientRefusesHTTPSDowngradeAndPortChange(t *testing.T) {
	base, _ := url.Parse("https://coder.example.com")
	check := sameOriginRedirects(base)
	for _, target := range []string{
		"http://coder.example.com/api/v2/users/me",
		"https://coder.example.com:8443/api/v2/users/me",
		"https://evil.example.com/api/v2/users/me",
		"wss://evil.example.com/api/v2/chats/x/stream",
	} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if err := check(req, []*http.Request{{}}); !errors.Is(err, ErrCrossOriginRedirect) {
			t.Fatalf("redirect to %s = %v, want refusal", target, err)
		}
	}
	for _, target := range []string{
		"https://coder.example.com:443/api/v2/users/me",
		"https://CODER.example.com/login",
		"wss://coder.example.com/api/v2/chats/x/stream",
	} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if err := check(req, []*http.Request{{}}); err != nil {
			t.Fatalf("same-origin redirect to %s refused: %v", target, err)
		}
	}
}

func TestClientFollowsSameOriginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("moved") == "" {
			http.Redirect(w, r, "/api/v2/users/me?moved=1", http.StatusTemporaryRedirect)
			return
		}
		if r.Header.Get(SessionTokenHeader) != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"u1","username":"dev","organization_ids":["o1"]}`))
	}))
	defer server.Close()
	user, err := testClient(t, server).Me(context.Background())
	if err != nil || user.ID != "u1" || user.Username != "dev" {
		t.Fatalf("Me() = %+v, %v", user, err)
	}
}

func TestClientDecodesErrorsWithoutRequestSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"You must be logged in","detail":"session expired"}`))
	}))
	defer server.Close()
	_, err := testClient(t, server).Me(context.Background())
	if StatusCode(err) != http.StatusUnauthorized {
		t.Fatalf("status = %d (%v), want 401", StatusCode(err), err)
	}
	if strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "session expired") {
		t.Fatalf("error text = %q", err)
	}
}

func TestCreateChatSendsHiddenSystemPromptToolsAndLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/chats" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := decodeJSON(r, &body); err != nil {
			t.Error(err)
		}
		if _, hasWorkspace := body["workspace_id"]; hasWorkspace {
			t.Error("create chat sent a workspace_id")
		}
		if body["system_prompt"] != "hidden" || body["client_type"] != "api" {
			t.Errorf("create body = %v", body)
		}
		if tools, _ := body["unsafe_dynamic_tools"].([]any); len(tools) != 1 {
			t.Errorf("dynamic tools = %v", body["unsafe_dynamic_tools"])
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"c1","status":"pending","labels":{"ao.conversation":"k"}}`))
	}))
	defer server.Close()
	chat, err := testClient(t, server).CreateChat(context.Background(), CreateChatRequest{
		OrganizationID: "o1", Content: TextInput("hi"), SystemPrompt: "hidden", ClientType: "api",
		Labels:             map[string]string{"ao.conversation": "k"},
		UnsafeDynamicTools: []DynamicTool{{Name: "ao_read_file", InputSchema: []byte(`{"type":"object"}`)}},
	})
	if err != nil || chat.ID != "c1" || chat.HasWorkspace() {
		t.Fatalf("CreateChat = %+v, %v", chat, err)
	}
}

func TestStreamChatReadsBatchesWithTokenAndAfterID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(SessionTokenHeader) != testToken || r.URL.Query().Get("after_id") != "7" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_ = wsjson.Write(r.Context(), conn, []StreamEvent{
			{Type: EventStatus, Status: &StreamStatus{Status: ChatStatusRunning}},
			{Type: EventMessagePart, MessagePart: &StreamMessagePart{Role: RoleAssistant, Part: ChatMessagePart{Type: PartText, Text: "hi"}}},
		})
	}))
	defer server.Close()
	stream, err := testClient(t, server).StreamChat(context.Background(), "c1", 7)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	batch, err := stream.Next(context.Background())
	if err != nil || len(batch) != 2 || batch[1].MessagePart.Part.Text != "hi" {
		t.Fatalf("Next() = %+v, %v", batch, err)
	}
}

func TestStreamChatReportsHandshakeStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	_, err := testClient(t, server).StreamChat(context.Background(), "missing", 0)
	if StatusCode(err) != http.StatusNotFound {
		t.Fatalf("StreamChat error = %v, want 404", err)
	}
}

func TestResolveCredentialsPrefersEnvironment(t *testing.T) {
	env := map[string]string{EnvURL: "https://coder.example.com/", EnvSessionToken: "env-token"}
	creds, err := ResolveCredentials(context.Background(), mapEnv(env), func(context.Context, map[string]string) ([]byte, error) {
		t.Fatal("token command ran although CODER_SESSION_TOKEN is set")
		return nil, nil
	})
	if err != nil || creds.Token != "env-token" || creds.URL.String() != "https://coder.example.com" {
		t.Fatalf("ResolveCredentials = %+v, %v", creds, err)
	}
	if creds.Source.Source != EnvSessionToken {
		t.Fatalf("source = %+v", creds.Source)
	}
}

func TestResolveCredentialsReadsCLIConfigAndTokenCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "url"), []byte("https://coder.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{EnvConfigDir: dir}
	creds, err := ResolveCredentials(context.Background(), mapEnv(env), func(_ context.Context, extra map[string]string) ([]byte, error) {
		if len(extra) != 0 {
			t.Errorf("token command env = %v; the URL came from the CLI's own config", extra)
		}
		return []byte("cli-token\n"), nil
	})
	if err != nil || creds.Token != "cli-token" || creds.URL.Host != "coder.example.com" {
		t.Fatalf("ResolveCredentials = %+v, %v", creds, err)
	}
	if !strings.Contains(creds.Source.Source, "login token") {
		t.Fatalf("source = %+v", creds.Source)
	}
}

func TestResolveCredentialsNotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveCredentials(context.Background(), mapEnv(map[string]string{EnvConfigDir: dir}), nil)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("no url: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "url"), []byte("https://coder.example.com"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveCredentials(context.Background(), mapEnv(map[string]string{EnvConfigDir: dir}), func(context.Context, map[string]string) ([]byte, error) {
		return []byte("no session token found - run 'coder login' first"), errors.New("exit status 1")
	})
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("no token: %v", err)
	}
}

func TestParseDeploymentURLRejectsUnsafeForms(t *testing.T) {
	for _, raw := range []string{"coder.example.com", "ftp://coder.example.com", "https://user:pw@coder.example.com", "https://coder.example.com/?x=1"} {
		if _, err := ParseDeploymentURL(raw); err == nil {
			t.Fatalf("ParseDeploymentURL(%q) accepted", raw)
		}
	}
}

func TestResolveOrganization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"o1","name":"first"},{"id":"o2","name":"main","is_default":true}]`))
	}))
	defer server.Close()
	client := testClient(t, server)
	org, err := ResolveOrganization(context.Background(), client, "")
	if err != nil || org.ID != "o2" {
		t.Fatalf("default org = %+v, %v", org, err)
	}
	org, err = ResolveOrganization(context.Background(), client, "FIRST")
	if err != nil || org.ID != "o1" {
		t.Fatalf("named org = %+v, %v", org, err)
	}
	if _, err := ResolveOrganization(context.Background(), client, "other"); err == nil {
		t.Fatal("non-member organization accepted")
	}
}

func mapEnv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func decodeJSON(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}
