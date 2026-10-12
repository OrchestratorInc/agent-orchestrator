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
	mode      string
	customers map[string]string
	applied   []postgres.SubscriptionUpdate
	events    map[string]bool
	notAdmin  bool
	applyErr  error
	linkErr   error
	cleared   []string
	touches   int
	cancelAt  *time.Time
}

func newFakeBillingStore() *fakeBillingStore {
	return &fakeBillingStore{
		state:     domain.OrgBilling{OrgID: billingOrgID, Plan: "free"},
		customers: map[string]string{},
		events:    map[string]bool{},
	}
}

func (f *fakeBillingStore) BillingSummary(context.Context, domain.Principal, string) (domain.BillingSummary, error) {
	mode := f.mode
	if mode == "" {
		mode = domain.BillingModeSelfServe
	}
	return domain.BillingSummary{Enabled: true, BillingMode: mode, Plan: f.state.Plan}, nil
}
func (f *fakeBillingStore) SubscriptionCancelAt(context.Context, domain.Principal, string) (*time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelAt, nil
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
	if f.linkErr != nil {
		return f.linkErr
	}
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
func (f *fakeBillingStore) ApplySubscriptionLocked(
	ctx context.Context, orgID string,
	fetch func(context.Context, domain.OrgBilling) (postgres.SubscriptionUpdate, error),
) (postgres.SubscriptionOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if orgID != f.state.OrgID {
		return "", postgres.ErrNotFound
	}
	if f.applyErr != nil {
		return "", f.applyErr
	}
	update, err := fetch(ctx, f.state)
	if err != nil {
		return "", err
	}
	f.applied = append(f.applied, update)
	return postgres.SubscriptionApplied, nil
}
func (f *fakeBillingStore) StartCheckout(
	ctx context.Context, _ domain.Principal, _ string,
	start func(context.Context, domain.OrgBilling) (postgres.CheckoutStart, error),
) (*domain.PendingCheckout, error) {
	if f.notAdmin {
		return nil, postgres.ErrForbidden
	}
	// Serialized, like the organization lock the store holds.
	f.mu.Lock()
	defer f.mu.Unlock()
	started, err := start(ctx, f.state)
	if started.CustomerID != "" {
		f.customers[started.CustomerID] = f.state.OrgID
		f.state.StripeCustomerID = started.CustomerID
	}
	if started.Pending != nil {
		f.state.PendingCheckout = started.Pending
	} else if started.ClearPending {
		f.state.PendingCheckout = nil
	}
	if err != nil {
		return nil, err
	}
	return started.Pending, nil
}
func (f *fakeBillingStore) ClearPendingCheckout(_ context.Context, _ string, checkoutID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, checkoutID)
	if f.state.PendingCheckout != nil && f.state.PendingCheckout.ID == checkoutID {
		f.state.PendingCheckout = nil
	}
	return nil
}
func (f *fakeBillingStore) OrgHasFeature(context.Context, string, string) bool { return true }
func (f *fakeBillingStore) TouchSessionPresence(context.Context, domain.Principal, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touches++
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
	expired       []string
	expireStatus  string
	subscription  billing.Subscription
	subscriptions int
	prices        int
	priceErr      error
	portalCancels []string
	// productSubs maps a product to the subscriptions on it.
	productSubs map[string][]string
}

func (f *fakeStripe) CreateCustomer(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.customers++
	return billingCusID, nil
}
func (f *fakeStripe) CreateCheckoutSession(_ context.Context, input billing.CheckoutRequest) (billing.CheckoutSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkouts = append(f.checkouts, input)
	id := fmt.Sprintf("cs_%d", len(f.checkouts))
	return billing.CheckoutSession{ID: id, URL: "https://checkout.stripe.test/" + id, ExpiresAt: input.ExpiresAt}, nil
}
func (f *fakeStripe) ExpireCheckoutSession(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expired = append(f.expired, id)
	if f.expireStatus != "" {
		return f.expireStatus, nil
	}
	return "expired", nil
}
func (f *fakeStripe) CreatePortalSession(_ context.Context, _, _, _, cancelSubscriptionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.portalCancels = append(f.portalCancels, cancelSubscriptionID)
	return "https://billing.stripe.test/p_1", nil
}
func (f *fakeStripe) GetPrice(_ context.Context, id string) (billing.Price, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prices++
	if f.priceErr != nil {
		return billing.Price{}, f.priceErr
	}
	return billing.Price{ID: id, UnitAmount: 2000, Currency: "usd", Interval: "month"}, nil
}
func (f *fakeStripe) ProductSubscriptions(_ context.Context, productID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.productSubs[productID], nil
}
func (f *fakeStripe) GetSubscription(context.Context, string) (billing.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscriptions++
	return f.subscription, nil
}

