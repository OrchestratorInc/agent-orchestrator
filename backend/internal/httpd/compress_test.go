package httpd

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func TestCompressJSON(t *testing.T) {
	big := `{"items":"` + strings.Repeat("x", 4096) + `"}`
	small := `{"ok":true}`
	cases := []struct {
		name, path, acceptEncoding, contentType, contentEncoding, body string
		wantGzip                                                       bool
	}{
		{name: "large JSON, gzip accepted", path: "/api/v1/sessions", acceptEncoding: "gzip, deflate, br", contentType: "application/json; charset=utf-8", body: big, wantGzip: true},
		{name: "no Accept-Encoding", path: "/api/v1/sessions", contentType: "application/json", body: big},
		{name: "gzip refused", path: "/api/v1/sessions", acceptEncoding: "gzip;q=0", contentType: "application/json", body: big},
		{name: "small JSON", path: "/api/v1/sessions", acceptEncoding: "gzip", contentType: "application/json", body: small},
		{name: "not JSON", path: "/api/v1/sessions/s1/attachments/a", acceptEncoding: "gzip", contentType: "application/octet-stream", body: big},
		{name: "already encoded", path: "/api/v1/sessions", acceptEncoding: "gzip", contentType: "application/json", contentEncoding: "br", body: big},
		{name: "preview proxy", path: "/api/v1/sessions/s1/preview/app/data.json", acceptEncoding: "gzip", contentType: "application/json", body: big},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := compressJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				if tc.contentEncoding != "" {
					w.Header().Set("Content-Encoding", tc.contentEncoding)
				}
				w.WriteHeader(http.StatusCreated)
				// Two writes: the threshold counts the whole body, not one write.
				half := len(tc.body) / 2
				io.WriteString(w, tc.body[:half])
				io.WriteString(w, tc.body[half:])
			}))
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201", rec.Code)
			}
			body := rec.Body.Bytes()
			if gotGzip := rec.Header().Get("Content-Encoding") == "gzip"; gotGzip != tc.wantGzip {
				t.Fatalf("Content-Encoding = %q, want gzip=%v", rec.Header().Get("Content-Encoding"), tc.wantGzip)
			}
			if tc.wantGzip {
				if rec.Header().Get("Vary") != "Accept-Encoding" {
					t.Errorf("Vary = %q, want Accept-Encoding", rec.Header().Get("Vary"))
				}
				zr, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatalf("gzip reader: %v", err)
				}
				if body, err = io.ReadAll(zr); err != nil {
					t.Fatalf("gunzip: %v", err)
				}
			}
			if string(body) != tc.body {
				t.Fatalf("body = %.40q..., want %.40q...", body, tc.body)
			}
		})
	}
}

func TestCompressJSONLeavesEventStreamsStreaming(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(compressJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", strings.Repeat("x", 2048))
		w.(http.Flusher).Flush()
		<-release
	})))
	defer srv.Close()
	defer close(release)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events", nil)
	req.Header.Set("Accept-Encoding", "gzip") // set by hand: the transport leaves the body as sent
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("Content-Encoding = %q, want none on an event stream", ce)
	}
	// The handler is still blocked: the event arrives only if Flush reached the wire.
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "data: xxx") {
		t.Fatalf("first event line = %.20q, err %v", line, err)
	}
}

func TestCompressJSONThroughTheLANTunnel(t *testing.T) {
	big := `{"items":"` + strings.Repeat("x", 4096) + `"}`
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(compressJSON(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, big)
	})), st, 0, slog.Default(), nil)
	port, err := m.StartLoopback(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	ws, _, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d%s", port, lanTunnelPath), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer secret12"}},
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	defer conn.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{
		Protocols:   protocols,
		DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil },
	}}
	req, _ := http.NewRequest(http.MethodGet, "http://tunnel/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp, err := client.Do(req) // the transport asks for gzip and decodes it
	if err != nil {
		t.Fatalf("request through the tunnel: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !resp.Uncompressed || string(body) != big {
		t.Fatalf("through the tunnel: uncompressed=%v, body %d bytes; want a gzipped response decoding to %d bytes", resp.Uncompressed, len(body), len(big))
	}
}
