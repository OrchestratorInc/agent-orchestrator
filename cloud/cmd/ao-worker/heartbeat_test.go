package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// heartbeatServer stands in for the control plane's heartbeat route. strict
// mimics a release from before the busy field: it rejects unknown fields the
// way decodeJSON does. advertise adds the busy feature to its responses.
type heartbeatServer struct {
	mu        sync.Mutex
	strict    bool
	advertise bool
	bodies    []map[string]any
}

func (s *heartbeatServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	s.bodies = append(s.bodies, body)
	if s.strict {
		var legacy struct {
			Version      string   `json:"version"`
			Capabilities []string `json:"capabilities"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&legacy); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "invalid_request", "message": err.Error()})
			return
		}
	}
	response := worker.HeartbeatResponse{OK: true, WorkerToken: "renewed", ExpiresIn: 60}
	if s.advertise {
		response.Features = []string{worker.HeartbeatFeatureBusy}
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (s *heartbeatServer) sent() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func heartbeatClient(t *testing.T, server *heartbeatServer) *client {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	c := &client{baseURL: httpServer.URL, http: httpServer.Client()}
	busy := func() bool { return true }
	c.busy.Store(&busy)
	return c
}

// busy is sent only once the control plane has advertised it.
func TestHeartbeatSendsBusyOnlyAfterTheControlPlaneAdvertisesIt(t *testing.T) {
	server := &heartbeatServer{advertise: true}
	c := heartbeatClient(t, server)
	for range 2 {
		if token, err := c.heartbeat(context.Background()); err != nil || token != "renewed" {
			t.Fatalf("heartbeat = %q, %v", token, err)
		}
	}
	sent := server.sent()
	if _, ok := sent[0]["busy"]; ok {
		t.Fatalf("first heartbeat sent busy before the control plane advertised it: %v", sent[0])
	}
	if sent[1]["busy"] != true {
		t.Fatalf("second heartbeat = %v, want busy once advertised", sent[1])
	}
}

// A control plane rolled back to before the busy field rejects it; the worker
// drops the field, retries once, and stops sending it.
func TestHeartbeatSurvivesAControlPlaneRollback(t *testing.T) {
	server := &heartbeatServer{strict: true}
	c := heartbeatClient(t, server)
	c.busyAccepted.Store(true) // learned from the newer control plane
	if token, err := c.heartbeat(context.Background()); err != nil || token != "renewed" {
		t.Fatalf("heartbeat after rollback = %q, %v", token, err)
	}
	if _, err := c.heartbeat(context.Background()); err != nil {
		t.Fatalf("next heartbeat: %v", err)
	}
	sent := server.sent()
	if len(sent) != 3 || sent[0]["busy"] != true {
		t.Fatalf("requests = %v, want busy, a retry without it, then one more without it", sent)
	}
	for _, body := range sent[1:] {
		if _, ok := body["busy"]; ok {
			t.Fatalf("busy sent again after the rollback: %v", sent)
		}
	}
}
