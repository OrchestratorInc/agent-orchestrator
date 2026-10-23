package httpd

import (
	"context"
	"encoding/base64"
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

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

type muxOriginSource struct{ attached atomic.Int32 }
type muxOriginStream struct{ net.Conn }

func (muxOriginStream) Resize(uint16, uint16) error { return nil }
func (s *muxOriginSource) Attach(ctx context.Context, _ ports.RuntimeHandle, _, _ uint16) (ports.Stream, error) {
	s.attached.Add(1)
	client, server := net.Pipe()
	go func() { defer server.Close(); _, _ = io.Copy(server, server) }()
	go func() { <-ctx.Done(); _ = client.Close() }()
	return muxOriginStream{client}, nil
}
func (*muxOriginSource) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) { return true, nil }

func TestMuxRejectsUntrustedExistingHandleAttachment(t *testing.T) {
	for _, origin := range []string{"http://standalone-1.localhost:3001", "http://ao-preview.mfxs2mi.localhost:3001", "http://ao-preview-artifact.mfxs2mi.localhost:3001", "http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost", "null", "http://evil.example"} {
		t.Run(origin, func(t *testing.T) {
			source := &muxOriginSource{}
			mgr := terminal.NewManager(source, nil, discardLogger())
			defer mgr.Close()
			srv := httptest.NewServer(NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), mgr, APIDeps{}, ControlDeps{}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/mux", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {origin}, "Authorization": {"Bearer forged"}}})
			if err == nil {
				_ = wsjson.Write(ctx, c, terminalMuxFrame{Ch: "terminal", Type: "open", ID: "standalone-1"})
				readFrame(t, c, "terminal", "opened", time.Second)
				_ = wsjson.Write(ctx, c, terminalMuxFrame{Ch: "terminal", Type: "data", ID: "standalone-1", Data: base64.StdEncoding.EncodeToString([]byte("in-memory attack marker"))})
				readFrame(t, c, "terminal", "data", time.Second)
				_ = c.Close(websocket.StatusNormalClosure, "unexpected")
				t.Error("hostile page attached and wrote to an existing terminal")
			} else if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Fatalf("response=%v err=%v", resp, err)
			}
			if source.attached.Load() != 0 {
				t.Fatalf("untrusted attachments=%d", source.attached.Load())
			}
		})
	}
}

func TestMuxAuthorizedSharedAttachment(t *testing.T) {
	for _, tc := range []struct {
		name, origin string
		mobile       bool
	}{
		{"desktop", "app://renderer", false}, {"development", "http://localhost:5173", false}, {"native", "", false}, {"paired mobile", "http://localhost", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &muxOriginSource{}
			mgr := terminal.NewManager(source, nil, discardLogger())
			defer mgr.Close()
			router := NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer", "http://localhost:5173"}}, discardLogger(), mgr, APIDeps{}, ControlDeps{})
			srv := httptest.NewServer(router)
			defer srv.Close()
			endpoint := srv.URL
			var lan *LANManager
			if tc.mobile {
				lan = NewMobileLAN(router, "mux-host", 0, discardLogger(), nil)
				lan.SetPasswordHash(mobilebridge.HashPassword("paired-password"))
				port, err := lan.StartLoopback(0)
				if err != nil {
					t.Fatal(err)
				}
				defer lan.Stop(context.Background())
				endpoint = "http://127.0.0.1:" + strconv.Itoa(port)
			}
			var viewers []*websocket.Conn
			for i := 0; i < 3; i++ {
				headers := http.Header{}
				if tc.origin != "" {
					headers.Set("Origin", tc.origin)
				}
				if tc.mobile {
					headers.Set("Authorization", "Bearer paired-password")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(endpoint, "http")+"/mux", &websocket.DialOptions{HTTPHeader: headers})
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				if err = wsjson.Write(ctx, c, terminalMuxFrame{Ch: "terminal", Type: "open", ID: "existing-agent-handle"}); err != nil {
					t.Fatal(err)
				}
				readFrame(t, c, "terminal", "opened", time.Second)
				if err = wsjson.Write(ctx, c, terminalMuxFrame{Ch: "terminal", Type: "data", ID: "existing-agent-handle", Data: base64.StdEncoding.EncodeToString([]byte("safe echo"))}); err != nil {
					t.Fatal(err)
				}
				frame := readFrame(t, c, "terminal", "data", time.Second)
				data, _ := base64.StdEncoding.DecodeString(frame.Data)
				if string(data) != "safe echo" {
					t.Fatalf("echo=%q", data)
				}
				cancel()
				if i == 0 {
					_ = c.Close(websocket.StatusNormalClosure, "reconnect")
				} else {
					viewers = append(viewers, c)
				}
			}
			for _, c := range viewers {
				_ = c.Close(websocket.StatusNormalClosure, "done")
			}
			if source.attached.Load() != 3 {
				t.Fatalf("attachments=%d", source.attached.Load())
			}
			if lan != nil {
				lan.SetPasswordHash(mobilebridge.HashPassword("rotated-password"))
				_, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(endpoint, "http")+"/mux", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://localhost"}, "Authorization": {"Bearer paired-password"}}})
				if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("retired password: response=%v err=%v", resp, err)
				}
			}
		})
	}
}