func billingServer(store *fakeBillingStore, stripe *fakeStripe) *Server {
	return New(Options{Store: store, AvailableSandboxProviders: []string{"docker", "freestyle"}, Billing: &BillingOptions{
		Stripe: stripe, WebhookSecret: webhookSecret,
		PriceIDs: map[string]string{"starter": billingPriceID, "pro": "price_pro"}, ReturnURL: "https://cp.test/billing/return",
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
	if response.Code != http.StatusOK || !body.Enabled || len(body.Plans) != 2 || body.BillingMode != domain.BillingModeSelfServe ||
		body.Plans[0].UnitAmount != 2000 || body.Plans[0].Interval != "month" || body.Features == nil {
		t.Fatalf("summary: %d %+v", response.Code, body)
	}
}

func getBillingSummary(t *testing.T, server *Server) billingSummaryResponse {
	t.Helper()
	response := httptest.NewRecorder()
	server.getBilling(response, billingRequest(http.MethodGet, "/", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", response.Code, response.Body.String())
	}
	var body billingSummaryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The app shows plans only to self-serve organizations that can run a billed
// provider; everyone else gets a billing mode that hides them.
func TestBillingModeHidesPlansWhenTheyDoNotApply(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	if body := getBillingSummary(t, New(Options{Store: store})); body.BillingMode != domain.BillingModeOff || body.Enabled {
		t.Fatalf("without Stripe: %+v", body)
	}
	store.mode = domain.BillingModeContract
	if body := getBillingSummary(t, billingServer(store, stripe)); body.BillingMode != domain.BillingModeContract || len(body.Plans) != 0 {
		t.Fatalf("contract: %+v", body)
	}
	store.mode = domain.BillingModeSelfServe
	coderOnly := New(Options{Store: store, SandboxProvider: "coder", Billing: &BillingOptions{
		Stripe: stripe, WebhookSecret: webhookSecret, PriceIDs: map[string]string{"starter": billingPriceID},
	}})
	if body := getBillingSummary(t, coderOnly); body.BillingMode != domain.BillingModeOff || len(body.Plans) != 0 {
		t.Fatalf("no billed provider offered: %+v", body)
	}
	gated := New(Options{Store: store, AvailableSandboxProviders: []string{"coder", "freestyle"},
		CapabilityGatedProviders: []string{"freestyle"}, Billing: &BillingOptions{
			Stripe: stripe, WebhookSecret: webhookSecret, PriceIDs: map[string]string{"starter": billingPriceID},
		}})
	if body := getBillingSummary(t, gated); body.BillingMode != domain.BillingModeOff {
		t.Fatalf("freestyle not entitled: %+v", body)
	}
	if stripe.prices != 0 {
		t.Fatalf("priced plans nobody is shown: %d Stripe calls", stripe.prices)
	}
}

// A failed price read is retried after a short while, not on every load.
func TestPlanOffersCacheFailuresBriefly(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{priceErr: errors.New("stripe down")}
	server := billingServer(store, stripe)
	first := getBillingSummary(t, server)
	if len(first.Plans) != 2 || first.Plans[0].UnitAmount != 0 {
		t.Fatalf("plans must be listed by name when prices fail: %+v", first.Plans)
	}
	calls := stripe.prices
	getBillingSummary(t, server)
	if stripe.prices != calls {
		t.Fatalf("a failed listing was refetched at once: %d -> %d calls", calls, stripe.prices)
	}
	server.offerCache.mu.Lock()
	server.offerCache.fetched = time.Now().Add(-2 * planOfferRetryTTL)
	server.offerCache.mu.Unlock()
	stripe.mu.Lock()
	stripe.priceErr = nil
	stripe.mu.Unlock()
	if body := getBillingSummary(t, server); body.Plans[0].UnitAmount != 2000 {
		t.Fatalf("not retried after the retry interval: %+v", body.Plans)
	}
}

// While a Checkout is open the same link is handed out again; choosing
// another plan closes it first; a Checkout paid in the meantime is not
// replaced by a second one.
func TestCheckoutReusesTheOpenSessionAndClosesItForAnotherPlan(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	server := billingServer(store, stripe)
	checkout := func(plan string) (int, string) {
		response := httptest.NewRecorder()
		server.createBillingCheckout(response, billingRequest(http.MethodPost, "/", `{"plan":"`+plan+`"}`))
		var body map[string]string
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response.Code, body["url"] + body["code"]
	}
	code, first := checkout("starter")
	if code != http.StatusOK || len(stripe.checkouts) != 1 || stripe.checkouts[0].ExpiresAt.IsZero() {
		t.Fatalf("first: %d %s %+v", code, first, stripe.checkouts)
	}
	if code, again := checkout("starter"); code != http.StatusOK || again != first || len(stripe.checkouts) != 1 {
		t.Fatalf("same plan again: %d %s, %d checkouts", code, again, len(stripe.checkouts))
	}
	code, pro := checkout("pro")
	if code != http.StatusOK || pro == first || len(stripe.expired) != 1 || stripe.expired[0] != "cs_1" ||
		len(stripe.checkouts) != 2 || stripe.customers != 1 {
		t.Fatalf("other plan: %d %s expired=%v checkouts=%d customers=%d", code, pro, stripe.expired, len(stripe.checkouts), stripe.customers)
	}
	stripe.expireStatus = "complete"
	if code, body := checkout("starter"); code != http.StatusConflict || body != "CHECKOUT_COMPLETED" ||
		len(stripe.checkouts) != 2 {
		t.Fatalf("paid meanwhile: %d %s, %d checkouts", code, body, len(stripe.checkouts))
	}
	// An expired link is not handed out again.
	store.state.PendingCheckout = &domain.PendingCheckout{ID: "cs_old", URL: "https://old", PriceID: billingPriceID,
		ExpiresAt: time.Now().Add(-time.Minute)}
	stripe.expireStatus = ""
	if code, link := checkout("starter"); code != http.StatusOK || link == "https://old" || len(stripe.checkouts) != 3 {
		t.Fatalf("expired pending: %d %s", code, link)
	}
}

// Concurrent requests to subscribe start one Checkout between them.
func TestConcurrentCheckoutsStartOneSession(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	server := billingServer(store, stripe)
	var wg sync.WaitGroup
	links := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := httptest.NewRecorder()
			server.createBillingCheckout(response, billingRequest(http.MethodPost, "/", `{"plan":"starter"}`))
			var body map[string]string
			_ = json.Unmarshal(response.Body.Bytes(), &body)
			links <- body["url"]
		}()
	}
	wg.Wait()
	close(links)
	seen := map[string]bool{}
	for link := range links {
		seen[link] = true
	}
	if len(stripe.checkouts) != 1 || len(seen) != 1 {
		t.Fatalf("%d checkouts, links %v", len(stripe.checkouts), seen)
	}
}

// Events for an organization that does not exist, or that cannot be one, are
// acknowledged so Stripe stops retrying them.
func TestWebhookAcknowledgesEventsForUnknownOrganizations(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: activeStarterSubscription()}
	server := billingServer(store, stripe)
	deliver := func(id, payload string) int {
		response := httptest.NewRecorder()
		server.stripeWebhook(response, signedWebhook(t, payload))
		if !store.events[id] && response.Code == http.StatusOK {
			t.Fatalf("%s acknowledged but not recorded", id)
		}
		return response.Code
	}
	notAnOrg := `{"id":"evt_a","type":"checkout.session.completed","data":{"object":{"id":"cs_1","customer":"cus_x",` +
		`"subscription":"sub_x","client_reference_id":"not-a-uuid"}}}`
	if code := deliver("evt_a", notAnOrg); code != http.StatusOK || len(store.customers) != 0 || stripe.subscriptions != 0 {
		t.Fatalf("non-UUID reference: %d customers=%v fetched=%d", code, store.customers, stripe.subscriptions)
	}
	store.linkErr = postgres.ErrNotFound
	deleted := `{"id":"evt_b","type":"checkout.session.completed","data":{"object":{"id":"cs_2","customer":"cus_y",` +
		`"subscription":"sub_y","client_reference_id":"33333333-3333-3333-3333-333333333333"}}}`
	if code := deliver("evt_b", deleted); code != http.StatusOK || len(store.applied) != 0 {
		t.Fatalf("deleted org: %d applied=%v", code, store.applied)
	}
	store.linkErr = nil
	// A subscription whose customer maps to an organization that was since
	// deleted.
	store.customers["cus_gone"] = "44444444-4444-4444-4444-444444444444"
	gone := `{"id":"evt_c","type":"customer.subscription.updated","data":{"object":{"id":"sub_1","customer":"cus_gone"}}}`
	if code := deliver("evt_c", gone); code != http.StatusOK || len(store.applied) != 0 {
		t.Fatalf("subscription of a deleted org: %d applied=%v", code, store.applied)
	}
}

// Invoices on API version 2025-03-31.basil name their subscription under
// parent.subscription_details; the subscription is synced from it.
func TestInvoiceWebhookSyncsTheSubscriptionInEitherShape(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: activeStarterSubscription()}
	store.customers[billingCusID] = billingOrgID
	server := billingServer(store, stripe)
	for index, object := range []string{
		`{"id":"in_1","customer":"` + billingCusID + `","parent":{"subscription_details":{"subscription":"` + billingSubID + `"}}}`,
		`{"id":"in_2","customer":"` + billingCusID + `","subscription":"` + billingSubID + `"}`,
	} {
		payload := fmt.Sprintf(`{"id":"evt_inv_%d","type":"invoice.paid","data":{"object":%s}}`, index, object)
		response := httptest.NewRecorder()
		server.stripeWebhook(response, signedWebhook(t, payload))
		if response.Code != http.StatusOK || len(store.applied) != index+1 || store.applied[index].SubscriptionID != billingSubID {
			t.Fatalf("invoice %d: %d applied=%+v", index, response.Code, store.applied)
		}
	}
}

// A completed or expired Checkout is forgotten, so the next request starts a
// fresh one.
func TestCheckoutEventsClearThePendingSession(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: activeStarterSubscription()}
	store.state.PendingCheckout = &domain.PendingCheckout{ID: "cs_9", URL: "https://x", PriceID: billingPriceID,
		ExpiresAt: time.Now().Add(time.Hour)}
	payload := `{"id":"evt_exp","type":"checkout.session.expired","data":{"object":{"id":"cs_9",` +
		`"client_reference_id":"` + billingOrgID + `"}}}`
	response := httptest.NewRecorder()
	billingServer(store, stripe).stripeWebhook(response, signedWebhook(t, payload))
	if response.Code != http.StatusOK || store.state.PendingCheckout != nil || len(store.cleared) != 1 {
		t.Fatalf("expired: %d pending=%+v", response.Code, store.state.PendingCheckout)
	}
}

