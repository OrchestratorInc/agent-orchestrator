package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/procinventory"
)

type fakeProcessService struct {
	inv      procinventory.Inventory
	invErr   error
	report   procinventory.KillReport
	killErr  error
	disabled bool
	gotKill  []procinventory.KillTarget
}

func (f *fakeProcessService) Inventory(context.Context) (procinventory.Inventory, error) {
	return f.inv, f.invErr
}

func (f *fakeProcessService) Kill(_ context.Context, targets []procinventory.KillTarget) (procinventory.KillReport, error) {
	f.gotKill = targets
	return f.report, f.killErr
}

func (f *fakeProcessService) Disabled() bool { return f.disabled }

func processServer(t *testing.T, svc controllers.ProcessService) *httptest.Server {
	t.Helper()
	c := &controllers.ProcessController{Processes: svc}
	r := chi.NewRouter()
	r.Route("/api/v1", func(sub chi.Router) {
		c.Register(sub)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestProcessRoutes_NilServiceIs501(t *testing.T) {
	srv := processServer(t, nil)

	resp, err := http.Get(srv.URL + "/api/v1/system/processes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("GET status = %d, want 501", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/api/v1/system/processes/kill", "application/json", bytes.NewReader([]byte(`{"targets":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("POST status = %d, want 501", resp.StatusCode)
	}
}

// TestProcessRoutes_DisabledServiceReportsGateAndRefusesKill covers the
// AO_NO_PROCINVENTORY runtime gate: the list route still answers 200 with
// disabled=true (one round trip teaches the status bar to hide and stop
// polling), while the kill route degrades to 501 like a nil service.
func TestProcessRoutes_DisabledServiceReportsGateAndRefusesKill(t *testing.T) {
	svc := &fakeProcessService{disabled: true, inv: procinventory.Inventory{
		Trees: []procinventory.Tree{{SessionID: "should-not-appear"}},
	}}
	srv := processServer(t, svc)

	resp, err := http.Get(srv.URL + "/api/v1/system/processes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}
	var got controllers.ProcessInventoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Disabled {
		t.Fatal("list response disabled = false, want true")
	}
	if len(got.Trees) != 0 {
		t.Fatalf("list response trees = %d, want 0 while disabled", len(got.Trees))
	}

	resp, err = http.Post(srv.URL+"/api/v1/system/processes/kill", "application/json", bytes.NewReader([]byte(`{"targets":[{"sessionId":"s1","rootPid":1,"rootLstart":"x"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("POST status = %d, want 501", resp.StatusCode)
	}
	if svc.gotKill != nil {
		t.Fatal("kill reached the service while disabled")
	}
}

func TestProcessList_ReturnsInventory(t *testing.T) {
	svc := &fakeProcessService{inv: procinventory.Inventory{
		GeneratedAt: time.Unix(1758000000, 0),
		Daemon:      procinventory.GroupSummary{PID: 4001, Present: true, RSSBytes: 92160 * 1024},
		Tmux:        procinventory.GroupSummary{PID: 4055, Present: true, RSSBytes: 45000 * 1024},
		Trees: []procinventory.Tree{
			{SessionID: "web-api-3", RootPID: 300, RootLstart: "Mon Sep 14 00:13:02 2026", PGID: 300, PIDCount: 3,
				RSSBytes: 153600 * 1024, Kind: "worker", State: procinventory.StateOrphan},
		},
		Totals: procinventory.Totals{OrphansCount: 1, OrphansRSSBytes: 153600 * 1024, DaemonRSSBytes: 92160 * 1024},
	}}
	srv := processServer(t, svc)

	resp, err := http.Get(srv.URL + "/api/v1/system/processes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body controllers.ProcessInventoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Totals.OrphansCount != 1 || len(body.Trees) != 1 || body.Trees[0].State != "orphan" {
		t.Fatalf("body = %+v", body)
	}
	if !body.Daemon.Present || body.Tmux.PID != 4055 {
		t.Fatalf("groups = %+v / %+v", body.Daemon, body.Tmux)
	}
}

func TestProcessList_ScanFailureIs500(t *testing.T) {
	svc := &fakeProcessService{invErr: errors.New("ps failed")}
	srv := processServer(t, svc)

	resp, err := http.Get(srv.URL + "/api/v1/system/processes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestProcessKill_MapsTargetsAndResults(t *testing.T) {
	svc := &fakeProcessService{report: procinventory.KillReport{Results: []procinventory.KillResult{
		{SessionID: "orphan-1", RootPID: 300, Status: procinventory.ResultKilled},
		{SessionID: "restored-1", RootPID: 310, Status: procinventory.ResultSkipped, Detail: "session is not an orphan (state owned)"},
	}}}
	srv := processServer(t, svc)

	body := `{"targets":[{"sessionId":"orphan-1","rootPid":300,"rootLstart":"Mon Sep 14 00:13:02 2026"}]}`
	resp, err := http.Post(srv.URL+"/api/v1/system/processes/kill", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(svc.gotKill) != 1 || svc.gotKill[0].SessionID != "orphan-1" || svc.gotKill[0].RootPID != 300 {
		t.Fatalf("targets = %+v", svc.gotKill)
	}
	var out controllers.ProcessKillResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 || out.Results[0].Status != "killed" || out.Results[1].Status != "skipped" {
		t.Fatalf("results = %+v", out.Results)
	}
}

func TestProcessKill_ConcurrentIs409(t *testing.T) {
	svc := &fakeProcessService{killErr: procinventory.ErrKillInProgress}
	srv := processServer(t, svc)

	resp, err := http.Post(srv.URL+"/api/v1/system/processes/kill", "application/json", bytes.NewReader([]byte(`{"targets":[]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestProcessKill_BadBodyIs400(t *testing.T) {
	srv := processServer(t, &fakeProcessService{})

	resp, err := http.Post(srv.URL+"/api/v1/system/processes/kill", "application/json", bytes.NewReader([]byte(`{oops`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
