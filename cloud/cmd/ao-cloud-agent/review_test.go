package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func reviewTestClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tokenFile := filepath.Join(t.TempDir(), "worker-token")
	if err := os.WriteFile(tokenFile, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &client{baseURL: server.URL, tokenFile: tokenFile, http: server.Client()}
}

func TestRunReviewTriggerAsksTheControlPlane(t *testing.T) {
	var method, path, auth string
	c := reviewTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(worker.TriggerReviewResponse{Reviews: []worker.TriggeredReview{{Number: 4, URL: "https://github.test/o/r/pull/4", Started: true}}})
	})
	if err := runReview(context.Background(), c, []string{"trigger"}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/worker/reviews/trigger" || auth != "Worker token" {
		t.Fatalf("request = %s %s auth=%q", method, path, auth)
	}
}

func TestRunReviewRejectsReviewerProcessesAndOtherSubcommands(t *testing.T) {
	called := false
	c := reviewTestClient(t, func(http.ResponseWriter, *http.Request) { called = true })
	if err := runReview(context.Background(), c, []string{"ls"}); err == nil {
		t.Fatal("accepted an unsupported review subcommand")
	}
	t.Setenv(worker.ReviewTerminalEnv, "1")
	if err := runReview(context.Background(), c, []string{"trigger"}); err == nil {
		t.Fatal("a reviewer process started a review")
	}
	if called {
		t.Fatal("contacted the control plane for a rejected command")
	}
}
