package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/go-chi/chi/v5"
)

type catalogReviewStore struct {
	Store
	mu                                   sync.Mutex
	creates, polls, cancels, eligibility int
	eligible                             bool
	ready                                bool
	timeout                              time.Duration
	response                             json.RawMessage
	source                               string
	createErr                            error
}

func (s *catalogReviewStore) UserModelCatalogSession(_ context.Context, _ domain.Principal, orgID, _ string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eligibility++
	if !s.eligible {
		return "", "", postgres.ErrNotFound
	}
	if s.source != "" {
		return orgID, s.source, nil
	}
	return orgID, workspaceTestSessionID, nil
}

func (s *catalogReviewStore) CreateWorkspaceRequest(_ context.Context, _ domain.Principal, orgID, sessionID, _ string, _ json.RawMessage, timeout time.Duration) (domain.WorkerRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creates++
	s.timeout = timeout
	if s.createErr != nil {
		return domain.WorkerRequest{}, s.createErr
	}
	return domain.WorkerRequest{ID: "00000000-0000-4000-8000-000000000003", OrgID: orgID, SessionID: sessionID}, nil
}

func (s *catalogReviewStore) GetWorkspaceRequest(_ context.Context, _ domain.Principal, _, _, _ string) (domain.WorkerRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polls++
	if !s.ready {
		return domain.WorkerRequest{Status: "pending"}, nil
	}
	if s.response != nil {
		return domain.WorkerRequest{Status: "succeeded", Response: s.response}, nil
	}
	return domain.WorkerRequest{Status: "succeeded", Response: json.RawMessage(`{"models":[{"id":"advisory","displayName":"Advisory"}]}`)}, nil
}

func (s *catalogReviewStore) CancelWorkspaceRequest(_ context.Context, _ domain.Principal, _, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels++
	return nil
}

func catalogReviewRequest(t *testing.T) *http.Request {
	r := workspaceReviewRequest(t, http.MethodGet, "/providers/codex/models", nil)
	chi.RouteContext(r.Context()).URLParams.Add("agent", "codex")
	return r
}

func TestCatalogReviewCoalescesBoundsPollingAndRechecksEligibility(t *testing.T) {
	store := &catalogReviewStore{eligible: true}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			recorder := httptest.NewRecorder()
			server.listUserAgentModels(recorder, catalogReviewRequest(t))
			if recorder.Code != http.StatusOK {
				t.Errorf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	time.Sleep(1100 * time.Millisecond)
	store.mu.Lock()
	creates, polls := store.creates, store.polls
	store.ready = true
	store.mu.Unlock()
	wg.Wait()
	if creates != 1 || polls > 2 {
		t.Fatalf("creates=%d polls=%d; want one discovery and at most two polls in 1.1s", creates, polls)
	}
	if store.timeout != 30*time.Second {
		t.Fatalf("discovery timeout=%s", store.timeout)
	}
	server.listUserAgentModels(httptest.NewRecorder(), catalogReviewRequest(t))
	store.mu.Lock()
	if store.creates != 1 || store.eligibility != 3 {
		t.Errorf("cache creates=%d eligibility=%d", store.creates, store.eligibility)
	}
	store.eligible = false
	store.mu.Unlock()
	recorder := httptest.NewRecorder()
	server.listUserAgentModels(recorder, catalogReviewRequest(t))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("ineligible cache access status=%d", recorder.Code)
	}
}