// Presence heartbeats for one session reach the store at most once per
// interval.
func TestPresenceHeartbeatsAreRateLimitedPerSession(t *testing.T) {
	store := newFakeBillingStore()
	server := New(Options{Store: store})
	ping := func(sessionID string) {
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		route := chi.NewRouteContext()
		route.URLParams.Add("orgId", billingOrgID)
		route.URLParams.Add("sessionId", sessionID)
		ctx := context.WithValue(request.Context(), chi.RouteCtxKey, route)
		ctx = context.WithValue(ctx, principalKey, domain.Principal{UserID: billingUserID, Provider: "local"})
		response := httptest.NewRecorder()
		server.touchSessionPresence(response, request.WithContext(ctx))
		if response.Code != http.StatusNoContent {
			t.Fatalf("presence: %d", response.Code)
		}
	}
	first, second := "55555555-5555-5555-5555-555555555555", "66666666-6666-6666-6666-666666666666"
	ping(first)
	ping(first)
	ping(second)
	if store.touches != 2 {
		t.Fatalf("touches = %d; want one per session within the interval", store.touches)
	}
	if !server.presenceLimiter.allow("x", time.Now().Add(-postgres.PresenceInterval)) ||
		!server.presenceLimiter.allow("x", time.Now()) {
		t.Fatal("a heartbeat after the interval must go through")
	}
}

