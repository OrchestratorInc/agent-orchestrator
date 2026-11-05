package httpd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// The real pairing service runs with counted collaborators: no LAN exposure,
// cloudflared process, or persistent user configuration is touched by these tests.
type mobileOriginLAN struct {
	mu      sync.Mutex
	running bool
	hash    string
	starts  int
}

func (l *mobileOriginLAN) Start(port int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running = true
	l.starts++
	return port, nil
}
func (l *mobileOriginLAN) StartLoopback(port int) (int, error) { return l.Start(port) }
func (l *mobileOriginLAN) Stop(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running = false
	return nil
}
func (l *mobileOriginLAN) Running() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.running }
func (l *mobileOriginLAN) BoundPort() int {
	if l.Running() {
		return 3011
	}
	return 0
}
func (l *mobileOriginLAN) SetPasswordHash(h string) { l.mu.Lock(); defer l.mu.Unlock(); l.hash = h }
func (l *mobileOriginLAN) PasswordHash() string     { l.mu.Lock(); defer l.mu.Unlock(); return l.hash }

type mobileOriginTunnel struct{ starts atomic.Int32 }

func (t *mobileOriginTunnel) Start(int)                            { t.starts.Add(1) }
func (*mobileOriginTunnel) Stop()                                  {}
func (*mobileOriginTunnel) Endpoint() *mobilebridge.TunnelEndpoint { return nil }
func (*mobileOriginTunnel) Status() mobilebridge.TunnelStatus {
	return mobilebridge.TunnelStatus{Supported: true}
}

func mobileOriginRouter(t *testing.T) (http.Handler, *controllers.BridgeService, *mobileOriginLAN, *mobileOriginTunnel) {
	t.Helper()
	lan := &mobileOriginLAN{}
	tunnel := &mobileOriginTunnel{}
	bridge := &controllers.BridgeService{LAN: lan, Tunnel: tunnel, ConfigPath: filepath.Join(t.TempDir(), "mobile", "config.json"), HostID: "test-host", PickLANHosts: func() []string { return []string{"192.0.2.1"} }, PickTailscaleHosts: func() []string { return nil }, QueryTS: func() mobilebridge.TailscaleInfo { return mobilebridge.TailscaleInfo{} }}
	router := NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer", "http://localhost:5173"}}, discardLogger(), nil, APIDeps{Mobile: &controllers.MobileController{Bridge: bridge}}, ControlDeps{})
	return router, bridge, lan, tunnel
}

func TestMobileControlRejectsHostileBrowserBeforeSideEffects(t *testing.T) {
	for _, origin := range []string{"http://standalone-1.localhost:3001", "http://ao-preview.mfxs2mi.localhost:3001", "http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost", "http://evil.example", "null"} {
		t.Run(origin, func(t *testing.T) {
			router, bridge, lan, tunnel := mobileOriginRouter(t)
			for _, path := range []string{"enable", "regenerate", "enable-lan-only", "enable-tunnel-only", "remote-access", "disable", "secure-pairing", "keep-awake", "browser-control", "status"} {
				methods := []string{http.MethodPost, http.MethodOptions}
				if path == "status" {
					methods = []string{http.MethodGet}
				}
				for _, method := range methods {
					r := httptest.NewRequest(method, "/api/v1/mobile/"+path, strings.NewReader(`{"enabled":true}`))
					r.Header.Set("Origin", origin)
					r.Header.Set("Content-Type", "text/plain")
					if method == http.MethodOptions {
						r.Header.Set("Access-Control-Request-Method", http.MethodPost)
					}
					w := httptest.NewRecorder()
					router.ServeHTTP(w, r)
					if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "ORIGIN_FORBIDDEN") || strings.Contains(w.Body.String(), `"password"`) {
						t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
					}
				}
			}
			if lan.Running() || tunnel.starts.Load() != 0 {
				t.Fatal("hostile request enabled listener or tunnel")
			}
			if _, err := os.Stat(bridge.ConfigPath); !os.IsNotExist(err) {
				t.Fatalf("hostile request persisted state: %v", err)
			}
		})
	}
}

