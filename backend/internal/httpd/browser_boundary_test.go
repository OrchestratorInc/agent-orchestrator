package httpd

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	shelltermsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

// No real shell or command is started: these fakes expose whether the HTTP
// boundary reached creation or attachment, and echo terminal bytes in memory.
type boundaryShells struct {
	opened     atomic.Int32
	otherCalls atomic.Int32
}

func (s *boundaryShells) OpenShellTerminal(context.Context, shelltermsvc.OpenShellTerminalInput) (shelltermsvc.ShellTerminal, error) {
	s.opened.Add(1)
	return shelltermsvc.ShellTerminal{HandleID: "shellterm-boundary"}, nil
}
func (s *boundaryShells) ListShellTerminalsForCurrentAppRun(context.Context) ([]shelltermsvc.ShellTerminal, error) {
	s.otherCalls.Add(1)
	return nil, nil
}
func (*boundaryShells) RenameShellTerminal(context.Context, string, string) (shelltermsvc.ShellTerminal, error) {
	return shelltermsvc.ShellTerminal{}, nil
}
func (*boundaryShells) CloseShellTerminal(context.Context, string) error { return nil }

type boundarySource struct{ attached atomic.Int32 }
type boundaryStream struct{ net.Conn }

func (boundaryStream) Resize(uint16, uint16) error { return nil }
func (s *boundarySource) Attach(ctx context.Context, _ ports.RuntimeHandle, _, _ uint16) (ports.Stream, error) {
	s.attached.Add(1)
	client, server := net.Pipe()
	go func() { defer server.Close(); _, _ = io.Copy(server, server) }()
	go func() { <-ctx.Done(); _ = client.Close() }()
	return boundaryStream{client}, nil
}
func (*boundarySource) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) { return true, nil }

func TestBrowserBoundaryRejectsShellCreationAndMux(t *testing.T) {
	for _, origin := range []string{
		"http://standalone-1.localhost:3001", "http://ao-preview.mfxs2mi.localhost:3001",
		"http://ao-preview-artifact.mfxs2mi.localhost:3001", "http://ao-inline-artifact.mfxs2mi.localhost:3001",
		"http://localhost", "http://127.0.0.1:8080", "http://[::1]:8080", "http://evil.example", "null",
	} {
		t.Run(origin, func(t *testing.T) {
			shells := &boundaryShells{}
			source := &boundarySource{}
			mgr := terminal.NewManager(source, nil, discardLogger())
			defer mgr.Close()
			router := NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer", "http://localhost:5173"}}, discardLogger(), mgr, APIDeps{ShellTerminals: shells}, ControlDeps{})
			srv := httptest.NewServer(router)
			defer srv.Close()
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
				req, _ := http.NewRequest(method, srv.URL+"/api/v1/shell-terminals", nil)
				req.Header.Set("Origin", origin)
				req.Header.Set("Content-Type", "text/plain")
				if method == http.MethodOptions {
					req.Header.Set("Access-Control-Request-Method", "POST")
				}
				resp, err := srv.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "ORIGIN_FORBIDDEN") {
					t.Errorf("%s: status=%d body=%s", method, resp.StatusCode, body)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/mux", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {origin}}})
			if err == nil {
				_ = wsjson.Write(ctx, conn, terminalMuxFrame{Ch: "terminal", Type: "open", ID: "existing-handle"})
				readFrame(t, conn, "terminal", "opened", time.Second)
				_ = conn.Close(websocket.StatusNormalClosure, "unexpected upgrade")
				t.Error("hostile browser upgraded /mux")
			} else if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Errorf("mux response=%v error=%v", resp, err)
			}
			if shells.opened.Load() != 0 || shells.otherCalls.Load() != 0 || source.attached.Load() != 0 {
				t.Errorf("side effects: shells=%d attachments=%d", shells.opened.Load(), source.attached.Load())
			}
		})
	}
}

