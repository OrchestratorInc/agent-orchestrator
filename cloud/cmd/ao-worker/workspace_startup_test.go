package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestPrepareWorkspaceReportsProgressAndOutcome(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "failed"}[fail], func(t *testing.T) {
			var states []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/worker/events" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var event struct {
					Type    string `json:"type"`
					Payload struct {
						WorkerID string `json:"workerId"`
						Epoch    int64  `json:"epoch"`
						State    string `json:"workspaceState"`
					} `json:"payload"`
				}
				if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
					t.Error(err)
				}
				if event.Type != "worker.ready" || event.Payload.WorkerID != "startup-worker" || event.Payload.Epoch != 7 {
					t.Errorf("invalid readiness envelope: %+v", event)
				}
				states = append(states, event.Payload.State)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			if fail {
				if err := os.WriteFile(workspace, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c := &client{baseURL: server.URL, http: server.Client()}
			bootstrap := worker.BootstrapResponse{SessionID: "startup-session", WorkerID: "startup-worker", Epoch: 7,
				Launch: worker.LaunchContext{RepositoryURL: "https://scratch.ao.local/workspace"}}
			err := prepareWorkspace(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), c, bootstrap, workspace, filepath.Join(root, "data"), server.URL)
			if (err != nil) != fail {
				t.Fatalf("prepareWorkspace err=%v, fail=%v", err, fail)
			}
			want := []string{"preparing", "ready"}
			if fail {
				want[1] = "failed"
			}
			if !reflect.DeepEqual(states, want) {
				t.Fatalf("startup states=%v, want %v", states, want)
			}
		})
	}
}

func TestPrepareWorkspaceRetriesOutcomeDelivery(t *testing.T) {
	var readyAttempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/worker/events" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var event struct {
			Payload struct {
				State string `json:"workspaceState"`
			} `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Error(err)
		}
		if event.Payload.State == "ready" {
			readyAttempts++
			if readyAttempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	root := t.TempDir()
	c := &client{baseURL: server.URL, http: server.Client()}
	bootstrap := worker.BootstrapResponse{SessionID: "startup-session", WorkerID: "startup-worker", Epoch: 7,
		Launch: worker.LaunchContext{RepositoryURL: "https://scratch.ao.local/workspace"}}
	if err := prepareWorkspace(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), c, bootstrap,
		filepath.Join(root, "workspace"), filepath.Join(root, "data"), server.URL); err != nil {
		t.Fatal(err)
	}
	if readyAttempts != 2 {
		t.Fatalf("ready delivery attempts=%d, want transient failure retried", readyAttempts)
	}
}
