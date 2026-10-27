package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

type capturedActivity struct {
	mu      sync.Mutex
	events  []domain.ActivityEvent
	release chan struct{}
}

func (c *capturedActivity) RecordActivityEvent(ctx context.Context, event domain.ActivityEvent) error {
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

func (c *capturedActivity) waitFor(t *testing.T, kinds ...string) []domain.ActivityEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		seen := map[string]bool{}
		for _, event := range c.events {
			seen[event.Kind] = true
		}
		events := append([]domain.ActivityEvent(nil), c.events...)
		c.mu.Unlock()
		missing := false
		for _, kind := range kinds {
			if !seen[kind] {
				missing = true
			}
		}
		if !missing {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("activity = %+v, want kinds %v", events, kinds)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startRecorder(t *testing.T, store activity.Store, queue int) *activity.Recorder {
	t.Helper()
	recorder := activity.NewRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)), activity.Options{QueueSize: queue})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go recorder.Run(ctx)
	return recorder
}

func TestRequestLogCarriesTraceFields(t *testing.T) {
	var logs bytes.Buffer
	server := New(Options{Store: newFakeBillingStore(), Logger: slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))})
	router := chi.NewRouter()
	router.Use(server.requestID)
	router.Use(server.requestLog)
	router.Get("/api/cloud/v1/orgs/{orgId}/sessions/{sessionId}", func(w http.ResponseWriter, r *http.Request) {
		// What authenticate and a handler would learn along the way.
		logctx.Annotate(r.Context(), logctx.Fields{UserID: "user-1", Provider: "coder", ConnectionID: "conn-1"})
		server.logger.InfoContext(r.Context(), "handler line")
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/cloud/v1/orgs/org-1/sessions/session-1", nil)
	request.Header.Set("X-Request-ID", "req-abc")
	router.ServeHTTP(httptest.NewRecorder(), request)

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %v", lines)
	}
	for _, raw := range lines {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"request_id": "req-abc", "user_id": "user-1", "sandbox_provider": "coder", "connection_id": "conn-1",
		} {
			if line[key] != want {
				t.Fatalf("%s = %v in %v", key, line[key], line)
			}
		}
		if line["msg"] == "HTTP request complete" &&
			(line["org_id"] != "org-1" || line["session_id"] != "session-1" || line["status"] != float64(204)) {
			t.Fatalf("access line = %v", line)
		}
	}
	if strings.Count(lines[1], `"request_id"`) != 1 {
		t.Fatalf("request_id duplicated: %s", lines[1])
	}
}

type fakeSupportStore struct {
	*fakeBillingStore
	queries []postgres.SupportQuery
}

func (f *fakeSupportStore) SupportLookup(_ context.Context, query postgres.SupportQuery) (postgres.SupportReport, error) {
	f.queries = append(f.queries, query)
	if query.SessionID == "missing" {
		return postgres.SupportReport{}, postgres.ErrNotFound
	}
	return postgres.SupportReport{Users: []postgres.SupportUser{{UserID: "user-1", Email: query.Email}}}, nil
}

