package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

func TestWorkWaitersNotifyWakesOnlyTheSession(t *testing.T) {
	ww := newWorkWaiters()
	wake, cancel := ww.subscribe("s1")
	defer cancel()

	// A notify for a different session must not wake this subscriber.
	ww.notify("s2")
	select {
	case <-wake:
		t.Fatal("woke on an unrelated session's notify")
	default:
	}

	ww.notify("s1")
	select {
	case <-wake:
	default:
		t.Fatal("did not wake on this session's notify")
	}

	// After deregister, a notify must not panic or deliver.
	cancel()
	ww.notify("s1")
}

func waitWorkServer() *Server {
	return &Server{
		workWaiters: newWorkWaiters(),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestWorkerWaitForWorkReturnsPromptlyOnNotify(t *testing.T) {
	srv := waitWorkServer()
	w := httptest.NewRecorder()
	req := workerRequest(t, http.MethodGet, "/worker/work/wait", "", "worker:connect")

	done := make(chan struct{})
	start := time.Now()
	go func() { srv.workerWaitForWork(w, req); close(done) }()

	// Wait until the handler has subscribed, then notify — robust against the
	// goroutine not yet having run.
	for i := 0; i < 200; i++ {
		srv.workWaiters.mu.Lock()
		n := len(srv.workWaiters.waiters[testOrchestratorID])
		srv.workWaiters.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	srv.workWaiters.notify(testOrchestratorID)

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed >= workWaitTimeout {
			t.Fatalf("returned after %v; the notify should have woken it well before the %v fallback", elapsed, workWaitTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("workerWaitForWork did not return after a notify")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestWorkerWaitForWorkRequiresConnectScope(t *testing.T) {
	srv := waitWorkServer()
	w := httptest.NewRecorder()
	srv.workerWaitForWork(w, workerRequest(t, http.MethodGet, "/worker/work/wait", ""))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestWorkerWaitForWorkReportsAnArtifactChangeOnce(t *testing.T) {
	srv := waitWorkServer()
	wait := func() worker.WorkWaitResponse {
		t.Helper()
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			srv.workerWaitForWork(w, workerRequest(t, http.MethodGet, "/worker/work/wait", "", "worker:connect"))
			close(done)
		}()
		for i := 0; i < 200; i++ {
			srv.workWaiters.mu.Lock()
			n := len(srv.workWaiters.waiters[testOrchestratorID])
			srv.workWaiters.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		srv.HandleWorkerWorkNotify(postgres.ArtifactsChangedNotifyPrefix + testOrchestratorID)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("an artifact change did not wake the worker")
		}
		var response worker.WorkWaitResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	if !wait().ArtifactsChanged {
		t.Fatal("the wake did not report the artifact change")
	}

	// A plain work notify does not report one.
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.workerWaitForWork(w, workerRequest(t, http.MethodGet, "/worker/work/wait", "", "worker:connect"))
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	srv.HandleWorkerWorkNotify(testOrchestratorID)
	<-done
	if strings.Contains(w.Body.String(), "artifactsChanged") {
		t.Fatalf("a plain wake reported an artifact change: %s", w.Body.String())
	}
}

func TestArtifactChangeMarksExpire(t *testing.T) {
	ww := newWorkWaiters()
	start := time.Now()
	ww.markArtifactsChanged("s1", start)
	if ww.takeArtifactsChanged("s1", start.Add(artifactsChangedTTL+time.Second)) {
		t.Fatal("a stale mark was reported")
	}
	ww.markArtifactsChanged("s1", start)
	ww.markArtifactsChanged("s2", start.Add(artifactsChangedTTL+time.Second))
	if _, kept := ww.artifactsChanged["s1"]; kept {
		t.Fatal("a stale mark was kept")
	}
}