func TestMobileControlTrustedPairingLifecycle(t *testing.T) {
	for _, origin := range []string{"app://renderer", "http://localhost:5173", ""} {
		t.Run(origin, func(t *testing.T) {
			router, bridge, lan, tunnel := mobileOriginRouter(t)
			call := func(method, path string) controllers.MobileStatusResponse {
				t.Helper()
				r := httptest.NewRequest(method, "/api/v1/mobile/"+path, nil)
				if origin != "" {
					r.Header.Set("Origin", origin)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body.String())
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("credential response is cacheable")
				}
				var status controllers.MobileStatusResponse
				if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
					t.Fatal(err)
				}
				return status
			}
			first := call(http.MethodPost, "enable")
			if !first.Enabled || first.Password == "" || !lan.Running() || tunnel.starts.Load() != 1 {
				t.Fatal("trusted enable did not start bridge")
			}
			if call(http.MethodGet, "status").Password != first.Password {
				t.Fatal("trusted status cannot redisplay pairing password")
			}
			for _, path := range []string{"status", "regenerate"} {
				method := http.MethodPost
				if path == "status" {
					method = http.MethodGet
				}
				r := httptest.NewRequest(method, "/api/v1/mobile/"+path, nil)
				r.Header.Set("Origin", "http://hostile.localhost")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), first.Password) {
					t.Fatal("hostile page reached active credentials")
				}
			}
			state, err := mobilebridge.Load(bridge.ConfigPath)
			if err != nil || state.Password != first.Password {
				t.Fatal("blocked request changed password")
			}
			next := call(http.MethodPost, "regenerate")
			if next.Password == "" || next.Password == first.Password {
				t.Fatal("trusted rotation failed")
			}
			if mobilebridge.PasswordMatches(lan.PasswordHash(), first.Password) || !mobilebridge.PasswordMatches(lan.PasswordHash(), next.Password) {
				t.Fatal("password rotation not applied to listener")
			}
			if call(http.MethodPost, "disable").Enabled || lan.Running() {
				t.Fatal("disable failed")
			}
		})
	}
}

func TestMobileControlNoOriginIsNotBrowserTrust(t *testing.T) {
	router, _, _, _ := mobileOriginRouter(t)
	for _, headers := range []http.Header{{"Sec-Fetch-Site": {"cross-site"}}, {"Sec-Fetch-Site": {"none"}}, {"Origin": {""}}, {"Origin": {"app://renderer", "http://localhost"}}} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/mobile/enable", nil)
		r.Header = headers
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("headers=%v status=%d", headers, w.Code)
		}
	}
	lan := NewMobileLAN(router, "mobile-test-host", 0, discardLogger(), nil)
	lan.SetPasswordHash(mobilebridge.HashPassword("phone-password"))
	port, err := lan.StartLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	defer lan.Stop(context.Background())
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/api/v1/mobile/enable", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer phone-password")
	req.Host = "127.0.0.1" // a forged loopback Host cannot bypass the LAN control block
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("paired phone control status=%d", resp.StatusCode)
	}
}

func TestMobileControlOriginElectron(t *testing.T) {
	binary := os.Getenv("AO_MOBILE_CONTROL_ELECTRON")
	if binary == "" {
		t.Skip("set AO_MOBILE_CONTROL_ELECTRON for real browser regression")
	}
	router, bridge, _, _ := mobileOriginRouter(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mobile-hostile-fixture" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<p>hostile local document</p>")
			return
		}
		router.ServeHTTP(w, r)
	}))
	defer srv.Close()
	script, err := filepath.Abs("../../../frontend/scripts/mobile-control-origin.integration.cjs")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{script}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox", "--disable-gpu"}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "AO_MOBILE_CONTROL_URL="+srv.URL, "AO_MOBILE_CONTROL_DATA="+t.TempDir(), "ELECTRON_RUN_AS_NODE=")
	out, err := cmd.CombinedOutput()
	t.Logf("Electron: %s", out)
	if err != nil || !strings.Contains(string(out), "AO_MOBILE_CONTROL_RESULT:0") {
		t.Fatalf("Electron regression failed: %v", err)
	}
	state, err := mobilebridge.Load(bridge.ConfigPath)
	if err != nil || !state.Enabled || state.Password == "" {
		t.Fatal("trusted pairing state missing")
	}
}
