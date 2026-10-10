package host

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	filestore "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestLoginTagUsesOnlyAOPrivateRequestContext(t *testing.T) {
	cases := []struct {
		name, id string
		context  bool
		want     bool
	}{
		{"valid", "attempt-one", true, true},
		{"no-header", "", true, false},
		{"no-context", "attempt-one", false, false},
		{"boundary-length", strings.Repeat("a", 128), true, true},
		{"over-limit", strings.Repeat("a", 129), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.context {
				header := make(http.Header)
				header.Set("X-AO-Login-ID", tc.id)
				ctx = coreauth.WithRequestInfo(ctx, &coreauth.RequestInfo{Headers: header})
			}
			a := &coreauth.Auth{ID: "account", Provider: "codex", Metadata: map[string]any{"email": "alice@example.test", "access_token": "fake-private-token"}}
			if err := tagLogin(ctx, a); err != nil {
				t.Fatal(err)
			}
			value, ok := a.Metadata["ao_login_id"]
			if ok != tc.want || (ok && value != tc.id) {
				t.Fatalf("marker=%v present=%v", value, ok)
			}
			if a.Metadata["email"] != "alice@example.test" || a.Metadata["access_token"] != "fake-private-token" {
				t.Fatal("login correlation changed provider credentials")
			}
		})
	}
}
func TestLoginTagAllocatesMetadataWithoutMutatingHeaders(t *testing.T) {
	header := make(http.Header)
	header.Set("X-AO-Login-ID", "attempt")
	ctx := coreauth.WithRequestInfo(context.Background(), &coreauth.RequestInfo{Headers: header})
	a := &coreauth.Auth{ID: "account"}
	if err := tagLogin(ctx, a); err != nil {
		t.Fatal(err)
	}
	if a.Metadata["ao_login_id"] != "attempt" || header.Get("X-AO-Login-ID") != "attempt" {
		t.Fatalf("metadata=%v headers=%v", a.Metadata, header)
	}
	if err := tagLogin(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
func TestLoginCorrelationSurvivesPublicSDKCredentialPersistence(t *testing.T) {
	root := t.TempDir()
	store := filestore.NewFileTokenStore()
	store.SetBaseDir(root)
	header := make(http.Header)
	header.Set("X-AO-Login-ID", "browser-attempt-1")
	ctx := coreauth.WithRequestInfo(context.Background(), &coreauth.RequestInfo{Headers: header})
	a := &coreauth.Auth{ID: "account.json", FileName: "account.json", Provider: "codex", Metadata: map[string]any{"type": "codex", "email": "alice@example.test", "access_token": "fake-access-only", "refresh_token": "fake-refresh-only"}}
	if err := tagLogin(ctx, a); err != nil {
		t.Fatal(err)
	}
	path, err := store.Save(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("credential mode=%v err=%v", stat, err)
	}
	if filepath.Dir(path) != root {
		t.Fatalf("credential escaped chosen storage=%s", path)
	}
	reloadedStore := filestore.NewFileTokenStore()
	reloadedStore.SetBaseDir(root)
	reloaded, err := reloadedStore.List(context.Background())
	if err != nil || len(reloaded) != 1 {
		t.Fatalf("reloaded=%+v err=%v", reloaded, err)
	}
	if reloaded[0].Metadata["ao_login_id"] != "browser-attempt-1" || reloaded[0].Metadata["email"] != "alice@example.test" || reloaded[0].Provider != "codex" {
		t.Fatalf("correlation lost=%+v", reloaded[0])
	}
}
func TestLoginResultReturnsOnlyExactMarkedIdentityToControlClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := testRoutes(t)
	manager := coreauth.NewManager(nil, nil, nil)
	for _, a := range []*coreauth.Auth{
		{ID: "other", FileName: "other.json", Provider: "codex", Metadata: map[string]any{"ao_login_id": "other-attempt", "email": "other@example.test", "access_token": "PRIVATE-OTHER"}},
		{ID: "wanted", FileName: "wanted.json", Provider: "claude", Metadata: map[string]any{"ao_login_id": "wanted-attempt", "email": "wanted@example.test", "access_token": "PRIVATE-ACCESS", "refresh_token": "PRIVATE-REFRESH"}},
	} {
		if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), a); err != nil {
			t.Fatal(err)
		}
	}
	boundary := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	engine := gin.New()
	engine.Use(boundary.Middleware)
	boundary.Configure(engine, handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager), &config.Config{})
	out := boundaryRequest(engine, "GET", "/ao/login-result/wanted-attempt", boundary.ControlKey, "", nil)
	if out.Code != 200 {
		t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(out.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["email"] != "wanted@example.test" || got["provider"] != "claude" || got["credential_ref"] != "wanted.json" || got["auth_id"] != "wanted" {
		t.Fatalf("identity=%v", got)
	}
	for _, secret := range []string{"PRIVATE", "access_token", "refresh_token", "other@example.test"} {
		if strings.Contains(out.Body.String(), secret) {
			t.Fatalf("login result leaked %s", secret)
		}
	}
	if out = boundaryRequest(engine, "GET", "/ao/login-result/unknown", boundary.ControlKey, "", nil); out.Code != 404 {
		t.Fatalf("unknown attempt status=%d", out.Code)
	}
	for _, token := range []string{"", boundary.InferenceKey, "session-ticket"} {
		if out = boundaryRequest(engine, "GET", "/ao/login-result/wanted-attempt", token, "", nil); out.Code != 401 {
			t.Fatalf("non-control login-result status=%d", out.Code)
		}
	}
}

func TestLoginResultFindsNativeImportedCredentialByReservedFileName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := testRoutes(t)
	manager := coreauth.NewManager(nil, nil, nil)
	a := &coreauth.Auth{ID: "ao-import-attempt.json", FileName: "ao-import-attempt.json", Provider: "codex", Metadata: map[string]any{"email": "imported@example.test"}}
	if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), a); err != nil {
		t.Fatal(err)
	}
	boundary := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	engine := gin.New()
	engine.Use(boundary.Middleware)
	boundary.Configure(engine, handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager), &config.Config{})
	out := boundaryRequest(engine, "GET", "/ao/login-result/import-attempt", boundary.ControlKey, "", nil)
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"credential_ref":"ao-import-attempt.json"`) {
		t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
	}
}