func TestSupportLookupIsOperatorOnly(t *testing.T) {
	const token = "support-token-0123456789-0123456789"
	var logs bytes.Buffer
	store := &fakeSupportStore{fakeBillingStore: newFakeBillingStore()}

	disabled := New(Options{Store: store}).Handler()
	response := httptest.NewRecorder()
	disabled.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/cloud/v1/control/support/lookup?email=a@b.c", nil))
	if response.Code == http.StatusOK {
		t.Fatal("the support route must not exist without AO_CLOUD_SUPPORT_TOKEN")
	}

	handler := New(Options{Store: store, SupportToken: token,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil))}).Handler()
	for _, header := range []string{"", "Bearer wrong", "Basic " + token} {
		request := httptest.NewRequest(http.MethodGet, "/api/cloud/v1/control/support/lookup?email=a@b.c", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q: status %d", header, response.Code)
		}
	}
	if len(store.queries) != 0 {
		t.Fatalf("unauthenticated lookups reached the store: %+v", store.queries)
	}

	request := httptest.NewRequest(http.MethodGet,
		"/api/cloud/v1/control/support/lookup?email=Dev@Example.com&sessions=5000&activity=7", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if len(store.queries) != 1 || store.queries[0].Email != "Dev@Example.com" ||
		store.queries[0].SessionLimit != 200 || store.queries[0].ActivityLimit != 7 {
		t.Fatalf("queries = %+v", store.queries)
	}
	if strings.Contains(logs.String(), "Dev@Example.com") {
		t.Fatalf("the looked-up email was logged:\n%s", logs.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/cloud/v1/control/support/lookup?sessionId=missing", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing session: %d", response.Code)
	}
}