func TestMuxCannotForgeBrowserTrust(t *testing.T) {
	for _, headers := range []http.Header{
		{"Sec-Fetch-Site": {"cross-site"}}, {"Origin": {""}}, {"Origin": {"app://renderer", "http://localhost"}},
		{"Origin": {"http://localhost"}, "Authorization": {"Bearer guessed"}}, {"Origin": {"null"}, "Cookie": {"ao_conn=paired-password"}},
	} {
		r := httptest.NewRequest(http.MethodGet, "/mux", nil)
		r.Header = headers
		if terminalMuxRequestAllowed(r, exactAllowedOrigins([]string{"app://renderer"})) {
			t.Fatalf("accepted forged headers=%v", headers)
		}
	}
	for _, host := range []string{"ao-preview.mfxs2mi.localhost:3001", "ao-preview-artifact.mfxs2mi.localhost:3001", "ao-inline-artifact.mfxs2mi.localhost:3001"} {
		r := httptest.NewRequest(http.MethodGet, "/mux", nil)
		r.Host = host
		r.Header.Set("Origin", "app://renderer")
		if terminalMuxRequestAllowed(r, exactAllowedOrigins([]string{"app://renderer"})) {
			t.Fatalf("preview host accepted: %s", host)
		}
	}
}

func TestMuxOriginElectron(t *testing.T) {
	binary := os.Getenv("AO_MUX_ELECTRON")
	if binary == "" {
		t.Skip("set AO_MUX_ELECTRON for real Chromium transport regression")
	}
	source := &muxOriginSource{}
	mgr := terminal.NewManager(source, nil, discardLogger())
	defer mgr.Close()
	router := NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), mgr, APIDeps{}, ControlDeps{})
	router.Get("/mux-hostile-fixture", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<p>hostile local page</p>")
	})
	srv := httptest.NewServer(router)
	defer srv.Close()
	script, err := filepath.Abs("../../../frontend/scripts/mux-origin.integration.cjs")
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
	cmd.Env = append(os.Environ(), "AO_MUX_URL="+srv.URL, "AO_MUX_DATA="+t.TempDir(), "ELECTRON_RUN_AS_NODE=")
	out, err := cmd.CombinedOutput()
	t.Logf("Electron: %s", out)
	if err != nil || !strings.Contains(string(out), "AO_MUX_RESULT:0") {
		t.Fatalf("Electron regression: %v", err)
	}
	if source.attached.Load() != 2 {
		t.Fatalf("attachments=%d want=2 trusted connections", source.attached.Load())
	}
}