func TestBrowserBoundaryAuthorizedClients(t *testing.T) {
	for _, tc := range []struct {
		name, origin string
		mobile       bool
	}{
		{"desktop", "app://renderer", false}, {"development", "http://localhost:5173", false}, {"native CLI", "", false}, {"paired phone", "http://localhost", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shells := &boundaryShells{}
			source := &boundarySource{}
			mgr := terminal.NewManager(source, nil, discardLogger())
			defer mgr.Close()
			var handler http.Handler = NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer", "http://localhost:5173"}}, discardLogger(), mgr, APIDeps{ShellTerminals: shells}, ControlDeps{})
			var base string
			if tc.mobile {
				mobile := NewMobileLAN(handler, "boundary-host", 0, discardLogger(), nil)
				mobile.SetPasswordHash(mobilebridge.HashPassword("test-password"))
				port, err := mobile.StartLoopback(0)
				if err != nil {
					t.Fatal(err)
				}
				defer mobile.Stop(context.Background())
				base = "http://127.0.0.1:" + strconv.Itoa(port)
			} else {
				srv := httptest.NewServer(handler)
				defer srv.Close()
				base = srv.URL
			}
			headers := http.Header{}
			if tc.origin != "" {
				headers.Set("Origin", tc.origin)
			}
			if tc.mobile {
				headers.Set("Authorization", "Bearer test-password")
			}
			req, _ := http.NewRequest(http.MethodPost, base+"/api/v1/shell-terminals", nil)
			req.Header = headers.Clone()
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated || shells.opened.Load() != 1 {
				t.Fatalf("create: status=%d calls=%d", resp.StatusCode, shells.opened.Load())
			}
			// Two viewers plus a reconnect must still attach the same durable handle.
			var connections []*websocket.Conn
			defer func() {
				for _, conn := range connections {
					_ = conn.Close(websocket.StatusNormalClosure, "done")
				}
			}()
			for i := range 3 {
				if i == 2 {
					_ = connections[0].Close(websocket.StatusNormalClosure, "reconnect")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/mux", &websocket.DialOptions{HTTPHeader: headers.Clone()})
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				connections = append(connections, conn)
				err = wsjson.Write(ctx, conn, terminalMuxFrame{Ch: "terminal", Type: "open", ID: "shellterm-boundary"})
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				readFrame(t, conn, "terminal", "opened", time.Second)
				data := base64.StdEncoding.EncodeToString([]byte("compatibility echo"))
				if err := wsjson.Write(ctx, conn, terminalMuxFrame{Ch: "terminal", Type: "data", ID: "shellterm-boundary", Data: data}); err != nil {
					cancel()
					t.Fatal(err)
				}
				got := readFrame(t, conn, "terminal", "data", time.Second)
				if got.Data != data {
					t.Fatalf("echo=%q want %q", got.Data, data)
				}
			}
			if source.attached.Load() != 3 {
				t.Fatalf("attachments=%d want 3", source.attached.Load())
			}
		})
	}
}

func TestBrowserBoundaryCannotForgeNativeOrMobileTrust(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers http.Header
	}{
		{"no-cors browser", http.Header{"Sec-Fetch-Site": {"cross-site"}}},
		{"same-origin browser", http.Header{"Sec-Fetch-Site": {"same-origin"}}},
		{"duplicate origin", http.Header{"Origin": {"app://renderer", "http://hostile.localhost"}}},
		{"empty origin", http.Header{"Origin": {""}}},
		{"forged bearer on primary", http.Header{"Origin": {"http://localhost"}, "Authorization": {"Bearer test-password"}}},
		{"forged auth marker", http.Header{"Origin": {"http://localhost"}, "X-AO-Authenticated": {"true"}, "X-Forwarded-For": {"127.0.0.1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shells := &boundaryShells{}
			router := NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), nil, APIDeps{ShellTerminals: shells}, ControlDeps{})
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3001/api/v1/shell-terminals", nil)
			req.Header = tc.headers
			req.Host = "rebound.attacker.example:3001"
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden || shells.opened.Load() != 0 {
				t.Fatalf("status=%d shells=%d", w.Code, shells.opened.Load())
			}
		})
	}
}

func TestBrowserBoundaryMobileCredentialsAndPreviewCookie(t *testing.T) {
	state := &authState{}
	state.setHash(mobilebridge.HashPassword("test-password"))
	shells := &boundaryShells{}
	router := NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), nil, APIDeps{ShellTerminals: shells}, ControlDeps{})
	handler := authMiddleware(state, newLockout(time.Now), nil)(router)
	for _, tc := range []struct {
		name, token, cookie, origin string
		want                        int
	}{
		{"missing", "", "", "http://localhost", 401},
		{"wrong", "bad", "", "http://localhost", 401},
		{"preview cookie", "", "test-password", "http://localhost", 401},
		{"paired phone", "test-password", "", "http://localhost", 201},
		{"authenticated hostile browser", "test-password", "", "http://hostile.localhost", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/shell-terminals", nil)
			req.Header.Set("Origin", tc.origin)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: authCookieName, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if shells.opened.Load() != 1 {
		t.Fatalf("shells=%d want=1", shells.opened.Load())
	}
	state.setHash(mobilebridge.HashPassword("rotated"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shell-terminals", nil)
	req.Header.Set("Authorization", "Bearer test-password")
	req.Header.Set("Origin", "http://localhost")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("retired credential: %d", w.Code)
	}
}

func TestBrowserBoundaryPreviewHostsCannotDispatchAPI(t *testing.T) {
	for _, host := range []string{"ao-preview.mfxs2mi.localhost:3001", "ao-preview-artifact.mfxs2mi.localhost:3001", "ao-inline-artifact.mfxs2mi.localhost:3001"} {
		router := newTestRouter(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), nil)
		for _, path := range []string{"/api/v1/shell-terminals", "/mux"} {
			req := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
			req.Header.Set("Origin", "http://"+host)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Errorf("host=%s path=%s status=%d body=%s", host, path, w.Code, w.Body.String())
			}
		}
	}
}