func TestBillingFunnelReachesTheActivityTrail(t *testing.T) {
	captured := &capturedActivity{}
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: activeStarterSubscription()}
	server := New(Options{Store: store, Activity: startRecorder(t, captured, 64), Billing: &BillingOptions{
		Stripe: stripe, WebhookSecret: webhookSecret,
		PriceIDs: map[string]string{"starter": billingPriceID}, ReturnURL: "https://cp.test/billing/return",
	}})

	response := httptest.NewRecorder()
	server.createBillingCheckout(response, billingRequest(http.MethodPost, "/", `{"plan":"starter"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("checkout: %d %s", response.Code, response.Body.String())
	}
	payload := `{"id":"evt_checkout_activity","type":"checkout.session.completed","data":{"object":{"id":"cs_1","object":"checkout.session",` +
		`"customer":"` + billingCusID + `","subscription":"` + billingSubID + `","client_reference_id":"` + billingOrgID + `"}}}`
	response = httptest.NewRecorder()
	server.stripeWebhook(response, signedWebhook(t, payload))
	if response.Code != http.StatusOK {
		t.Fatalf("webhook: %d", response.Code)
	}
	server.writeStoreError(httptest.NewRecorder(), billingRequest(http.MethodPost, "/", ""), postgres.ErrPlanRequired)
	server.writeStoreError(httptest.NewRecorder(), billingRequest(http.MethodPost, "/", ""),
		&postgres.PlanLimitError{Code: postgres.LimitWeekly, Plan: "starter", Limit: 1200, Used: 1200})
	// Ordinary store errors are not plan refusals.
	server.writeStoreError(httptest.NewRecorder(), billingRequest(http.MethodPost, "/", ""), postgres.ErrNotFound)

	events := captured.waitFor(t, domain.ActivityCheckoutStarted, domain.ActivityCheckoutCompleted,
		domain.ActivityPlanChanged, domain.ActivityPlanRefused, domain.ActivityLimitHit)
	for _, event := range events {
		if event.OrgID != billingOrgID {
			t.Fatalf("event for the wrong organization: %+v", event)
		}
		switch event.Kind {
		case domain.ActivityCheckoutStarted:
			if event.UserID != billingUserID || event.Attrs["plan"] != "starter" {
				t.Fatalf("checkout_started = %+v", event)
			}
		case domain.ActivityPlanChanged:
			if event.Attrs["plan"] != "starter" || event.Attrs["subscription_status"] != "active" {
				t.Fatalf("plan_changed = %+v", event)
			}
		case domain.ActivityLimitHit:
			if event.Attrs["code"] != postgres.LimitWeekly || event.Attrs["used"] != int64(1200) {
				t.Fatalf("limit_hit = %+v", event)
			}
		}
	}
	if len(events) != 5 {
		t.Fatalf("events = %+v, want exactly five", events)
	}
}

func TestActivityNeverBlocksARequest(t *testing.T) {
	captured := &capturedActivity{release: make(chan struct{})}
	defer close(captured.release)
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	server := New(Options{Store: store, Activity: startRecorder(t, captured, 1), Billing: &BillingOptions{
		Stripe: stripe, WebhookSecret: webhookSecret,
		PriceIDs: map[string]string{"starter": billingPriceID}, ReturnURL: "https://cp.test/billing/return",
	}})
	started := time.Now()
	for range 50 {
		store.state.StripeSubscriptionID = ""
		response := httptest.NewRecorder()
		server.createBillingCheckout(response, billingRequest(http.MethodPost, "/", `{"plan":"starter"}`))
		if response.Code != http.StatusOK {
			t.Fatalf("checkout: %d", response.Code)
		}
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("50 requests with a stuck activity store took %s", elapsed)
	}
}

type readinessStore struct {
	*fakeBillingStore
	readiness postgres.SandboxReadiness
	err       error
}

func (s *readinessStore) SandboxReadiness(context.Context, string, string) (postgres.SandboxReadiness, error) {
	return s.readiness, s.err
}

func TestWorkerReadyMeasuresTimeToReady(t *testing.T) {
	const orgID, sessionID = "11111111-1111-1111-1111-111111111111", "33333333-3333-3333-3333-333333333333"
	for name, tc := range map[string]struct {
		readiness postgres.SandboxReadiness
		phase     string
		minTTR    int64
	}{
		"first start": {postgres.SandboxReadiness{Provider: "freestyle", SessionKind: "worker",
			SessionCreatedAt: time.Now().Add(-9 * time.Second)}, "create", 9000},
		"resume": {postgres.SandboxReadiness{Provider: "coder", ConnectionID: "conn-1", SessionKind: "worker",
			SessionCreatedAt:     time.Now().Add(-48 * time.Hour),
			PreviousWorkerSeenAt: ptr(time.Now().Add(-time.Hour)),
			StartupStartedAt:     ptr(time.Now().Add(-3 * time.Second))}, "resume", 3000},
	} {
		t.Run(name, func(t *testing.T) {
			captured := &capturedActivity{}
			store := &readinessStore{fakeBillingStore: newFakeBillingStore(), readiness: tc.readiness}
			server := New(Options{Store: store, Activity: startRecorder(t, captured, 16)})
			readiness, err := server.sandboxReadiness(context.Background(), orgID, sessionID)
			server.recordWorkerReady(httptest.NewRequest(http.MethodPost, "/", nil), orgID, sessionID,
				"worker-1", "v1", 3, readiness, err)
			events := captured.waitFor(t, domain.ActivityWorkerConnected, domain.ActivitySandboxReady)
			for _, event := range events {
				if event.Kind != domain.ActivitySandboxReady {
					continue
				}
				ttr, _ := event.Attrs["ttr_ms"].(int64)
				if event.Attrs["phase"] != tc.phase || ttr < tc.minTTR || ttr > tc.minTTR+5000 ||
					event.Provider != tc.readiness.Provider {
					t.Fatalf("sandbox.ready = %+v", event)
				}
			}
		})
	}

	// Without readiness (an older store) only worker.connected is recorded.
	captured := &capturedActivity{}
	server := New(Options{Store: newFakeBillingStore(), Activity: startRecorder(t, captured, 16)})
	readiness, err := server.sandboxReadiness(context.Background(), orgID, sessionID)
	if !errors.Is(err, errReadinessUnavailable) {
		t.Fatalf("err = %v", err)
	}
	server.recordWorkerReady(httptest.NewRequest(http.MethodPost, "/", nil), orgID, sessionID, "worker-1", "v1", 1, readiness, err)
	events := captured.waitFor(t, domain.ActivityWorkerConnected)
	time.Sleep(20 * time.Millisecond)
	for _, event := range events {
		if event.Kind == domain.ActivitySandboxReady {
			t.Fatalf("sandbox.ready without readiness: %+v", event)
		}
	}
}

func ptr[T any](value T) *T { return &value }
