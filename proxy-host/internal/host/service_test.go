package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	proxyapi "github.com/router-for-me/CLIProxyAPI/v8/sdk/api"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"gopkg.in/yaml.v3"
)

func TestBuildRepairsEmptyCredentialInFlightDefaults(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("credential-in-flight:\n  snapshot-interval: \"\"\n  stale-after: \"\"\n  max-part-bytes: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	routes := testRoutes(t)
	_, err := Build(root, 39001, strings.Repeat("c", 32), strings.Repeat("i", 32), routes)
	if err != nil {
		t.Fatalf("Build rejected a legacy config with an empty optional block: %v", err)
	}
	repaired, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repaired), "snapshot-interval: 2s") {
		t.Fatalf("credential-in-flight defaults were not persisted: %s", repaired)
	}
}

func TestSDKServiceStartupRetainsExactSelector(t *testing.T) {
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	routes := testRoutes(t)
	applyRoutes(t, routes, 1, testRoute("s1", "session-a", "codex", "account-a"), testRoute("s2", "session-b", "codex", "account-b"))
	boundary := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	cfg := &config.Config{Host: "127.0.0.1", Port: port, AuthDir: filepath.Join(root, "auth"), CommercialMode: true, MaxRetryCredentials: 1}
	cfg.APIKeys = []string{boundary.InferenceKey}
	cfg.Routing.Strategy = "fill-first"
	cfg.RemoteManagement.DisableControlPanel = true
	cfg.RemoteManagement.DisableAutoUpdatePanel = true
	path := filepath.Join(root, "config.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = writePrivate(path, data); err != nil {
		t.Fatal(err)
	}
	var manager *coreauth.Manager
	execution := &fakeExecutor{}
	ready := make(chan error, 1)
	service, err := cliproxy.NewBuilder().WithConfig(cfg).WithConfigPath(path).WithLocalManagementPassword(boundary.ControlKey).
		WithServerOptions(proxyapi.WithEngineConfigurator(func(engine *gin.Engine) { engine.Use(boundary.Middleware) }), proxyapi.WithRouterConfigurator(func(engine *gin.Engine, h *handlers.BaseAPIHandler, cfg *config.Config) {
			boundary.Configure(engine, h, cfg)
			manager = h.AuthManager
		})).
		WithHooks(cliproxy.Hooks{OnAfterStart: func(_ *cliproxy.Service) {
			manager.RegisterExecutor(execution)
			for _, id := range []string{"account-a", "account-b"} {
				if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: id, Provider: "codex", Status: coreauth.StatusActive}); err != nil {
					ready <- err
					return
				}
				cliproxy.GlobalModelRegistry().RegisterClient(id, "codex", []*cliproxy.ModelInfo{{ID: "probe-model"}})
			}
			ready <- nil
		}}).Build()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- service.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		select {
		case <-finished:
		case <-shutdownCtx.Done():
			t.Error("SDK service did not stop")
		}
		for _, id := range []string{"account-a", "account-b"} {
			cliproxy.GlobalModelRegistry().UnregisterClient(id)
		}
	})
	select {
	case err = <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case err = <-finished:
		t.Fatalf("startup=%v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("startup timed out")
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/responses", port)
	client := &http.Client{Timeout: 5 * time.Second}
	for _, ticket := range []string{"session-a", "session-b", "session-a"} {
		req, err := http.NewRequest("POST", url, strings.NewReader(`{"model":"probe-model","input":"fake"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+ticket)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("ticket=%s status=%d body=%s", ticket, response.StatusCode, body)
		}
	}
	if got := strings.Join(execution.recorded(), ","); got != "account-a,account-b,account-a" {
		t.Fatalf("requests=%s", got)
	}
}
func TestBuildRejectsUnsafeConfiguration(t *testing.T) {
	routes := testRoutes(t)
	for _, tc := range []struct {
		root               string
		port               int
		control, inference string
	}{
		{"relative", 1234, strings.Repeat("c", 32), strings.Repeat("i", 32)},
		{t.TempDir(), 0, strings.Repeat("c", 32), strings.Repeat("i", 32)},
		{t.TempDir(), 65536, strings.Repeat("c", 32), strings.Repeat("i", 32)},
		{t.TempDir(), 1234, "short", strings.Repeat("i", 32)},
		{t.TempDir(), 1234, strings.Repeat("c", 32), "short"},
		{t.TempDir(), 1234, strings.Repeat("c", 32), strings.Repeat("c", 32)},
	} {
		service, err := Build(tc.root, tc.port, tc.control, tc.inference, routes)
		if err == nil || service != nil {
			t.Fatalf("unsafe configuration accepted root=%s port=%d", tc.root, tc.port)
		}
	}
	if err := Run(context.Background(), "relative", 0, "", ""); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("invalid run=%v", err)
	}
}
