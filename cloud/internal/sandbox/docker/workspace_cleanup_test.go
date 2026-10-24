package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

const orphanSession = "903a1c93-9300-4f53-92ad-7ee18a0fbcad"

// Independently calculated SHA-256 prefix of test-auth + NUL + orphanSession.
const orphanWorkspace = "ao-workspace-ce7b96f01aa4d6d71c6962bcce315466"

type orphanEngine struct {
	mu                sync.Mutex
	volume, container bool
	failCreate        bool
	failVolumeDeletes int
	labels            map[string]string
	volumeName        string
	volumeDeletes     int
}

func (e *orphanEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1.44")
	switch {
	case r.Method == http.MethodPost && path == "/volumes/create":
		var input createVolumeRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Name != orphanWorkspace {
			w.WriteHeader(400)
			return
		}
		e.volume = true
		e.labels = input.Labels
		e.volumeName = input.Name
		_ = json.NewEncoder(w).Encode(volumeView{Name: e.volumeName, Labels: e.labels})
	case r.Method == http.MethodGet && path == "/volumes/"+orphanWorkspace:
		if !e.volume {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(volumeView{Name: e.volumeName, Labels: e.labels})
	case r.Method == http.MethodDelete && path == "/volumes/"+orphanWorkspace:
		e.volumeDeletes++
		if r.URL.RawQuery != "" || e.failVolumeDeletes > 0 {
			if e.failVolumeDeletes > 0 {
				e.failVolumeDeletes--
			}
			w.WriteHeader(500)
			return
		}
		e.volume = false
		w.WriteHeader(204)
	case r.Method == http.MethodPost && path == "/containers/create":
		if e.failCreate {
			w.WriteHeader(500)
			return
		}
		e.container = true
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "auth-container"})
	case r.Method == http.MethodPost && path == "/containers/auth-container/start":
		w.WriteHeader(204)
	case r.Method == http.MethodGet && path == "/containers/auth-container/json":
		if !e.container {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(containerView{ID: "auth-container", Config: inspectConfig{Labels: e.labels}, State: containerState{Running: true}})
	case r.Method == http.MethodDelete && path == "/containers/auth-container":
		e.container = false
		w.WriteHeader(204)
	default:
		w.WriteHeader(400)
	}
}

func orphanClient(t *testing.T, engine *orphanEngine) *Client {
	t.Helper()
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	client, err := New(Config{Host: server.URL, WorkerImage: "auth:test", Namespace: "test-auth", HTTPClient: server.Client(), APIVersion: "1.44"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func orphanSpec() sandbox.Spec {
	return sandbox.Spec{SessionID: orphanSession, OrgID: "org-fixture", Labels: map[string]string{"ao.purpose": "agent-login"}}
}

func TestDeleteWorkspaceBySessionRecoversFailedCreateVolume(t *testing.T) {
	engine := &orphanEngine{failCreate: true}
	client := orphanClient(t, engine)
	if _, err := client.Create(context.Background(), orphanSpec()); err == nil {
		t.Fatal("container failure was not reproduced")
	}
	engine.mu.Lock()
	volume, container := engine.volume, engine.container
	engine.mu.Unlock()
	if !volume || container {
		t.Fatal("fixture did not leave a volume-only partial create")
	}
	found, err := client.DeleteWorkspaceBySession(context.Background(), orphanSession)
	if err != nil || !found {
		t.Fatalf("volume-only cleanup found=%v err=%v", found, err)
	}
	engine.mu.Lock()
	remaining := engine.volume
	engine.mu.Unlock()
	if remaining {
		t.Fatal("volume-only partial create retained its workspace")
	}
	if found, err := client.DeleteWorkspaceBySession(context.Background(), orphanSession); err != nil || found {
		t.Fatal("absent workspace cleanup was not idempotent")
	}
}

func TestDeleteWorkspaceBySessionRetriesAfterContainerRemoval(t *testing.T) {
	engine := &orphanEngine{failVolumeDeletes: 1}
	client := orphanClient(t, engine)
	env, err := client.Create(context.Background(), orphanSpec())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(sandbox.WithoutCleanup(context.Background()), env.ID); err == nil {
		t.Fatal("volume delete failure was not reproduced")
	}
	engine.mu.Lock()
	volume, container := engine.volume, engine.container
	engine.mu.Unlock()
	if !volume || container {
		t.Fatal("fixture did not remove container before volume delete failed")
	}
	if found, err := client.DeleteWorkspaceBySession(context.Background(), orphanSession); err != nil || !found {
		t.Fatalf("orphan retry found=%v err=%v", found, err)
	}
	engine.mu.Lock()
	remaining := engine.volume
	engine.mu.Unlock()
	if remaining {
		t.Fatal("failed volume deletion was not recovered on retry")
	}
}

func TestDeleteWorkspaceBySessionRejectsForeignOwnership(t *testing.T) {
	for _, label := range []string{labelNamespace, labelSessionID, labelManaged, labelProvider, labelWorkspace, "name"} {
		t.Run(label, func(t *testing.T) {
			engine := &orphanEngine{failCreate: true}
			client := orphanClient(t, engine)
			_, _ = client.Create(context.Background(), orphanSpec())
			engine.mu.Lock()
			if label == "name" {
				engine.volumeName = "some-user-workspace"
			} else {
				engine.labels[label] = "other-user-namespace"
			}
			engine.mu.Unlock()
			found, err := client.DeleteWorkspaceBySession(context.Background(), orphanSession)
			if err == nil || found {
				t.Fatal("foreign workspace ownership was accepted")
			}
			engine.mu.Lock()
			remaining, deletes := engine.volume, engine.volumeDeletes
			engine.mu.Unlock()
			if !remaining || deletes != 0 {
				t.Fatal("foreign user workspace was deleted")
			}
		})
	}
}
