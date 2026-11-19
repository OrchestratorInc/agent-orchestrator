package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
)

type fakeForkService struct {
	enabled bool
	opts    sessionfork.Options
	err     error
	forked  []string
}

func (f *fakeForkService) Enabled() bool { return f.enabled }
func (f *fakeForkService) Options(context.Context, domain.SessionID) (sessionfork.Options, error) {
	return f.opts, f.err
}
func (f *fakeForkService) ForkTurn(_ context.Context, id domain.SessionID, req, turn string) (domain.SessionRecord, error) {
	f.forked = append(f.forked, string(id)+"/"+req+turn)
	if f.err != nil {
		return domain.SessionRecord{}, f.err
	}
	return domain.SessionRecord{ID: "p-9", DisplayName: "Fork of X"}, nil
}

func forkRouter(svc SessionForkService) http.Handler {
	r := chi.NewRouter()
	(&SessionForksController{Svc: svc}).Register(r)
	return r
}

func TestForkRouteDisabledWithoutFlag(t *testing.T) {
	svc := &fakeForkService{err: sessionfork.ErrDisabled}
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/sessions/p-1/fork-options", nil),
		httptest.NewRequest(http.MethodPost, "/sessions/p-1/fork", strings.NewReader(`{"clientRequestId":"r1"}`)),
	} {
		rec := httptest.NewRecorder()
		forkRouter(svc).ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "SESSION_FORK_DISABLED") {
			t.Fatalf("%s %s = %d %s", req.Method, req.URL, rec.Code, rec.Body)
		}
	}
	if len(svc.forked) != 0 {
		t.Fatal("a disabled daemon must not fork")
	}
}

func TestForkRouteCreatesSession(t *testing.T) {
	svc := &fakeForkService{enabled: true}
	rec := httptest.NewRecorder()
	forkRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/p-1/fork", strings.NewReader(`{"clientRequestId":"r1"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	var out ForkSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.SessionID != "p-9" || out.DisplayName != "Fork of X" || len(svc.forked) != 1 || svc.forked[0] != "p-1/r1" {
		t.Fatalf("out = %+v forked = %v", out, svc.forked)
	}
	rec = httptest.NewRecorder()
	forkRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/p-1/fork", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing request id = %d", rec.Code)
	}
}

func TestForkRouteMapsRefusals(t *testing.T) {
	svc := &fakeForkService{enabled: true, err: &sessionfork.Refusal{Code: sessionfork.CodeSourceBusy, Message: "Wait for the agent to go idle to fork this session."}}
	rec := httptest.NewRecorder()
	forkRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/p-1/fork", strings.NewReader(`{"clientRequestId":"r1"}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "FORK_SOURCE_BUSY") {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	svc = &fakeForkService{enabled: true, opts: sessionfork.Options{Refusal: &sessionfork.Refusal{Code: sessionfork.CodeOrchestrator, Message: "Orchestrators can't be forked."}}}
	rec = httptest.NewRecorder()
	forkRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/p-1/fork-options", nil))
	var opts SessionForkOptionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || opts.Allowed || opts.Refusal == nil || opts.Refusal.Code != "FORK_ORCHESTRATOR" {
		t.Fatalf("code = %d opts = %+v", rec.Code, opts)
	}
	svc = &fakeForkService{enabled: true, err: sessionfork.ErrSessionNotFound}
	rec = httptest.NewRecorder()
	forkRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/nope/fork-options", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing session = %d", rec.Code)
	}
}

func TestForkRoutePassesTheTurn(t *testing.T) {
	svc := &fakeForkService{enabled: true}
	rec := httptest.NewRecorder()
	forkRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/p-1/fork", strings.NewReader(`{"clientRequestId":"r1","turnId":"@turn-2"}`)))
	if rec.Code != http.StatusCreated || len(svc.forked) != 1 || svc.forked[0] != "p-1/r1@turn-2" {
		t.Fatalf("code=%d forked=%v", rec.Code, svc.forked)
	}
}
