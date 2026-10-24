package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
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

// A client error (4xx) is the control plane's answer, for example a spent
// ticket: retrying it cannot succeed.
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

// A control plane too slow or busy to answer (a 5xx, or a timeout after it
// already redeemed the ticket) is retried with the same ticket: the control
// plane redeems it again until a worker checks in on its epoch.
func TestBootstrapRetriesAServerError(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request worker.BootstrapRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.BootstrapToken != "ticket" {
			t.Errorf("request = %+v, %v; want the same ticket", request, err)
		}
		if calls.Add(1) <= 2 {
			http.Error(w, "context canceled", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workerToken":"worker-token"}`)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client()}

	response, err := c.bootstrapWithRetry(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), "ticket")
	if err != nil || response.WorkerToken != "worker-token" || calls.Load() != 3 {
		t.Fatalf("token %q, %v after %d calls; want worker-token after 3", response.WorkerToken, err, calls.Load())
	}
}

// Retries stop once the budget is spent and return the last error.
func TestBootstrapStopsRetryingAfterItsBudget(t *testing.T) {
	budget, maxDelay := bootstrapRetryBudget, bootstrapRetryMaxDelay
	bootstrapRetryBudget, bootstrapRetryMaxDelay = 2*time.Second, 500*time.Millisecond
	t.Cleanup(func() { bootstrapRetryBudget, bootstrapRetryMaxDelay = budget, maxDelay })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client()}

	started := time.Now()
	_, err := c.bootstrapWithRetry(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), "ticket")
	if err == nil || !strings.Contains(err.Error(), "returned 503") {
		t.Fatalf("err = %v, want the last 503", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second || calls.Load() < 3 {
		t.Fatalf("%d calls in %s; want several within the 2s budget", calls.Load(), elapsed)
	}
}

// A control plane slower than the usual request timeout still answers the
// bootstrap: it has its own, longer timeout, so a slow but working control
// plane is waited out instead of retried into the ground.
func TestBootstrapWaitsLongerThanOtherRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workerToken":"worker-token"}`)
	}))
	defer server.Close()
	c := &client{baseURL: server.URL,
		http:          &http.Client{Transport: server.Client().Transport, Timeout: 50 * time.Millisecond},
		bootstrapHTTP: &http.Client{Transport: server.Client().Transport, Timeout: 5 * time.Second}}
	response, err := c.bootstrap(context.Background(), "ticket")
	if err != nil || response.WorkerToken != "worker-token" {
		t.Fatalf("bootstrap = %+v, %v; want it to wait out the slow control plane", response, err)
	}
	// Other requests keep the short timeout.
	if err := c.do(context.Background(), "/worker/heartbeat", map[string]string{}, nil); err == nil {
		t.Fatal("a non-bootstrap request used the bootstrap timeout")
	}
}
