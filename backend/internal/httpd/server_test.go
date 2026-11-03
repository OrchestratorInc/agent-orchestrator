package httpd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthProbes(t *testing.T) {
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("GET %s Content-Type = %q, want JSON", path, ct)
		}
	}
}

func TestHealthProbesIncludeDaemonIdentity(t *testing.T) {
	router := newTestRouter(config.Config{StartupWorkingDirectory: "/startup"}, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	wantExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		var body struct {
			ExecutablePath          string `json:"executablePath"`
			WorkingDirectory        string `json:"workingDirectory"`
			StartupWorkingDirectory string `json:"startupWorkingDirectory"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if body.ExecutablePath != wantExe {
			t.Errorf("GET %s executablePath = %q, want %q", path, body.ExecutablePath, wantExe)
		}
		if body.WorkingDirectory != wantCWD {
			t.Errorf("GET %s workingDirectory = %q, want %q", path, body.WorkingDirectory, wantCWD)
		}
		if body.StartupWorkingDirectory != "/startup" {
			t.Errorf("GET %s startupWorkingDirectory = %q, want /startup", path, body.StartupWorkingDirectory)
		}
	}
}

func TestHealthProbesIncludeAppImageIdentity(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}

	t.Run("reports AO_APPIMAGE when set", func(t *testing.T) {
		t.Setenv("AO_APPIMAGE", "/home/user/Apps/agent-orchestrator.AppImage")
		router := newTestRouter(config.Config{}, discardLogger(), nil)
		srv := httptest.NewServer(router)
		defer srv.Close()

		for _, path := range []string{"/healthz", "/readyz"} {
			resp, err := client.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			var body struct {
				AppImagePath string `json:"appImagePath"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			if body.AppImagePath != "/home/user/Apps/agent-orchestrator.AppImage" {
				t.Errorf("GET %s appImagePath = %q, want the AO_APPIMAGE value", path, body.AppImagePath)
			}
		}
	})

	t.Run("omits appImagePath when AO_APPIMAGE is unset", func(t *testing.T) {
		t.Setenv("AO_APPIMAGE", "")
		router := newTestRouter(config.Config{}, discardLogger(), nil)
		srv := httptest.NewServer(router)
		defer srv.Close()

		for _, path := range []string{"/healthz", "/readyz"} {
			resp, err := client.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			if _, ok := body["appImagePath"]; ok {
				t.Errorf("GET %s appImagePath present, want omitted", path)
			}
		}
	})
}

// TestServerLifecycle exercises the full Run loop: bind an ephemeral port,
// publish running.json, serve a request, then cancel the context and confirm a
// clean shutdown that removes the handshake file.
func TestServerLifecycle(t *testing.T) {
	runPath := filepath.Join(t.TempDir(), "running.json")
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0, // let the OS pick a free port — no conflict with a real daemon
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}

	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	// Wait for the handshake file to confirm the server is up.
	base := "http://" + srv.Addr().String()
	waitForHealth(t, base)

	info, err := runfile.Read(runPath)
	if err != nil {
		t.Fatalf("read run-file: %v", err)
	}
	if info == nil {
		t.Fatal("run-file not written while server running")
		return
	}
	if info.Port == 0 {
		t.Error("run-file recorded port 0; want the actual bound port")
	}

	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on graceful shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	if after, _ := runfile.Read(runPath); after != nil {
		t.Error("run-file still present after shutdown; want it removed")
	}
}

func TestServerRunWithReadyPublishesBeforeCallback(t *testing.T) {
	runPath := filepath.Join(t.TempDir(), "running.json")
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- srv.RunWithReady(ctx, func() { close(ready) })
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("ready callback was not called")
	}
	if info, err := runfile.Read(runPath); err != nil || info == nil {
		t.Fatalf("run-file unavailable in ready callback: info=%v err=%v", info, err)
	}
	waitForHealth(t, "http://"+srv.Addr().String())

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("RunWithReady returned error on graceful shutdown: %v", err)
	}
}

func TestServerShutdownEndpoint(t *testing.T) {
	runPath := filepath.Join(t.TempDir(), "running.json")
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}

	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(context.Background()) }()

	base := "http://" + srv.Addr().String()
	waitForHealth(t, base)

	// Not http.Post: the shared DefaultTransport races the health poll's conn
	// return-to-pool and dials a spare that never carries a request; the server
	// then waits on that silent conn for the whole ShutdownTimeout (observed as
	// a flaky 5s failure under -race). DisableKeepAlives keeps this POST off
	// the shared pool entirely.
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Post(base+"/shutdown", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /shutdown = %d, want 202", resp.StatusCode)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on shutdown endpoint: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after shutdown endpoint")
	}

	if after, _ := runfile.Read(runPath); after != nil {
		t.Error("run-file still present after shutdown endpoint; want it removed")
	}
}

func waitForHealth(t *testing.T, base string) {
	t.Helper()
	// Per-request timeout so a stalled connect or hung handshake doesn't park
	// the test for the full Go test timeout; the outer deadline only bounds
	// the polling loop, not any single GET. Keep-alives are off for the reason
	// given in TestServerShutdownEndpoint: a pooled spare conn that never carries a
	// request holds graceful shutdown for the whole ShutdownTimeout.
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not become healthy within timeout")
}

