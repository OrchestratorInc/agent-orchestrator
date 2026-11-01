package httpd

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func TestLANManagerCompressionPreservesBoundaries(t *testing.T) {
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{HostID: "h_scratch"}, ControlDeps{})
	remote := NewMobileLAN(router, "h_scratch", 0, discardLogger(), nil)
	remote.SetPasswordHash(mobilebridge.HashPassword("test-password"))
	for _, tc := range []struct {
		name, method, path, auth, accept string
		loopback                         bool
		status                           int
		encoding                         string
	}{
		{"loopback_identity", "GET", "/api/v1/identity", "", "gzip", true, 200, ""},
		{"loopback_api_without_auth", "GET", "/api/v1/sessions", "", "gzip", true, 501, ""},
		{"remote_identity_exempt", "GET", "/api/v1/identity", "", "gzip", false, 200, "gzip"},
		{"remote_identity_rejects_gzip", "GET", "/api/v1/identity", "", "gzip;q=0", false, 200, ""},
		{"remote_identity_write_requires_auth", "POST", "/api/v1/identity", "", "gzip", false, 401, ""},
		{"remote_identity_nested_requires_auth", "GET", "/api/v1/identity/nested", "", "gzip", false, 401, ""},
		{"remote_api_requires_auth", "GET", "/api/v1/sessions", "", "gzip", false, 401, ""},
		{"remote_api_error", "GET", "/api/v1/sessions", "Bearer test-password", "gzip", false, 501, "gzip"},
		{"remote_api_error_rejects_gzip", "GET", "/api/v1/sessions", "Bearer test-password", "gzip;q=0", false, 501, ""},
		{"remote_control_block", "POST", "/shutdown", "Bearer test-password", "gzip", false, 404, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, nil)
			req.Header.Set("Accept-Encoding", tc.accept)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			recorder := httptest.NewRecorder()
			handler := remote.handler
			if tc.loopback {
				handler = router
			}
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.status || recorder.Header().Get("Content-Encoding") != tc.encoding {
				t.Fatalf("status=%d encoding=%q; want %d %q", recorder.Code, recorder.Header().Get("Content-Encoding"), tc.status, tc.encoding)
			}
			var body io.Reader = recorder.Body
			if tc.encoding == "gzip" {
				reader, err := gzip.NewReader(recorder.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				body = reader
			}
			var payload map[string]any
			if err := json.NewDecoder(body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if tc.status == http.StatusOK {
				if payload["hostId"] != "h_scratch" || payload["apiVersion"] != float64(1) {
					t.Fatalf("identity response changed: %v", payload)
				}
			} else if payload["code"] == nil || payload["message"] == nil {
				t.Fatalf("error envelope changed: %v", payload)
			}
		})
	}
}

func TestLANManagerCompressionFlushesEventStreamOnWire(t *testing.T) {
	release := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-release:
			_, _ = io.WriteString(w, "data: second\n\n")
		case <-r.Context().Done():
		}
	})
	remote := NewMobileLAN(inner, "h_scratch", 0, discardLogger(), nil)
	remote.SetPasswordHash(mobilebridge.HashPassword("test-password"))
	server := httptest.NewServer(remote.handler)
	defer server.Close()
	defer close(release)
	if !server.Listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatal("test listener must stay on loopback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events", nil)
	req.Header.Set("Authorization", "Bearer test-password")
	req.Header.Set("Accept-Encoding", "gzip")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("first event did not flush while the handler was still open: %v", err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Encoding") != "" {
		t.Fatal("event stream was encoded")
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first event was buffered or changed: %q %v", line, err)
	}
}

func TestLANManagerCompressionThroughTunnel(t *testing.T) {
	body := strings.Repeat(`{"id":"scratch-session"}`, 200)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("inner tunnel request was not HTTP/2")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	remote := NewMobileLAN(inner, "h_scratch", 0, discardLogger(), nil)
	remote.SetPasswordHash(mobilebridge.HashPassword("test-password"))
	port, err := remote.StartLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, upgrade, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d%s", port, lanTunnelPath), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer test-password"}, "Accept-Encoding": {"gzip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if upgrade.Header.Get("Content-Encoding") != "" {
		t.Fatal("WebSocket upgrade was encoded")
	}
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer conn.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{
		Protocols:          protocols,
		DisableCompression: true,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return conn, nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	for _, tc := range []struct {
		auth, accept, encoding string
		status                 int
	}{
		{"", "gzip", "", http.StatusUnauthorized},
		{"Bearer test-password", "gzip;q=0, identity;q=1", "", http.StatusOK},
		{"Bearer test-password", "gzip", "gzip", http.StatusOK},
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://tunnel/api/v1/sessions", nil)
		req.Header.Set("Authorization", tc.auth)
		req.Header.Set("Accept-Encoding", tc.accept)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.status || response.Header.Get("Content-Encoding") != tc.encoding {
			response.Body.Close()
			t.Fatalf("inner request: status=%d encoding=%q; want %d %q", response.StatusCode, response.Header.Get("Content-Encoding"), tc.status, tc.encoding)
		}
		var decoded io.Reader = response.Body
		if tc.encoding == "gzip" {
			reader, err := gzip.NewReader(response.Body)
			if err != nil {
				response.Body.Close()
				t.Fatal(err)
			}
			defer reader.Close()
			decoded = reader
		}
		got, err := io.ReadAll(decoded)
		response.Body.Close()
		if err != nil || (tc.status == http.StatusOK && string(got) != body) {
			t.Fatalf("inner response changed: %v", err)
		}
	}
}