// Downgrading to Free opens the portal straight on cancelling the plan; a
// plain request opens the portal home, and there is nothing to cancel without
// a live subscription.
func TestPortalCancelFlow(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{}
	server := billingServer(store, stripe)
	store.state.StripeCustomerID = "cus_1"
	store.customers["cus_1"] = billingOrgID
	stripe.subscription = billing.Subscription{ID: billingSubID, CustomerID: "cus_1", Status: "active"}

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

	// Already set to cancel in Stripe (and not yet known here): the end date
	// is stored and the portal opens on its home page instead of failing.
	ends := time.Now().Add(10 * 24 * time.Hour).UTC()
	stripe.subscription.CancelAt = &ends
	response = httptest.NewRecorder()
	server.createBillingPortal(response, billingRequest(http.MethodPost, "/", `{"flow":"cancel"}`))
	if response.Code != http.StatusOK || stripe.portalCancels[3] != "" {
		t.Fatalf("already cancelling: %d, cancel target %q", response.Code, stripe.portalCancels[3])
	}
	last := store.applied[len(store.applied)-1]
	if last.CancelAt == nil || !last.CancelAt.Equal(ends) {
		t.Fatalf("stored cancel at = %v, want %v", last.CancelAt, ends)
	}

	response = httptest.NewRecorder()
	server.createBillingPortal(response, billingRequest(http.MethodPost, "/", `{"flow":"refund"}`))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown flow: %d", response.Code)
	}
}