func TestCatalogReviewCancelsOnlyAfterLastWaiter(t *testing.T) {
	store := &catalogReviewStore{eligible: true}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: time.Second}
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()
	var wg sync.WaitGroup
	for _, ctx := range []context.Context{ctx1, ctx2} {
		r := catalogReviewRequest(t)
		r = r.WithContext(context.WithValue(r.Context(), reviewCancelKey{}, ctx))
		ctx, cancel := context.WithCancel(r.Context())
		wg.Go(func() { <-ctx.Value(reviewCancelKey{}).(context.Context).Done(); cancel() })
		wg.Go(func() { server.listUserAgentModels(httptest.NewRecorder(), r.WithContext(ctx)) })
	}
	time.Sleep(150 * time.Millisecond)
	cancel1()
	time.Sleep(150 * time.Millisecond)
	store.mu.Lock()
	cancels := store.cancels
	store.mu.Unlock()
	if cancels != 0 {
		t.Errorf("first waiter cancelled shared discovery: %d", cancels)
	}
	cancel2()
	wg.Wait()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		cancels = store.cancels
		store.mu.Unlock()
		if cancels > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cancels != 1 {
		t.Fatalf("final waiter cancellation count=%d", cancels)
	}
}

type reviewCancelKey struct{}

func TestCatalogReviewSessionRouteKeepsConfiguredTimeout(t *testing.T) {
	store := &workspaceReviewHandlerStore{}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workerRequestTimeout: 100 * time.Millisecond}
	server.getChatModels(httptest.NewRecorder(), workspaceReviewRequest(t, http.MethodGet, "/chat-models", nil))
	if store.requestTimeout != 100*time.Millisecond {
		t.Fatalf("session timeout=%s", store.requestTimeout)
	}
}

func TestCatalogReviewCachesSuccessAndFailureWithBoundedLifetimes(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "invalid response"}[success], func(t *testing.T) {
			store := &catalogReviewStore{eligible: true, ready: true}
			if !success {
				store.response = json.RawMessage(`{}`)
			}
			server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			start := time.Now()
			recorder := httptest.NewRecorder()
			server.listUserAgentModels(recorder, catalogReviewRequest(t))
			status, ttl := http.StatusOK, 5*time.Minute
			if !success {
				status, ttl = http.StatusBadGateway, 5*time.Second
			}
			if recorder.Code != status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			server.listUserAgentModels(httptest.NewRecorder(), catalogReviewRequest(t))
			store.mu.Lock()
			creates := store.creates
			store.mu.Unlock()
			if creates != 1 {
				t.Fatalf("cached creates=%d", creates)
			}
			server.modelCatalogMu.Lock()
			for _, entry := range server.modelCatalogs {
				if entry.expires.Before(start.Add(ttl)) || entry.expires.After(time.Now().Add(ttl)) {
					t.Errorf("unexpected expiry=%s ttl=%s", entry.expires, ttl)
				}
				entry.expires = time.Now().Add(-time.Second)
			}
			server.modelCatalogMu.Unlock()
			server.listUserAgentModels(httptest.NewRecorder(), catalogReviewRequest(t))
			store.mu.Lock()
			creates = store.creates
			store.mu.Unlock()
			if creates != 2 {
				t.Fatalf("expired creates=%d", creates)
			}
		})
	}
}

func TestCatalogReviewBoundsEntriesAndSeparatesSources(t *testing.T) {
	store := &catalogReviewStore{eligible: true, ready: true}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), modelCatalogs: make(map[modelCatalogKey]*modelCatalogEntry)}
	for i := range 128 {
		key := modelCatalogKey{user: string(rune(i + 1))}
		server.modelCatalogs[key] = &modelCatalogEntry{done: make(chan struct{}), finished: false}
	}
	recorder := httptest.NewRecorder()
	server.listUserAgentModels(recorder, catalogReviewRequest(t))
	if recorder.Code != http.StatusTooManyRequests || store.creates != 0 || len(server.modelCatalogs) != 128 {
		t.Fatalf("capacity status=%d creates=%d entries=%d", recorder.Code, store.creates, len(server.modelCatalogs))
	}
	for _, entry := range server.modelCatalogs {
		entry.finished = true
		entry.expires = time.Now().Add(time.Hour)
		close(entry.done)
	}
	for _, change := range []func(*http.Request){
		func(*http.Request) {},
		func(r *http.Request) {
			*r = *r.WithContext(context.WithValue(r.Context(), principalKey, domain.Principal{UserID: "other-user"}))
		},
		func(r *http.Request) {
			chi.RouteContext(r.Context()).URLParams.Add("orgId", "00000000-0000-4000-8000-000000000099")
		},
		func(r *http.Request) { chi.RouteContext(r.Context()).URLParams.Add("agent", "claude-code") },
	} {
		r := catalogReviewRequest(t)
		change(r)
		recorder := httptest.NewRecorder()
		server.listUserAgentModels(recorder, r)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if len(server.modelCatalogs) != 128 {
			t.Fatalf("entries=%d", len(server.modelCatalogs))
		}
	}
	if store.creates != 4 {
		t.Fatalf("source-boundary creates=%d", store.creates)
	}
	store.source = "00000000-0000-4000-8000-000000000098"
	server.listUserAgentModels(httptest.NewRecorder(), catalogReviewRequest(t))
	if store.creates != 5 {
		t.Fatalf("replacement-session creates=%d", store.creates)
	}
}