// The separate Electron process uses Chromium's real headers for app://renderer
// and hostile documents. Optional locally; the frontend workflow enables it.
func TestBrowserBoundaryElectron(t *testing.T) {
	electronBinary := os.Getenv("AO_BOUNDARY_ELECTRON")
	if electronBinary == "" {
		t.Skip("set AO_BOUNDARY_ELECTRON to the installed Electron executable")
	}
	shells := &boundaryShells{}
	source := &boundarySource{}
	mgr := terminal.NewManager(source, nil, discardLogger())
	defer mgr.Close()
	dataDir := t.TempDir()
	page := `<script>parent.postMessage("boundary-render-loaded", "*")</script><p id="render-marker">render loaded</p>`
	if err := attachmentstore.New(dataDir).PutRender(context.Background(), "boundary", "r1", []byte(page)); err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithControl(config.Config{DataDir: dataDir, AllowedOrigins: []string{"app://renderer"}}, discardLogger(), mgr, APIDeps{ShellTerminals: shells}, ControlDeps{})
	// Protected media fixture runs through the router's actual browser guard.
	// Element transports are the same as workspace blobs and streamed previews.
	router.Get("/boundary-media/{file}", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			w.Header().Set("Content-Type", "image/png")
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
			_, _ = w.Write(png)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		wav := make([]byte, 44+1600)
		copy(wav, "RIFF")
		binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
		copy(wav[8:], "WAVEfmt ")
		binary.LittleEndian.PutUint32(wav[16:], 16)
		binary.LittleEndian.PutUint16(wav[20:], 1)
		binary.LittleEndian.PutUint16(wav[22:], 1)
		binary.LittleEndian.PutUint32(wav[24:], 8000)
		binary.LittleEndian.PutUint32(wav[28:], 16000)
		binary.LittleEndian.PutUint16(wav[32:], 2)
		binary.LittleEndian.PutUint16(wav[34:], 16)
		copy(wav[36:], "data")
		binary.LittleEndian.PutUint32(wav[40:], 1600)
		_, _ = w.Write(wav)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/boundary-fixture/") {
			// Header emission on actual preview routes is tested in controllers;
			// this fixture checks Chromium's enforcement and resulting request headers.
			if strings.HasSuffix(r.URL.Path, "/sandbox") {
				w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms")
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<title>untrusted document fixture</title>")
			return
		}
		router.ServeHTTP(w, r)
	}))
	defer srv.Close()
	script, err := filepath.Abs("../../../frontend/scripts/browser-boundary.integration.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{script}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox", "--disable-gpu"}, args...)
	}
	cmd := exec.CommandContext(ctx, electronBinary, args...)
	cmd.Env = append(os.Environ(), "AO_BOUNDARY_URL="+srv.URL, "AO_BOUNDARY_DATA="+t.TempDir(), "ELECTRON_RUN_AS_NODE=")
	output, err := cmd.CombinedOutput()
	t.Logf("Electron: %s", output)
	if err != nil || !strings.Contains(string(output), "AO_BOUNDARY_RESULT:0") {
		t.Fatalf("Electron compatibility failed: %v", err)
	}
	if shells.opened.Load() != 1 || source.attached.Load() != 1 {
		t.Fatalf("unexpected browser effects: shells=%d attachments=%d", shells.opened.Load(), source.attached.Load())
	}
}

func TestBrowserBoundaryPreviewCookieGrantsOnlyPreviewBytes(t *testing.T) {
	state := &authState{}
	state.setHash(mobilebridge.HashPassword("preview-password"))
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	handler := authMiddleware(state, newLockout(time.Now), nil)(router)
	for _, path := range []string{"/api/v1/sessions/s1/preview/files/asset.js", "/api/v1/shell-terminals", "/mux"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Origin", "null")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: "preview-password"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		want := http.StatusUnauthorized
		if strings.HasSuffix(path, "asset.js") {
			want = http.StatusNotImplemented
		}
		if w.Code != want {
			t.Errorf("path=%s status=%d want=%d body=%s", path, w.Code, want, w.Body.String())
		}
	}
}