// When a plan set to cancel ends goes from Stripe to the store through the
// locked apply, and back out on the billing summary.
func TestCancelAtFlowsFromStripeToTheSummary(t *testing.T) {
	cancelAt := time.Unix(1_802_592_000, 0).UTC()
	subscription := activeStarterSubscription()
	subscription.CancelAt = &cancelAt
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: subscription}
	store.customers[billingCusID] = billingOrgID
	server := billingServer(store, stripe)
	payload := `{"id":"evt_cancel","type":"customer.subscription.updated","data":{"object":{"id":"` + billingSubID +
		`","object":"subscription","customer":"` + billingCusID + `","status":"active"}}}`
	response := httptest.NewRecorder()
	server.stripeWebhook(response, signedWebhook(t, payload))
	if response.Code != http.StatusOK || len(store.applied) != 1 || store.applied[0].CancelAt == nil ||
		!store.applied[0].CancelAt.Equal(cancelAt) {
		t.Fatalf("status %d applied %+v", response.Code, store.applied)
	}
	if summary := getBillingSummary(t, server); summary.CancelAt != nil {
		t.Fatalf("renewing plan reports cancelAt %v", summary.CancelAt)
	}
	store.cancelAt = &cancelAt
	if summary := getBillingSummary(t, server); summary.CancelAt == nil || !summary.CancelAt.Equal(cancelAt) {
		t.Fatalf("cancelAt = %v, want %v", summary.CancelAt, cancelAt)
	}
}

// Editing a plan product's metadata in Stripe re-reads every subscription on
// it, so new limits apply at once rather than at each renewal, and refreshes
// the cached plan cards.
func TestProductUpdateReappliesItsSubscriptions(t *testing.T) {
	store, stripe := newFakeBillingStore(), &fakeStripe{subscription: activeStarterSubscription()}
	store.customers[billingCusID] = billingOrgID
	stripe.productSubs = map[string][]string{"prod_starter": {billingSubID}}
	server := billingServer(store, stripe)

	// Prime the plan-card cache.
	_ = server.planOffers(context.Background())
	pricesBefore := stripe.prices

	payload := `{"id":"evt_product","type":"product.updated","data":{"object":{"id":"prod_starter","object":"product"}}}`
	response := httptest.NewRecorder()
	server.stripeWebhook(response, signedWebhook(t, payload))
	if response.Code != http.StatusOK || len(store.applied) != 1 || store.applied[0].SubscriptionID != billingSubID ||
		!store.events["evt_product"] {
		t.Fatalf("product update: status %d applied %+v events %v", response.Code, store.applied, store.events)
	}
	_ = server.planOffers(context.Background())
	if stripe.prices == pricesBefore {
		t.Fatal("the plan-card cache was not refreshed after a product update")
	}

	// A product with no subscriptions is acknowledged without applying anything.
	payload = `{"id":"evt_product_2","type":"product.updated","data":{"object":{"id":"prod_unused","object":"product"}}}`
	response = httptest.NewRecorder()
	server.stripeWebhook(response, signedWebhook(t, payload))
	if response.Code != http.StatusOK || len(store.applied) != 1 {
		t.Fatalf("unused product: status %d applied %d", response.Code, len(store.applied))
	}
}
