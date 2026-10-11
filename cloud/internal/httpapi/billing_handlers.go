package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/billing"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// StripeAPI is the part of the Stripe client the billing handlers use.
type StripeAPI interface {
	CreateCustomer(ctx context.Context, orgID, name string) (string, error)
	CreateCheckoutSession(ctx context.Context, input billing.CheckoutRequest) (string, error)
	CreatePortalSession(ctx context.Context, customerID, returnURL, configuration string) (string, error)
	GetSubscription(ctx context.Context, subscriptionID string) (billing.Subscription, error)
	GetPrice(ctx context.Context, priceID string) (billing.Price, error)
}

// BillingOptions configures Stripe billing. A nil *BillingOptions leaves
// billing off: the billing summary reports it disabled and the payment
// endpoints answer 404.
type BillingOptions struct {
	Stripe        StripeAPI
	WebhookSecret string
	// PriceIDs maps a plan name to its Stripe price.
	PriceIDs  map[string]string
	ReturnURL string
	// PortalConfiguration is the Customer Portal configuration (bpc_...) to
	// open; empty uses the account's default.
	PortalConfiguration string
}

// billingStore is the store surface billing needs; the Postgres store
// implements it.
type billingStore interface {
	BillingSummary(ctx context.Context, principal domain.Principal, orgID string) (domain.BillingSummary, error)
	OrgBilling(ctx context.Context, principal domain.Principal, orgID string) (domain.OrgBilling, error)
	RequireOrgAdmin(ctx context.Context, principal domain.Principal, orgID string) error
	OrgDisplayName(ctx context.Context, orgID string) (string, error)
	LinkStripeCustomer(ctx context.Context, orgID, customerID string) error
	OrgForStripeCustomer(ctx context.Context, customerID string) (string, bool, error)
	ApplySubscription(ctx context.Context, orgID string, update postgres.SubscriptionUpdate) error
	StripeEventSeen(ctx context.Context, eventID string) (bool, error)
	RecordStripeEvent(ctx context.Context, eventID, eventType string) error
	ResetWeeklyUsage(ctx context.Context, principal domain.Principal, orgID string) error
	WakeAllowed(ctx context.Context, principal domain.Principal, orgID, sessionID string) error
}

func (s *Server) billingReady() bool {
	return s.billingOptions != nil && s.billingStore != nil && s.billingOptions.Stripe != nil
}

// planOffer is one plan the organization can choose, priced from Stripe.
type planOffer struct {
	Name string `json:"name"`
	billing.Price
}

type billingSummaryResponse struct {
	domain.BillingSummary
	Plans []planOffer `json:"plans"`
}

// planOfferTTL bounds how stale a cached price may be: a price edited in the
// Stripe dashboard shows in the app within this long.
const planOfferTTL = 10 * time.Minute

type planOfferCache struct {
	mu      sync.Mutex
	offers  []planOffer
	fetched time.Time
}

// planOffers returns the configured plans priced from Stripe, cached. A plan
// whose price cannot be read is listed by name only rather than hidden.
func (s *Server) planOffers(ctx context.Context) []planOffer {
	cache := &s.offerCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.offers != nil && time.Since(cache.fetched) < planOfferTTL {
		return cache.offers
	}
	names := make([]string, 0, len(s.billingOptions.PriceIDs))
	for name := range s.billingOptions.PriceIDs {
		names = append(names, name)
	}
	sort.Strings(names)
	offers := make([]planOffer, 0, len(names))
	complete := true
	for _, name := range names {
		priceID := s.billingOptions.PriceIDs[name]
		price, err := s.billingOptions.Stripe.GetPrice(ctx, priceID)
		if err != nil {
			s.logger.Warn("read Stripe price", "plan", name, "price_id", priceID, "error", err)
			price, complete = billing.Price{ID: priceID}, false
		}
		offers = append(offers, planOffer{Name: name, Price: price})
	}
	sort.SliceStable(offers, func(i, j int) bool { return offers[i].UnitAmount < offers[j].UnitAmount })
	if complete {
		cache.offers, cache.fetched = offers, time.Now()
	}
	return offers
}