// TestNewFallsBackOnPortConflict confirms that when the configured port is
// already held, the constructor binds an ephemeral port instead of failing, so
// the desktop supervisor never gets stuck on "daemon not ready".
func TestNewFallsBackOnPortConflict(t *testing.T) {
	cfg := config.Config{Host: "127.0.0.1", Port: 0, RunFilePath: filepath.Join(t.TempDir(), "r.json")}

	first, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	defer closeListeners(first)

	// Request the exact port the first server took; the second server should
	// fall back to a different, ephemeral port rather than error out.
	conflict := config.Config{Host: "127.0.0.1", Port: first.boundPort(), RunFilePath: cfg.RunFilePath}
	second, err := NewWithDeps(conflict, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New on an already-bound port = %v, want ephemeral fallback", err)
	}
	defer closeListeners(second)

	if second.boundPort() == first.boundPort() {
		t.Fatalf("second server bound the same port %d; want a fallback port", second.boundPort())
	}
	if second.boundPort() == 0 {
		t.Fatal("second server bound port 0; want a real fallback port")
	}
}

// closeListeners releases a constructed Server's sockets without running it.
func closeListeners(s *Server) {
	_ = s.listen.Close()
	if s.listen6 != nil {
		_ = s.listen6.Close()
	}
}

// requireIPv6Loopback skips a test on a host without an IPv6 loopback.
func requireIPv6Loopback(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback on this host: %v", err)
	}
	_ = ln.Close()
}

// probeIPv6Loopback reads a health probe and its ipv6Loopback flag.
func probeIPv6Loopback(t *testing.T, base string) (pid int, ipv6Loopback bool) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET %s/healthz: %v", base, err)
	}
	defer resp.Body.Close()
	var body struct {
		PID          int   `json:"pid"`
		IPv6Loopback *bool `json:"ipv6Loopback"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s/healthz: %v", base, err)
	}
	if body.IPv6Loopback == nil {
		t.Fatalf("%s/healthz has no ipv6Loopback", base)
	}
	return body.PID, *body.IPv6Loopback
}

// The desktop opens event streams on a *.localhost name, which Chromium
// resolves to ::1 first. The daemon serves the same router there, at the same
// port, so that name always reaches it; the probes say so.
func TestServerAlsoServesIPv6Loopback(t *testing.T) {
	requireIPv6Loopback(t)
	cfg := config.Config{Host: "127.0.0.1", Port: 0, ShutdownTimeout: 5 * time.Second, RunFilePath: filepath.Join(t.TempDir(), "running.json")}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	addr6, ok := srv.IPv6LoopbackAddr().(*net.TCPAddr)
	if !ok || !addr6.IP.Equal(net.IPv6loopback) || addr6.Port != srv.boundPort() {
		t.Fatalf("IPv6 loopback listener = %v; want [::1]:%d", srv.IPv6LoopbackAddr(), srv.boundPort())
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	base4 := "http://" + srv.Addr().String()
	base6 := fmt.Sprintf("http://[::1]:%d", srv.boundPort())
	waitForHealth(t, base4)
	waitForHealth(t, base6)
	pid4, flag4 := probeIPv6Loopback(t, base4)
	pid6, flag6 := probeIPv6Loopback(t, base6)
	if !flag4 || !flag6 || pid4 != os.Getpid() || pid6 != os.Getpid() {
		t.Fatalf("probes: 127.0.0.1 pid %d ipv6Loopback %v, ::1 pid %d ipv6Loopback %v; want this daemon with true on both", pid4, flag4, pid6, flag6)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
	// Shutdown released both addresses.
	if ln, err := net.Listen("tcp6", base6[len("http://"):]); err != nil {
		t.Fatalf("[::1]:%d still held after shutdown: %v", srv.boundPort(), err)
	} else {
		_ = ln.Close()
	}
}

// Another process on [::1] at the daemon's port would receive whatever the
// desktop sends to a *.localhost name. The daemon keeps serving 127.0.0.1 and
// reports the IPv6 listener down, so the desktop stays on 127.0.0.1.
func TestServerReportsIPv6LoopbackTaken(t *testing.T) {
	requireIPv6Loopback(t)
	for attempt := 0; ; attempt++ {
		free, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := free.Addr().(*net.TCPAddr).Port
		_ = free.Close()
		squatter, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
		if err != nil {
			if attempt < 5 {
				continue
			}
			t.Fatalf("hold [::1]:%d: %v", port, err)
		}
		defer squatter.Close()

		cfg := config.Config{Host: "127.0.0.1", Port: port, ShutdownTimeout: 5 * time.Second, RunFilePath: filepath.Join(t.TempDir(), "running.json")}
		srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if srv.boundPort() != port {
			// Something took the IPv4 port in between; try another.
			closeListeners(srv)
			_ = squatter.Close()
			if attempt < 5 {
				continue
			}
			t.Fatalf("bound %d, want %d", srv.boundPort(), port)
		}
		if srv.IPv6LoopbackAddr() != nil {
			closeListeners(srv)
			t.Fatalf("IPv6 loopback listener = %v while another socket holds [::1]:%d; want none", srv.IPv6LoopbackAddr(), port)
		}
		ctx, cancel := context.WithCancel(context.Background())
		runErr := make(chan error, 1)
		go func() { runErr <- srv.Run(ctx) }()
		base := "http://" + srv.Addr().String()
		waitForHealth(t, base)
		if _, flag := probeIPv6Loopback(t, base); flag {
			t.Fatal("ipv6Loopback = true while another socket holds [::1]; want false")
		}
		cancel()
		if err := <-runErr; err != nil {
			t.Fatalf("Run: %v", err)
		}
		return
	}
}

// Only the loopback daemon binds [::1]; a test or caller configuring another
// host gets no second listener.
func TestBindIPv6LoopbackOnlyForTheLoopbackHost(t *testing.T) {
	primary, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	if ln := bindIPv6Loopback(config.Config{Host: "localhost"}, primary, discardLogger()); ln != nil {
		_ = ln.Close()
		t.Fatal("bound [::1] for a non-loopback-IP host; want nothing")
	}
}