func TestCatalogReviewKeepsHandlerNotFoundAndCancelsExpiredDiscovery(t *testing.T) {
	store := &catalogReviewStore{eligible: true, createErr: postgres.ErrNotFound}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for range 2 {
		recorder := httptest.NewRecorder()
		server.listUserAgentModels(recorder, catalogReviewRequest(t))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("handler status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var envelope errorEnvelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Code != "not_found" {
			t.Fatalf("handler envelope=%s error=%v", recorder.Body.String(), err)
		}
	}
	if store.creates != 1 || store.eligibility != 2 {
		t.Fatalf("creates=%d eligibility=%d", store.creates, store.eligibility)
	}
	store.createErr = nil
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	r := catalogReviewRequest(t)
	ctx = context.WithValue(ctx, principalKey, principalFrom(r))
	result := server.fetchModelCatalog(r.WithContext(ctx), workspaceTestOrgID, workspaceTestSessionID)
	if result.status != http.StatusGatewayTimeout || store.cancels != 1 {
		t.Fatalf("expired result=%+v cancels=%d", result, store.cancels)
	}
}

func TestCatalogReviewHTTPDisconnectCancelsSharedDiscoveryOnlyForFinalWaiter(t *testing.T) {
	store := &catalogReviewStore{eligible: true}
	server := &Server{store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := chi.NewRouteContext()
		route.URLParams.Add("orgId", workspaceTestOrgID)
		route.URLParams.Add("agent", "codex")
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, route)
		ctx = context.WithValue(ctx, principalKey, domain.Principal{UserID: "user-1"})
		server.listUserAgentModels(w, r.WithContext(ctx))
	}))
	defer httpServer.Close()
	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()
	defer cancel2()
	finished := []chan error{make(chan error, 1), make(chan error, 1)}
	for i, ctx := range []context.Context{ctx1, ctx2} {
		go func() {
			r, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
			if err == nil {
				var response *http.Response
				response, err = httpServer.Client().Do(r)
				if response != nil {
					response.Body.Close()
				}
			}
			finished[i] <- err
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	waiters := 0
	for time.Now().Before(deadline) {
		server.modelCatalogMu.Lock()
		for _, entry := range server.modelCatalogs {
			waiters = entry.waiters
		}
		server.modelCatalogMu.Unlock()
		if waiters == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiters != 2 {
		t.Fatalf("HTTP waiters=%d", waiters)
	}
	cancel1()
	if err := <-finished[0]; err == nil {
		t.Error("cancelled HTTP request succeeded")
	}
	time.Sleep(30 * time.Millisecond)
	store.mu.Lock()
	cancels := store.cancels
	store.mu.Unlock()
	if cancels != 0 {
		t.Fatalf("first HTTP disconnect cancelled shared discovery=%d", cancels)
	}
	cancel2()
	if err := <-finished[1]; err == nil {
		t.Error("cancelled HTTP request succeeded")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		cancels = store.cancels
		store.mu.Unlock()
		if cancels == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cancels != 1 {
		t.Fatalf("final HTTP disconnect cancellations=%d", cancels)
	}
}
