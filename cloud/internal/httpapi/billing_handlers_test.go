package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/billing"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

const (
	billingOrgID   = "11111111-1111-1111-1111-111111111111"
	billingUserID  = "22222222-2222-2222-2222-222222222222"
	webhookSecret  = "whsec_test"
	billingCusID   = "cus_123"
	billingSubID   = "sub_123"
	billingPriceID = "price_starter"
)

type fakeBillingStore struct {
	Store
	mu        sync.Mutex
	state     domain.OrgBilling
	customers map[string]string
	applied   []postgres.SubscriptionUpdate
	events    map[string]bool
	notAdmin  bool
	applyErr  error
}

func newFakeBillingStore() *fakeBillingStore {
	return &fakeBillingStore{
		state:     domain.OrgBilling{OrgID: billingOrgID, Plan: "free"},
		customers: map[string]string{},
		events:    map[string]bool{},
	}
}

func (f *fakeBillingStore) BillingSummary(context.Context, domain.Principal, string) (domain.BillingSummary, error) {
	return domain.BillingSummary{Enabled: true, Plan: f.state.Plan}, nil
}
func (f *fakeBillingStore) SubscriptionCancelAt(context.Context, domain.Principal, string) (*time.Time, error) {
	return nil, nil
}
func (f *fakeBillingStore) OrgBilling(context.Context, domain.Principal, string) (domain.OrgBilling, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *fakeBillingStore) RequireOrgAdmin(context.Context, domain.Principal, string) error {
	if f.notAdmin {
		return postgres.ErrForbidden
	}
	return nil
}
func (f *fakeBillingStore) OrgDisplayName(context.Context, string) (string, error) {
	return "Acme", nil
}
func (f *fakeBillingStore) LinkStripeCustomer(_ context.Context, orgID, customerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.customers[customerID] = orgID
	f.state.StripeCustomerID = customerID
	return nil
}
func (f *fakeBillingStore) OrgForStripeCustomer(_ context.Context, customerID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	orgID, ok := f.customers[customerID]
	return orgID, ok, nil
}
func (f *fakeBillingStore) ApplySubscription(_ context.Context, _ string, update postgres.SubscriptionUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied = append(f.applied, update)
	return nil
}
func (f *fakeBillingStore) StripeEventSeen(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events[id], nil
}
func (f *fakeBillingStore) RecordStripeEvent(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[id] = true
	return nil
}
func (f *fakeBillingStore) ResetWeeklyUsage(context.Context, domain.Principal, string) error {
	return nil
}
func (f *fakeBillingStore) WakeAllowed(context.Context, domain.Principal, string, string) error {
	return nil
}

type fakeStripe struct {
	mu            sync.Mutex
	customers     int
	checkouts     []billing.CheckoutRequest
	subscription  billing.Subscription
	subscriptions int
	portalCancels []string
}

func (f *fakeStripe) CreateCustomer(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.customers++
	return billingCusID, nil
}
func (f *fakeStripe) CreateCheckoutSession(_ context.Context, input billing.CheckoutRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkouts = append(f.checkouts, input)
	return "https://checkout.stripe.test/cs_1", nil
}
func (f *fakeStripe) CreatePortalSession(_ context.Context, _, _, _, cancelSubscriptionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.portalCancels = append(f.portalCancels, cancelSubscriptionID)
	return "https://billing.stripe.test/p_1", nil
}
func (f *fakeStripe) GetPrice(_ context.Context, id string) (billing.Price, error) {
	return billing.Price{ID: id, UnitAmount: 2000, Currency: "usd", Interval: "month"}, nil
}
func (f *fakeStripe) GetSubscription(context.Context, string) (billing.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscriptions++
	return f.subscription, nil
}

func billingServer(store *fakeBillingStore, stripe *fakeStripe) *Server {
	return New(Options{Store: store, Billing: &BillingOptions{
		Stripe: stripe, WebhookSecret: webhookSecret,
		PriceIDs: map[string]string{"starter": billingPriceID}, ReturnURL: "https://cp.test/billing/return",
	}})
}

func signedWebhook(t *testing.T, payload string) *http.Request {
	t.Helper()
	timestamp := fmt.Sprint(time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(timestamp + "." + payload))
	request := httptest.NewRequest(http.MethodPost, "/api/cloud/v1/billing/stripe/webhook", strings.NewReader(payload))
	request.Header.Set("Stripe-Signature", "t="+timestamp+",v1="+hex.EncodeToString(mac.Sum(nil)))
	return request
}

