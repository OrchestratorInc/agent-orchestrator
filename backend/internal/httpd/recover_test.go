package httpd

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
)

// A client that leaves mid-body breaks ReverseProxy's copy, which then aborts
// the response with http.ErrAbortHandler. Nothing in the daemon failed: the
// abort reaches the server, with no panic log, event or report.
func TestRecoverTelemetryLetsAnAbortedResponseThrough(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first part")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(release) })
	target, _ := url.Parse(upstream.URL)
	sink := &captureSink{}
	var logs bytes.Buffer
	proxy := recoverTelemetry(slog.New(slog.NewTextHandler(&logs, nil)), sink)(httputil.NewSingleHostReverseProxy(target))
	handled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handled)
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := response.Body.Read(make([]byte, 64)); err != nil {
		t.Fatalf("first part: %v", err)
	}
	cancel()
	_ = response.Body.Close()
	<-handled
	if len(sink.events) != 0 || logs.Len() != 0 {
		t.Fatalf("a client leaving mid-body was reported as a panic: events %v, log %q", sink.events, logs.String())
	}
}
