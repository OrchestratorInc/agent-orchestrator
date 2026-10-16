package agentbrowser

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary stands in for the worker: run as HostCommand, it is a fake
// browser service that answers stats with RB_TOKEN and exits on /exit.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == HostCommand {
		flags := flag.NewFlagSet(HostCommand, flag.ExitOnError)
		addr := flags.String("addr", "", "")
		flags.String("data", "", "")
		flags.String("chrome", "", "")
		flags.String("ab", "", "")
		flags.Int("cdp-port", 0, "")
		_ = flags.Parse(os.Args[2:])
		http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("token") != os.Getenv("RB_TOKEN") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}
		})
		http.HandleFunc("/exit", func(http.ResponseWriter, *http.Request) { os.Exit(0) })
		_ = http.ListenAndServe(*addr, nil)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestServiceStartsTheHostOnDemandRestartsItAndStopsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Service{token: strings.Repeat("ab", 32), logger: slog.New(slog.DiscardHandler)}
	s.start = func(ctx context.Context) (*instance, error) {
		return s.startHost(ctx, t.TempDir(), "chromium", "agent-browser")
	}
	if s.live() != nil {
		t.Fatal("a service is running before any request")
	}
	target, err := s.ensure(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	first := s.live()
	if again, _ := s.ensure(ctx); again != target {
		t.Fatalf("second request started another service: %s, then %s", target, again)
	}

	// The service exits: the next request starts a new one.
	_, _ = http.Get(target.JoinPath("exit").String())
	<-first.done
	if _, err := s.ensure(ctx); err != nil {
		t.Fatalf("restart: %v", err)
	}
	second := s.live()
	if second == nil || second == first {
		t.Fatal("the exited service was not replaced")
	}

	cancel()
	select {
	case <-second.done:
	case <-time.After(stopGrace + 2*time.Second):
		t.Fatal("the service outlived its context")
	}
}

// fakeBrowser is a browser service that records the last request it got.
func fakeBrowser(t *testing.T) (*httptest.Server, *atomic.Pointer[http.Request]) {
	var last atomic.Pointer[http.Request]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last.Store(r)
		if strings.HasPrefix(r.URL.Path, "/download/") {
			http.Redirect(w, r, "/elsewhere?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "browser "+r.URL.Path)
	}))
	t.Cleanup(server.Close)
	return server, &last
}

// fakeStart starts no process: the "service" is upstream, until ctx ends.
func fakeStart(upstream *httptest.Server, starts *atomic.Int32) func(context.Context) (*instance, error) {
	return func(ctx context.Context) (*instance, error) {
		starts.Add(1)
		target, _ := url.Parse(upstream.URL)
		done := make(chan struct{})
		context.AfterFunc(ctx, func() { close(done) })
		return &instance{url: target, done: done}, nil
	}
}

func TestRelayHandlerAllowsStreamRoutesWithTheTokenAndNoCredentials(t *testing.T) {
	upstream, last := fakeBrowser(t)
	var starts atomic.Int32
	s := &Service{token: "service-token", logger: slog.New(slog.DiscardHandler), start: fakeStart(upstream, &starts)}
	handler := s.relayHandler(context.Background())
	serve := func(target string, header http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header = header
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	if got := serve("/stats", nil); got.Code != http.StatusOK || got.Body.String() != `{"running":false}` || starts.Load() != 0 {
		t.Fatalf("stats before start = %d %q after %d starts; want {\"running\":false} without a start", got.Code, got.Body, starts.Load())
	}
	for _, target := range []string{"/agent", "/json/version", "/download/..", "/download/a/b", "/stats/"} {
		if got := serve(target, nil); got.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", target, got.Code)
		}
	}
	if starts.Load() != 0 {
		t.Fatalf("refused routes started the browser %d times", starts.Load())
	}

	got := serve("/ws/cdp?tab=1&token=forged", http.Header{
		"Authorization": {"Bearer ao"}, "Proxy-Authorization": {"x"}, "Cookie": {"ao=1"},
		"Origin": {"https://desktop"}, "X-Ao-Browser-Capability": {"cap"}, "Accept": {"text/html"},
	})
	if got.Code != http.StatusOK || starts.Load() != 1 {
		t.Fatalf("ws/cdp = %d after %d starts, want 200 after one", got.Code, starts.Load())
	}
	in := last.Load()
	if in.URL.Path != "/ws/cdp" || in.URL.Query().Get("token") != "service-token" || in.URL.Query().Get("tab") != "1" {
		t.Errorf("upstream got %s, want /ws/cdp with the service token and the tab", in.URL)
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Origin", "X-Ao-Browser-Capability"} {
		if in.Header.Get(name) != "" {
			t.Errorf("upstream got %s", name)
		}
	}
	if in.Header.Get("Accept") != "text/html" {
		t.Error("upstream lost the Accept header")
	}

	if got := serve("/stats", nil); got.Body.String() != "browser /stats" {
		t.Errorf("stats while running = %q, want the browser's", got.Body)
	}
	redirect := serve("/download/guid-1", nil)
	if location := redirect.Header().Get("Location"); strings.Contains(location, "token") || redirect.Body.Len() != 0 {
		t.Errorf("redirect leaks the token: Location %q, body %q", location, redirect.Body)
	}
	if starts.Load() != 1 {
		t.Errorf("browser started %d times, want once", starts.Load())
	}
}

// The agent's socket is at a fixed path, so an agent that outlives the worker
// finds the next worker's; only the agent's command starts the browser, and
// the service's token is added on the way.
func TestAgentSocketIsStableAndOnlyTheCommandStartsTheBrowser(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"chromium", "agent-browser"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	sockets, err := os.MkdirTemp("/tmp", "ab") // a socket path must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	socket := filepath.Join(sockets, "ao-browser.sock")
	logger := slog.New(slog.DiscardHandler)

	first, err := New(t.TempDir(), socket, logger)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Env()["AO_BROWSER_SOCKET"]; got != socket {
		t.Fatalf("AO_BROWSER_SOCKET = %q, want %q", got, socket)
	}
	if st, err := os.Stat(socket); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
	// A worker that died left its socket file: the next one takes the path.
	first.agent.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = first.agent.Close()
	second, err := New(t.TempDir(), socket, logger)
	if err != nil {
		t.Fatalf("a restarted worker could not take the socket: %v", err)
	}
	defer second.agent.Close()

	upstream, last := fakeBrowser(t)
	var starts atomic.Int32
	second.start = fakeStart(upstream, &starts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = (&http.Server{Handler: second.agentHandler(ctx)}).Serve(second.agent) }()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	for _, probe := range []struct{ method, path string }{{"GET", "/"}, {"GET", "/stats"}, {"GET", "/agent"}, {"POST", "/v/cdp"}} {
		request, _ := http.NewRequest(probe.method, "http://browser"+probe.path, nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", probe.method, probe.path, response.StatusCode)
		}
	}
	if starts.Load() != 0 {
		t.Fatalf("stray requests started the browser %d times", starts.Load())
	}
	response, err := client.Post("http://browser/agent", "application/json", strings.NewReader(`{"args":["snapshot"]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || starts.Load() != 1 {
		t.Fatalf("agent command = %d after %d starts, want 200 after one", response.StatusCode, starts.Load())
	}
	if got := last.Load().Header.Get("Authorization"); got != "Bearer "+second.token {
		t.Errorf("service got Authorization %q, want the service token", got)
	}
}
