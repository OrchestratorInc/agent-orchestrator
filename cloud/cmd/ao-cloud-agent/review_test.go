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

// ao spawn --branch asks for a worker on that branch; without it the request
// carries no branch, so the control plane names one as before.
func TestRunSpawnSendsBranchOnlyWhenGiven(t *testing.T) {
	var bodies []map[string]any
	c := reviewTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"session":{"id":"s1","status":"provisioning"}}`))
	})
	if err := runSpawn(context.Background(), c, []string{"--name", "resume", "--prompt", "continue", "--branch", "feature/x"}); err != nil {
		t.Fatal(err)
	}
	if err := runSpawn(context.Background(), c, []string{"--name", "fresh", "--prompt", "start"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0]["branch"] != "feature/x" {
		t.Fatalf("bodies = %v, want the first to carry branch feature/x", bodies)
	}
	if _, ok := bodies[1]["branch"]; ok {
		t.Fatalf("spawn without --branch sent %v", bodies[1])
	}
}