func activeStarterSubscription() billing.Subscription {
	limits := domain.PlanLimits{Plan: "starter", MaxActiveSandboxes: 2, OrchestratorSlots: 1, WindowHours: 5,
		WindowSessionHours: 6, WeeklySessionHours: 20, ManualResetsPerMonth: 1}
	return billing.Subscription{ID: billingSubID, CustomerID: billingCusID, Status: "active", OrgID: billingOrgID, Limits: &limits}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"evt_1","type":"customer.subscription.updated"}`))
	request.Header.Set("Stripe-Signature", "t=1,v1=00")
	response := httptest.NewRecorder()
	billingServer(store, stripe).stripeWebhook(response, request)
	if response.Code != http.StatusBadRequest || stripe.subscriptions != 0 || len(store.applied) != 0 {
		t.Fatalf("status %d, fetched %d, applied %d", response.Code, stripe.subscriptions, len(store.applied))
	}
}

func TestCheckoutCompletedLinksCustomerAndAppliesSubscriptionOnce(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: activeStarterSubscription()}
	server := billingServer(store, stripe)
	payload := `{"id":"evt_checkout","type":"checkout.session.completed","data":{"object":{"id":"cs_1","object":"checkout.session",` +
		`"customer":"` + billingCusID + `","subscription":"` + billingSubID + `","client_reference_id":"` + billingOrgID + `"}}}`
	for range 2 {
		response := httptest.NewRecorder()
		server.stripeWebhook(response, signedWebhook(t, payload))
		if response.Code != http.StatusOK {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
	}
	if store.customers[billingCusID] != billingOrgID {
		t.Fatalf("customer not linked: %v", store.customers)
	}
	if len(store.applied) != 1 || store.applied[0].Status != "active" || store.applied[0].Limits.Plan != "starter" {
		t.Fatalf("applied = %+v; want exactly one apply despite the redelivery", store.applied)
	}
}

func TestSubscriptionWebhookRereadsStripeAndRetriesOnFailure(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: activeStarterSubscription()}
	store.customers[billingCusID] = billingOrgID
	store.applyErr = errors.New("database unavailable")
	server := billingServer(store, stripe)
	payload := `{"id":"evt_update","type":"customer.subscription.updated","data":{"object":{"id":"` + billingSubID +
		`","object":"subscription","customer":"` + billingCusID + `","status":"canceled"}}}`
	response := httptest.NewRecorder()
	server.stripeWebhook(response, signedWebhook(t, payload))
	if response.Code != http.StatusInternalServerError || store.events["evt_update"] {
		t.Fatalf("a failed apply must answer 5xx and not mark the event done: %d %v", response.Code, store.events)
	}
	// Stripe retries; this time it succeeds, with Stripe's current state
	// (active) rather than the stale payload (canceled).
	store.applyErr = nil
	response = httptest.NewRecorder()
	server.stripeWebhook(response, signedWebhook(t, payload))
	if response.Code != http.StatusOK || len(store.applied) != 1 || store.applied[0].Status != "active" {
		t.Fatalf("retry: status %d applied %+v", response.Code, store.applied)
	}
}

func billingRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("orgId", billingOrgID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, route)
	ctx = context.WithValue(ctx, principalKey, domain.Principal{UserID: billingUserID, Provider: "local"})
	return request.WithContext(ctx)
}

func TestCheckoutCreatesCustomerOnceAndRefusesASecondSubscription(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	server := billingServer(store, stripe)

	response := httptest.NewRecorder()
	server.createBillingCheckout(response, billingRequest(http.MethodPost, "/", `{"plan":"enterprise"}`))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown plan: %d", response.Code)
	}

	response = httptest.NewRecorder()
	server.createBillingCheckout(response, billingRequest(http.MethodPost, "/", `{"plan":"starter"}`))
	var body map[string]string
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusOK || body["url"] == "" || stripe.customers != 1 ||
		len(stripe.checkouts) != 1 || stripe.checkouts[0].PriceID != billingPriceID || stripe.checkouts[0].OrgID != billingOrgID {
		t.Fatalf("checkout: %d %v customers=%d checkouts=%+v", response.Code, body, stripe.customers, stripe.checkouts)
	}

	store.state.StripeSubscriptionID, store.state.SubscriptionStatus = billingSubID, "active"
	response = httptest.NewRecorder()
	server.createBillingCheckout(response, billingRequest(http.MethodPost, "/", `{"plan":"starter"}`))
	if response.Code != http.StatusConflict || stripe.customers != 1 {
		t.Fatalf("second subscription: %d (customers created %d)", response.Code, stripe.customers)
	}

	store.notAdmin = true
	response = httptest.NewRecorder()
	server.createBillingPortal(response, billingRequest(http.MethodPost, "/", ``))
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-admin portal: %d", response.Code)
	}
}

