package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A connection dropped before the control plane sees the first request is
// retried, so one network blip does not strand a new session.
func TestBootstrapRetriesADroppedConnection(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workerToken":"worker-token"}`)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client()}

	response, err := c.bootstrapWithRetry(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), "ticket")
	if err != nil {
		t.Fatalf("bootstrap failed after a dropped connection: %v", err)
	}
	if response.WorkerToken != "worker-token" || calls.Load() != 2 {
		t.Fatalf("token %q after %d calls; want worker-token after 2", response.WorkerToken, calls.Load())
	}
}

// An HTTP error is the control plane's answer: the single-use ticket must not
// be replayed.
func TestBootstrapDoesNotRetryAnHTTPError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "ticket already used", http.StatusConflict)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client()}

	if _, err := c.bootstrapWithRetry(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), "ticket"); err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("HTTP error retried: %d calls", calls.Load())
	}
}