func (s *Server) getBilling(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if s.billingStore == nil {
		writeJSON(w, http.StatusOK, billingSummaryResponse{Plans: []planOffer{}})
		return
	}
	summary, err := s.billingStore.BillingSummary(r.Context(), principalFrom(r), orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	plans := []planOffer{}
	if s.billingReady() {
		plans = s.planOffers(r.Context())
	} else {
		summary.Enabled = false
	}
	writeJSON(w, http.StatusOK, billingSummaryResponse{BillingSummary: summary, Plans: plans})
}

func (s *Server) createBillingCheckout(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if !s.billingReady() {
		writeError(w, r, http.StatusNotFound, "BILLING_DISABLED", "Billing is not enabled on this deployment.")
		return
	}
	var input struct {
		Plan string `json:"plan"`
	}
	if err := decodeJSONLimit(w, r, &input, 4<<10); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	price, ok := s.billingOptions.PriceIDs[input.Plan]
	if !ok {
		writeError(w, r, http.StatusUnprocessableEntity, "UNKNOWN_PLAN", "Choose one of the available plans.")
		return
	}
	principal := principalFrom(r)
	if err := s.billingStore.RequireOrgAdmin(r.Context(), principal, orgID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	state, err := s.billingStore.OrgBilling(r.Context(), principal, orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// A second Checkout would start a second subscription; plan changes go
	// through the Customer Portal instead.
	if state.StripeSubscriptionID != "" && subscriptionLive(state.SubscriptionStatus) {
		writeError(w, r, http.StatusConflict, "ALREADY_SUBSCRIBED",
			"This organization already has a subscription. Use Manage billing to change plans.")
		return
	}
	customerID := state.StripeCustomerID
	if customerID == "" {
		name, err := s.billingStore.OrgDisplayName(r.Context(), orgID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		customerID, err = s.billingOptions.Stripe.CreateCustomer(r.Context(), orgID, name)
		if err != nil {
			s.writeBillingProviderError(w, r, "create Stripe customer", err)
			return
		}
		if err := s.billingStore.LinkStripeCustomer(r.Context(), orgID, customerID); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	link, err := s.billingOptions.Stripe.CreateCheckoutSession(r.Context(), billing.CheckoutRequest{
		OrgID:      orgID,
		CustomerID: customerID,
		PriceID:    price,
		SuccessURL: s.billingOptions.ReturnURL + "?checkout=success",
		CancelURL:  s.billingOptions.ReturnURL + "?checkout=cancelled",
	})
	if err != nil {
		s.writeBillingProviderError(w, r, "create Stripe checkout session", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": link})
}

func subscriptionLive(status string) bool {
	switch status {
	case "active", "trialing", "past_due", "unpaid", "incomplete":
		return true
	}
	return false
}

func (s *Server) createBillingPortal(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if !s.billingReady() {
		writeError(w, r, http.StatusNotFound, "BILLING_DISABLED", "Billing is not enabled on this deployment.")
		return
	}
	principal := principalFrom(r)
	if err := s.billingStore.RequireOrgAdmin(r.Context(), principal, orgID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	state, err := s.billingStore.OrgBilling(r.Context(), principal, orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if state.StripeCustomerID == "" {
		writeError(w, r, http.StatusConflict, "NO_SUBSCRIPTION", "Choose a plan first.")
		return
	}
	link, err := s.billingOptions.Stripe.CreatePortalSession(
		r.Context(), state.StripeCustomerID, s.billingOptions.ReturnURL, s.billingOptions.PortalConfiguration)
	if err != nil {
		s.writeBillingProviderError(w, r, "create Stripe portal session", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": link})
}

func (s *Server) resetBillingUsage(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if !s.billingReady() {
		writeError(w, r, http.StatusNotFound, "BILLING_DISABLED", "Billing is not enabled on this deployment.")
		return
	}
	principal := principalFrom(r)
	if err := s.billingStore.ResetWeeklyUsage(r.Context(), principal, orgID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	summary, err := s.billingStore.BillingSummary(r.Context(), principal, orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// stripeWebhook applies subscription changes. Every handled event re-reads
// the subscription from Stripe, so duplicates and out-of-order deliveries
// converge on Stripe's current state. A transient failure answers 5xx and
// Stripe retries.
func (s *Server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.billingReady() {
		writeError(w, r, http.StatusNotFound, "BILLING_DISABLED", "Billing is not enabled on this deployment.")
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body could not be read.")
		return
	}
	event, err := billing.VerifyWebhook(payload, r.Header.Get("Stripe-Signature"), s.billingOptions.WebhookSecret, time.Now())
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_SIGNATURE", "The webhook signature is invalid.")
		return
	}
	ctx := r.Context()
	seen, err := s.billingStore.StripeEventSeen(ctx, event.ID)
	if err != nil {
		s.writeWebhookFailure(w, r, event, "check event", err)
		return
	}
	if seen {
		w.WriteHeader(http.StatusOK)
		return
	}
	object := event.Data.Object
	subscriptionID := ""
	switch event.Type {
	case "checkout.session.completed":
		if object.ClientReferenceID != "" && object.Customer != "" {
			if err := s.billingStore.LinkStripeCustomer(ctx, object.ClientReferenceID, object.Customer); err != nil {
				s.writeWebhookFailure(w, r, event, "link customer", err)
				return
			}
		}
		subscriptionID = object.Subscription
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		subscriptionID = object.ID
	case "invoice.paid", "invoice.payment_failed":
		subscriptionID = object.Subscription
	}
	if subscriptionID != "" {
		if err := s.syncSubscription(ctx, subscriptionID); err != nil {
			s.writeWebhookFailure(w, r, event, "sync subscription", err)
			return
		}
	}
	if err := s.billingStore.RecordStripeEvent(ctx, event.ID, event.Type); err != nil {
		s.writeWebhookFailure(w, r, event, "record event", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) syncSubscription(ctx context.Context, subscriptionID string) error {
	subscription, err := s.billingOptions.Stripe.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return err
	}
	orgID, found, err := s.billingStore.OrgForStripeCustomer(ctx, subscription.CustomerID)
	if err != nil {
		return err
	}
	if !found {
		// The customer was created outside Checkout (or its link was lost):
		// fall back to the organization stamped on the subscription.
		if subscription.OrgID == "" || requireUUID(subscription.OrgID, "orgId") != nil {
			s.logger.Warn("Stripe subscription for an unknown customer",
				"subscription_id", subscription.ID, "customer_id", subscription.CustomerID)
			return nil
		}
		if err := s.billingStore.LinkStripeCustomer(ctx, subscription.OrgID, subscription.CustomerID); err != nil {
			return err
		}
		orgID = subscription.OrgID
	}
	if subscription.LimitsErr != nil {
		// Keep the last good limits rather than locking the customer out over
		// a dashboard mistake, and make it loud.
		s.logger.Error("Stripe product metadata is not a valid AO plan; keeping previous limits",
			"subscription_id", subscription.ID, "org_id", orgID, "error", subscription.LimitsErr)
	}
	return s.billingStore.ApplySubscription(ctx, orgID, postgres.SubscriptionUpdate{
		SubscriptionID: subscription.ID,
		Status:         subscription.Status,
		Limits:         subscription.Limits,
		PeriodStart:    subscription.PeriodStart,
		PeriodEnd:      subscription.PeriodEnd,
	})
}

func (s *Server) writeWebhookFailure(w http.ResponseWriter, r *http.Request, event billing.Event, step string, err error) {
	s.logger.Error("Stripe webhook failed", "step", step, "event_id", event.ID, "event_type", event.Type,
		"error", err, "request_id", requestID(r))
	writeError(w, r, http.StatusInternalServerError, "internal_error", "The webhook could not be processed.")
}

func (s *Server) writeBillingProviderError(w http.ResponseWriter, r *http.Request, step string, err error) {
	s.logger.Error("Stripe request failed", "step", step, "error", err, "request_id", requestID(r))
	writeError(w, r, http.StatusBadGateway, "BILLING_PROVIDER_ERROR", "The payment provider could not be reached. Try again.")
}

// billingReturn is the page Checkout and the Customer Portal send the browser
// back to. The desktop app refreshes the plan itself; this only tells the
// user they can go back.
func (s *Server) billingReturn(w http.ResponseWriter, r *http.Request) {
	message := "You can close this tab and return to Agent Orchestrator."
	switch r.URL.Query().Get("checkout") {
	case "success":
		message = "Payment complete. You can close this tab and return to Agent Orchestrator."
	case "cancelled":
		message = "Checkout was cancelled. You can close this tab and return to Agent Orchestrator."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Agent Orchestrator</title>`+
		`<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#111;color:#eee}p{max-width:28rem;padding:1rem;text-align:center;line-height:1.5}</style>`+
		`</head><body><p>`+message+`</p></body></html>`)
}

// writePlanError maps billing refusals to responses the desktop app can act
// on. It reports whether err was a billing refusal.
func writePlanError(w http.ResponseWriter, r *http.Request, err error) bool {
	var limitErr *postgres.PlanLimitError
	switch {
	case errors.Is(err, postgres.ErrPlanRequired):
		writeError(w, r, http.StatusPaymentRequired, "PLAN_REQUIRED", "Choose a plan to use AO Cloud.")
		return true
	case errors.Is(err, postgres.ErrNoResetsLeft):
		writeError(w, r, http.StatusConflict, "NO_RESETS_LEFT", "There are no manual resets left this billing period.")
		return true
	case errors.As(err, &limitErr):
		// Not 403: the desktop client reads a 403 as a stale organization
		// membership and re-resolves the org. A full slot is a conflict with
		// existing sessions; a usage limit is a rate limit that lifts on its own.
		status := http.StatusConflict
		if limitErr.Code == postgres.LimitWindow || limitErr.Code == postgres.LimitWeekly {
			status = http.StatusTooManyRequests
			if limitErr.ResetsAt != nil {
				if wait := time.Until(*limitErr.ResetsAt); wait > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				}
			}
		}
		writeJSON(w, status, planLimitEnvelope{
			Error:     http.StatusText(status),
			Code:      limitErr.Code,
			Message:   planLimitMessage(limitErr.Code),
			RequestID: requestID(r),
			Plan:      limitErr.Plan,
			Limit:     limitErr.Limit,
			Used:      limitErr.Used,
			ResetsAt:  limitErr.ResetsAt,
		})
		return true
	}
	return false
}

type planLimitEnvelope struct {
	Error     string     `json:"error"`
	Code      string     `json:"code"`
	Message   string     `json:"message"`
	RequestID string     `json:"requestId,omitempty"`
	Plan      string     `json:"plan"`
	Limit     int        `json:"limit"`
	Used      int        `json:"used"`
	ResetsAt  *time.Time `json:"resetsAt,omitempty"`
}

func planLimitMessage(code string) string {
	switch code {
	case postgres.LimitOrchestratorSlots:
		return "Your plan's orchestrator is already running. Stop it to start another."
	case postgres.LimitWorkerSlots:
		return "All your plan's running sessions are in use. Pause or archive one, or upgrade your plan."
	case postgres.LimitTotalSessions:
		return "You've reached your plan's total sessions. Archive one or upgrade your plan."
	case postgres.LimitWindow:
		return "You've reached your plan's usage limit for now."
	case postgres.LimitWeekly:
		return "You've reached your plan's weekly usage limit."
	}
	return "Your plan's limit has been reached."
}

// presenceStore records that a person is looking at a session.
type presenceStore interface {
	TouchSessionPresence(ctx context.Context, principal domain.Principal, orgID, sessionID string) error
}

// touchSessionPresence is the desktop app's heartbeat while a session is on
// screen, so reading an agent's output counts as using the session and it is
// not idle-paused under the reader.
func (s *Server) touchSessionPresence(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	store, ok := s.store.(presenceStore)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := store.TouchSessionPresence(r.Context(), principalFrom(r), orgID, sessionID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