func TestPlanErrorsMapToActionableResponses(t *testing.T) {
	resets := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{postgres.ErrPlanRequired, http.StatusPaymentRequired, "PLAN_REQUIRED"},
		{postgres.ErrNoResetsLeft, http.StatusConflict, "NO_RESETS_LEFT"},
		{&postgres.PlanLimitError{Code: postgres.LimitWeekly, Plan: "starter", Limit: 1200, Used: 1200, ResetsAt: &resets},
			http.StatusTooManyRequests, postgres.LimitWeekly},
		{&postgres.PlanLimitError{Code: postgres.LimitWorkerSlots, Plan: "starter", Limit: 1, Used: 1},
			http.StatusConflict, postgres.LimitWorkerSlots},
	} {
		response := httptest.NewRecorder()
		if !writePlanError(response, httptest.NewRequest(http.MethodPost, "/", nil), tc.err) {
			t.Fatalf("%v not handled", tc.err)
		}
		var body struct {
			Code     string     `json:"code"`
			ResetsAt *time.Time `json:"resetsAt"`
			Limit    int        `json:"limit"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != tc.status || body.Code != tc.code {
			t.Fatalf("%v: %d %s", tc.err, response.Code, body.Code)
		}
		if tc.code == postgres.LimitWeekly && (body.ResetsAt == nil || !body.ResetsAt.Equal(resets) || body.Limit != 1200) {
			t.Fatalf("limit details missing: %+v", body)
		}
	}
	if writePlanError(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), errors.New("other")) {
		t.Fatal("a non-billing error was treated as a plan error")
	}
}

func TestBillingSummaryReportsDisabledWithoutStripe(t *testing.T) {
	store := newFakeBillingStore()
	server := New(Options{Store: store})
	response := httptest.NewRecorder()
	server.getBilling(response, billingRequest(http.MethodGet, "/", ""))
	var body billingSummaryResponse
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusOK || body.Enabled || len(body.Plans) != 0 {
		t.Fatalf("summary without Stripe: %d %+v", response.Code, body)
	}
}

func TestBillingSummaryListsPlansPricedFromStripe(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	response := httptest.NewRecorder()
	billingServer(store, stripe).getBilling(response, billingRequest(http.MethodGet, "/", ""))
	var body billingSummaryResponse
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusOK || !body.Enabled || len(body.Plans) != 1 ||
		body.Plans[0].Name != "starter" || body.Plans[0].UnitAmount != 2000 || body.Plans[0].Interval != "month" {
		t.Fatalf("summary: %d %+v", response.Code, body)
	}
}

// Downgrading to Free opens the portal straight on cancelling the plan; a
// plain request opens the portal home, and there is nothing to cancel without
// a live subscription.
func TestPortalCancelFlow(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	server := billingServer(store, stripe)
	store.state.StripeCustomerID = "cus_1"

	response := httptest.NewRecorder()
	server.createBillingPortal(response, billingRequest(http.MethodPost, "/", `{"flow":"cancel"}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("cancel without a subscription: %d", response.Code)
	}

	store.state.StripeSubscriptionID, store.state.SubscriptionStatus = billingSubID, "active"
	for _, body := range []string{``, `{}`, `{"flow":"cancel"}`} {
		response = httptest.NewRecorder()
		server.createBillingPortal(response, billingRequest(http.MethodPost, "/", body))
		if response.Code != http.StatusOK {
			t.Fatalf("portal %q: %d %s", body, response.Code, response.Body.String())
		}
	}
	if len(stripe.portalCancels) != 3 || stripe.portalCancels[0] != "" || stripe.portalCancels[1] != "" ||
		stripe.portalCancels[2] != billingSubID {
		t.Fatalf("portal cancel targets = %q", stripe.portalCancels)
	}

	response = httptest.NewRecorder()
	server.createBillingPortal(response, billingRequest(http.MethodPost, "/", `{"flow":"refund"}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown flow: %d", response.Code)
	}
}
