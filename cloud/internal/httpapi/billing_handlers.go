package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/billing"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// StripeAPI is the part of the Stripe client the billing handlers use.
type StripeAPI interface {
	CreateCustomer(ctx context.Context, orgID, name string) (string, error)
	CreateCheckoutSession(ctx context.Context, input billing.CheckoutRequest) (billing.CheckoutSession, error)
	ExpireCheckoutSession(ctx context.Context, sessionID string) (string, error)
	CreatePortalSession(ctx context.Context, customerID, returnURL, configuration, cancelSubscriptionID string) (string, error)
	GetSubscription(ctx context.Context, subscriptionID string) (billing.Subscription, error)
	GetPrice(ctx context.Context, priceID string) (billing.Price, error)
	ProductSubscriptions(ctx context.Context, productID string) ([]string, error)
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
	SubscriptionCancelAt(ctx context.Context, principal domain.Principal, orgID string) (*time.Time, error)
	OrgBilling(ctx context.Context, principal domain.Principal, orgID string) (domain.OrgBilling, error)
	RequireOrgAdmin(ctx context.Context, principal domain.Principal, orgID string) error
	OrgDisplayName(ctx context.Context, orgID string) (string, error)
	LinkStripeCustomer(ctx context.Context, orgID, customerID string) error
	OrgForStripeCustomer(ctx context.Context, customerID string) (string, bool, error)
	ApplySubscriptionLocked(ctx context.Context, orgID string,
		fetch func(ctx context.Context, current domain.OrgBilling) (postgres.SubscriptionUpdate, error),
	) (postgres.SubscriptionOutcome, error)
	StartCheckout(ctx context.Context, principal domain.Principal, orgID string,
		start func(ctx context.Context, state domain.OrgBilling) (postgres.CheckoutStart, error),
	) (*domain.PendingCheckout, error)
	ClearPendingCheckout(ctx context.Context, orgID, checkoutID string) error
	StripeEventSeen(ctx context.Context, eventID string) (bool, error)
	RecordStripeEvent(ctx context.Context, eventID, eventType string) error
	ResetWeeklyUsage(ctx context.Context, principal domain.Principal, orgID string) error
	WakeAllowed(ctx context.Context, principal domain.Principal, orgID, sessionID string) error
	OrgHasFeature(ctx context.Context, orgID, feature string) bool
}

func (s *Server) billingReady() bool {
	return s.billingOptions != nil && s.billingStore != nil && s.billingOptions.Stripe != nil
}

// orgHasFeature reports whether the organization may use a paywalled feature
// (see postgres.Store.OrgHasFeature). Without billing every feature is
// available.
func (s *Server) orgHasFeature(ctx context.Context, orgID, feature string) bool {
	if s.billingStore == nil {
		return true
	}
	return s.billingStore.OrgHasFeature(ctx, orgID, feature)
}

// orgOffersBilledProvider reports whether the principal's organization may
// run sessions on a billed provider: Freestyle, or Coder for an organization
// without the coder capability (see postgres sandboxBilledTx). An organization
// offered only unbilled providers has nothing to buy, so plans do not apply.
func (s *Server) orgOffersBilledProvider(principal domain.Principal, orgID string) bool {
	providers := s.providersForOrg(principal, orgID)
	if slices.Contains(providers, sandbox.ProviderFreestyle) {
		return true
	}
	return slices.Contains(providers, sandbox.ProviderCoder) && !s.orgHasCoderCapability(principal, orgID)
}

// planOffer is one plan the organization can choose, priced from Stripe.
type planOffer struct {
	Name string `json:"name"`
	billing.Price
}

type billingSummaryResponse struct {
	domain.BillingSummary
	// CancelAt is when a subscription set to cancel ends and the organization
	// drops to Free.
	CancelAt *time.Time  `json:"cancelAt,omitempty"`
	Plans    []planOffer `json:"plans"`
}

const (
	// planOfferTTL bounds how stale a cached price may be: a price edited in
	// the Stripe dashboard shows in the app within this long.
	planOfferTTL = 10 * time.Minute
	// planOfferRetryTTL is how long a listing with a price Stripe failed to
	// return is served before Stripe is asked again, so an outage does not
	// put a Stripe call on every billing screen load.
	planOfferRetryTTL = time.Minute
	// planOfferFetchTimeout bounds one refresh of every price.
	planOfferFetchTimeout = 30 * time.Second
)

type planOfferCache struct {
	mu       sync.Mutex
	offers   []planOffer
	fetched  time.Time
	complete bool
	// inflight is closed when the refresh under way finishes.
	inflight chan struct{}
}

// planOffers returns the configured plans priced from Stripe, cached. A plan
// whose price cannot be read is listed by name only rather than hidden. One
// request refreshes the cache at a time, without holding the cache lock
// while it waits on Stripe; others are served the previous listing, or wait
// for the first one.
func (s *Server) planOffers(ctx context.Context) []planOffer {
	cache := &s.offerCache
	for {
		cache.mu.Lock()
		if cache.offers != nil {
			ttl := planOfferTTL
			if !cache.complete {
				ttl = planOfferRetryTTL
			}
			if time.Since(cache.fetched) < ttl {
				offers := cache.offers
				cache.mu.Unlock()
				return offers
			}
		}
		if wait := cache.inflight; wait != nil {
			stale := cache.offers
			cache.mu.Unlock()
			if stale != nil {
				return stale
			}
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return s.unpricedPlanOffers()
			}
		}
		done := make(chan struct{})
		cache.inflight = done
		cache.mu.Unlock()

		// The refresh outlives a caller that gives up: others may be waiting.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), planOfferFetchTimeout)
		offers, complete := s.fetchPlanOffers(fetchCtx)
		cancel()

		cache.mu.Lock()
		cache.offers, cache.fetched, cache.complete, cache.inflight = offers, time.Now(), complete, nil
		cache.mu.Unlock()
		close(done)
		return offers
	}
}

// fetchPlanOffers prices every configured plan from Stripe, cheapest first.
func (s *Server) fetchPlanOffers(ctx context.Context) ([]planOffer, bool) {
	offers := s.unpricedPlanOffers()
	complete := true
	for index := range offers {
		price, err := s.billingOptions.Stripe.GetPrice(ctx, offers[index].ID)
		if err != nil {
			s.logger.Warn("read Stripe price", "plan", offers[index].Name, "price_id", offers[index].ID, "error", err)
			complete = false
			continue
		}
		offers[index].Price = price
	}
	sort.SliceStable(offers, func(i, j int) bool { return offers[i].UnitAmount < offers[j].UnitAmount })
	return offers, complete
}

// unpricedPlanOffers lists the configured plans by name, without prices.
func (s *Server) unpricedPlanOffers() []planOffer {
	names := make([]string, 0, len(s.billingOptions.PriceIDs))
	for name := range s.billingOptions.PriceIDs {
		names = append(names, name)
	}
	sort.Strings(names)
	offers := make([]planOffer, 0, len(names))
	for _, name := range names {
		offers = append(offers, planOffer{Name: name, Price: billing.Price{ID: s.billingOptions.PriceIDs[name]}})
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
		writeJSON(w, http.StatusOK, billingSummaryResponse{
			BillingSummary: domain.BillingSummary{BillingMode: domain.BillingModeOff, Features: []string{}},
			Plans:          []planOffer{},
		})
		return
	}
	principal := principalFrom(r)
	summary, err := s.billingStore.BillingSummary(r.Context(), principal, orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	cancelAt, err := s.billingStore.SubscriptionCancelAt(r.Context(), principal, orgID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if summary.Features == nil {
		summary.Features = []string{}
	}
	switch {
	case !s.billingReady():
		summary.Enabled = false
		summary.BillingMode = domain.BillingModeOff
	case summary.BillingMode == domain.BillingModeSelfServe && !s.orgOffersBilledProvider(principal, orgID):
		// Nothing this organization runs is billed: no plans to show.
		summary.BillingMode = domain.BillingModeOff
	}
	plans := []planOffer{}
	if summary.BillingMode == domain.BillingModeSelfServe {
		plans = s.planOffers(r.Context())
	}
	writeJSON(w, http.StatusOK, billingSummaryResponse{BillingSummary: summary, CancelAt: cancelAt, Plans: plans})
}

// checkoutSessionTTL is how long a Checkout link stays payable. While it is
// open the organization's admins are handed the same link again rather than
// a second Checkout, which could start a second subscription.
const checkoutSessionTTL = time.Hour

var (
	errAlreadySubscribed = errors.New("organization already has a subscription")
	errCheckoutCompleted = errors.New("organization's checkout just completed")
)

// stripeCallError is a failed call to Stripe made while starting a Checkout.
type stripeCallError struct {
	step string
	err  error
}

func (e *stripeCallError) Error() string { return e.step + ": " + e.err.Error() }
func (e *stripeCallError) Unwrap() error { return e.err }

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
	stripe := s.billingOptions.Stripe
	now := time.Now()
	// Runs under the organization's lock: concurrent requests see the
	// Checkout the first one starts and are handed the same link.
	pending, err := s.billingStore.StartCheckout(r.Context(), principalFrom(r), orgID,
		func(ctx context.Context, state domain.OrgBilling) (postgres.CheckoutStart, error) {
			var start postgres.CheckoutStart
			// A second Checkout would start a second subscription; plan
			// changes go through the Customer Portal instead.
			if state.StripeSubscriptionID != "" && subscriptionLive(state.SubscriptionStatus) {
				return start, errAlreadySubscribed
			}
			if open := state.PendingCheckout; open.Open(now) {
				if open.PriceID == price {
					start.Pending = open
					return start, nil
				}
				// Another plan: close the open Checkout first, so only one
				// can ever be paid.
				status, err := stripe.ExpireCheckoutSession(ctx, open.ID)
				if err != nil {
					return start, &stripeCallError{"expire Stripe checkout session", err}
				}
				if status == "complete" {
					return start, errCheckoutCompleted
				}
				start.ClearPending = true
			}
			customerID := state.StripeCustomerID
			if customerID == "" {
				name, err := s.billingStore.OrgDisplayName(ctx, orgID)
				if err != nil {
					return start, err
				}
				if customerID, err = stripe.CreateCustomer(ctx, orgID, name); err != nil {
					return start, &stripeCallError{"create Stripe customer", err}
				}
				start.CustomerID = customerID
			}
			session, err := stripe.CreateCheckoutSession(ctx, billing.CheckoutRequest{
				OrgID:      orgID,
				CustomerID: customerID,
				PriceID:    price,
				SuccessURL: s.billingOptions.ReturnURL + "?checkout=success",
				CancelURL:  s.billingOptions.ReturnURL + "?checkout=cancelled",
				ExpiresAt:  now.Add(checkoutSessionTTL),
			})
			if err != nil {
				return start, &stripeCallError{"create Stripe checkout session", err}
			}
			start.Pending = &domain.PendingCheckout{
				ID: session.ID, URL: session.URL, PriceID: price, ExpiresAt: session.ExpiresAt,
			}
			start.ClearPending = false
			return start, nil
		})
	var callErr *stripeCallError
	switch {
	case errors.Is(err, errAlreadySubscribed):
		writeError(w, r, http.StatusConflict, "ALREADY_SUBSCRIBED",
			"This organization already has a subscription. Use Manage billing to change plans.")
	case errors.Is(err, errCheckoutCompleted):
		writeError(w, r, http.StatusConflict, "CHECKOUT_COMPLETED",
			"A checkout for this organization was just paid. Your plan will appear in a moment.")
	case errors.As(err, &callErr):
		s.writeBillingProviderError(w, r, callErr.step, callErr.err)
	case err != nil:
		s.writeStoreError(w, r, err)
	case pending == nil:
		s.writeStoreError(w, r, errors.New("checkout started without a session"))
	default:
		s.recordActivity(r, domain.ActivityEvent{OrgID: orgID, Kind: domain.ActivityCheckoutStarted,
			Attrs: map[string]any{"plan": input.Plan}})
		writeJSON(w, http.StatusOK, map[string]string{"url": pending.URL})
	}
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
	// {"flow":"cancel"} opens the portal straight on cancelling the plan
	// (downgrading to Free, which has no Stripe price to switch to).
	var body struct {
		Flow string `json:"flow"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSONLimit(w, r, &body, 4<<10); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	cancelSubscriptionID := ""
	switch body.Flow {
	case "":
	case "cancel":
		if state.StripeSubscriptionID == "" || !subscriptionLive(state.SubscriptionStatus) {
			writeError(w, r, http.StatusConflict, "NO_SUBSCRIPTION", "There is no plan to cancel.")
			return
		}
		// Stripe refuses a cancel flow for a plan already set to cancel, and
		// a webhook may not have told us yet. Read the subscription first and
		// store it, so the app learns when the plan ends; an already
		// cancelling plan opens the portal home, where it can be renewed.
		subscription, err := s.billingOptions.Stripe.GetSubscription(r.Context(), state.StripeSubscriptionID)
		if err != nil {
			s.writeBillingProviderError(w, r, "read Stripe subscription", err)
			return
		}
		// Store it through the same locked flow as a webhook (a synthetic
		// event: nothing to dedupe), so a missed webhook cannot leave the
		// app unaware the plan is ending.
		if err := s.syncSubscription(r.Context(), billing.Event{Type: "portal.cancel_check"},
			state.StripeSubscriptionID, state.StripeCustomerID); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		if subscription.CancelAt == nil {
			cancelSubscriptionID = state.StripeSubscriptionID
		}
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_request", "flow must be cancel or empty.")
		return
	}
	link, err := s.billingOptions.Stripe.CreatePortalSession(r.Context(), state.StripeCustomerID,
		s.billingOptions.ReturnURL, s.billingOptions.PortalConfiguration, cancelSubscriptionID)
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
// Stripe retries; an event for an organization that does not exist (any
// more) is acknowledged, so Stripe stops retrying it.
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
	if step, err := s.handleStripeEvent(ctx, event); err != nil {
		s.writeWebhookFailure(w, r, event, step, err)
		return
	}
	if err := s.billingStore.RecordStripeEvent(ctx, event.ID, event.Type); err != nil {
		s.writeWebhookFailure(w, r, event, "record event", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleStripeEvent applies one verified event. It returns the failed step
// with an error Stripe should retry.
func (s *Server) handleStripeEvent(ctx context.Context, event billing.Event) (string, error) {
	object := event.Data.Object
	switch event.Type {
	case "checkout.session.completed", "checkout.session.expired":
		orgID, err := s.checkoutOrg(ctx, event)
		if err != nil {
			return "find checkout organization", err
		}
		if orgID == "" {
			return "", nil
		}
		if event.Type == "checkout.session.completed" && object.Customer != "" {
			if linked, err := s.linkWebhookCustomer(ctx, event, orgID, object.Customer); err != nil || !linked {
				return "link customer", err
			}
		}
		if err := s.billingStore.ClearPendingCheckout(ctx, orgID, object.ID); err != nil {
			return "clear pending checkout", err
		}
		if event.Type == "checkout.session.completed" {
			s.recordCheckoutCompleted(ctx, orgID)
		}
		if event.Type == "checkout.session.completed" && object.Subscription != "" {
			return "sync subscription", s.syncSubscription(ctx, event, object.Subscription, object.Customer)
		}
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		return "sync subscription", s.syncSubscription(ctx, event, object.ID, object.Customer)
	case "invoice.paid", "invoice.payment_failed":
		if subscriptionID := event.InvoiceSubscription(); subscriptionID != "" {
			return "sync subscription", s.syncSubscription(ctx, event, subscriptionID, object.Customer)
		}
	case "product.updated":
		return s.syncProduct(ctx, event, object.ID)
	}
	return "", nil
}

// syncProduct applies a plan product's edited metadata (its plan limits) to
// every subscription on it right away, instead of at each one's next renewal,
// and drops the cached plan prices so the plan cards show the change too. A
// failure retries the whole event; re-reading a subscription is idempotent.
func (s *Server) syncProduct(ctx context.Context, event billing.Event, productID string) (string, error) {
	s.offerCache.mu.Lock()
	s.offerCache.offers = nil
	s.offerCache.mu.Unlock()
	if productID == "" {
		return "", nil
	}
	subscriptionIDs, err := s.billingOptions.Stripe.ProductSubscriptions(ctx, productID)
	if err != nil {
		return "list product subscriptions", err
	}
	for _, subscriptionID := range subscriptionIDs {
		if err := s.syncSubscription(ctx, event, subscriptionID, ""); err != nil {
			return "sync product subscription", err
		}
	}
	if len(subscriptionIDs) > 0 {
		s.logger.Info("applied Stripe product update to its subscriptions",
			"product_id", productID, "subscriptions", len(subscriptionIDs))
	}
	return "", nil
}

// checkoutOrg is the organization a Checkout event belongs to: the
// client_reference_id the control plane set, or the organization of its
// customer. Empty (and no error) when there is none, which is acknowledged.
func (s *Server) checkoutOrg(ctx context.Context, event billing.Event) (string, error) {
	object := event.Data.Object
	if reference := object.ClientReferenceID; reference != "" {
		if requireUUID(reference, "client_reference_id") == nil {
			return reference, nil
		}
		s.logger.Warn("Stripe checkout with a client_reference_id that is not an organization",
			"event_id", event.ID, "checkout_id", object.ID, "client_reference_id", reference)
	}
	if object.Customer == "" {
		return "", nil
	}
	orgID, found, err := s.billingStore.OrgForStripeCustomer(ctx, object.Customer)
	if err != nil || !found {
		if err == nil {
			s.logger.Warn("Stripe checkout for an unknown customer", "event_id", event.ID,
				"checkout_id", object.ID, "customer_id", object.Customer)
		}
		return "", err
	}
	return orgID, nil
}

// linkWebhookCustomer links a webhook's customer to its organization. It
// reports false, without an error, when the organization is gone or already
// has another customer: retrying would never succeed, so the event is
// acknowledged and the conflict logged for a person to look at.
func (s *Server) linkWebhookCustomer(ctx context.Context, event billing.Event, orgID, customerID string) (bool, error) {
	err := s.billingStore.LinkStripeCustomer(ctx, orgID, customerID)
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		s.logger.Warn("Stripe event for an organization that does not exist", "event_id", event.ID,
			"event_type", event.Type, "org_id", orgID, "customer_id", customerID)
		return false, nil
	case errors.Is(err, postgres.ErrConflict):
		s.logger.Error("Stripe customer conflicts with the organization's customer; not linked",
			"event_id", event.ID, "event_type", event.Type, "org_id", orgID, "customer_id", customerID, "error", err)
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

// syncSubscription re-reads a subscription from Stripe and stores it on its
// organization. The read happens under the organization's lock, so
// concurrent events for one organization store Stripe's state in order.
func (s *Server) syncSubscription(ctx context.Context, event billing.Event, subscriptionID, customerHint string) error {
	stripe := s.billingOptions.Stripe
	orgID := ""
	if customerHint != "" {
		mapped, found, err := s.billingStore.OrgForStripeCustomer(ctx, customerHint)
		if err != nil {
			return err
		}
		if found {
			orgID = mapped
		}
	}
	if orgID == "" {
		subscription, err := stripe.GetSubscription(ctx, subscriptionID)
		if err != nil {
			return err
		}
		mapped, found, err := s.billingStore.OrgForStripeCustomer(ctx, subscription.CustomerID)
		if err != nil {
			return err
		}
		if found {
			orgID = mapped
		} else {
			// The customer was created outside Checkout (or its link was
			// lost): fall back to the organization stamped on the
			// subscription.
			if subscription.OrgID == "" || requireUUID(subscription.OrgID, "orgId") != nil {
				s.logger.Warn("Stripe subscription for an unknown customer", "event_id", event.ID,
					"subscription_id", subscription.ID, "customer_id", subscription.CustomerID)
				return nil
			}
			if linked, err := s.linkWebhookCustomer(ctx, event, subscription.OrgID, subscription.CustomerID); err != nil || !linked {
				return err
			}
			orgID = subscription.OrgID
		}
	}
	var appliedStatus string
	var appliedLimits *domain.PlanLimits
	outcome, err := s.billingStore.ApplySubscriptionLocked(ctx, orgID,
		func(ctx context.Context, current domain.OrgBilling) (postgres.SubscriptionUpdate, error) {
			subscription, err := stripe.GetSubscription(ctx, subscriptionID)
			if err != nil {
				return postgres.SubscriptionUpdate{}, err
			}
			if subscription.LimitsErr != nil {
				// Keep the last good limits rather than locking the customer
				// out over a dashboard mistake, and make it loud.
				s.logger.Error("Stripe product metadata is not a valid AO plan; keeping previous limits",
					"subscription_id", subscription.ID, "org_id", orgID, "error", subscription.LimitsErr)
			}
			appliedStatus, appliedLimits = subscription.Status, subscription.Limits
			return postgres.SubscriptionUpdate{
				SubscriptionID: subscription.ID,
				CustomerID:     subscription.CustomerID,
				Status:         subscription.Status,
				Limits:         subscription.Limits,
				PeriodStart:    subscription.PeriodStart,
				PeriodEnd:      subscription.PeriodEnd,
				CancelAt:       subscription.CancelAt,
				Created:        subscription.Created,
			}, nil
		})
	if errors.Is(err, postgres.ErrNotFound) {
		s.logger.Warn("Stripe subscription for an organization that does not exist", "event_id", event.ID,
			"subscription_id", subscriptionID, "org_id", orgID)
		return nil
	}
	if err != nil {
		return err
	}
	if outcome == postgres.SubscriptionApplied || outcome == postgres.SubscriptionReplaced {
		s.recordPlanChanged(ctx, orgID, appliedStatus, appliedLimits)
	}
	switch {
	case outcome.Duplicate():
		s.logger.Error("organization has two live Stripe subscriptions; keeping the newest. Cancel and refund the other one.",
			"event_id", event.ID, "org_id", orgID, "subscription_id", subscriptionID, "outcome", string(outcome))
	case outcome != postgres.SubscriptionApplied && outcome != postgres.SubscriptionReplaced:
		s.logger.Info("ignored a Stripe subscription that is not the organization's", "event_id", event.ID,
			"org_id", orgID, "subscription_id", subscriptionID, "outcome", string(outcome))
	}
	return nil
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
// back to. It hands off to the desktop app through the ao-app://billing deep
// link, which opens Settings on Billing; the app also re-reads the plan when
// its window regains focus, so a build that cannot receive the deep link (a
// development build) still shows the new plan.
func (s *Server) billingReturn(w http.ResponseWriter, r *http.Request) {
	outcome := r.URL.Query().Get("checkout")
	message := "You can return to Agent Orchestrator."
	switch outcome {
	case "success":
		message = "Payment complete. Returning you to Agent Orchestrator."
	case "cancelled":
		message = "Checkout was cancelled. Returning you to Agent Orchestrator."
	default:
		outcome = ""
	}
	target := "ao-app://billing"
	if outcome != "" {
		target += "?checkout=" + outcome
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8">`+
		`<meta name="viewport" content="width=device-width,initial-scale=1">`+
		`<meta http-equiv="refresh" content="0;url=`+target+`">`+
		`<title>Agent Orchestrator</title>`+
		`<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#111;color:#eee}`+
		`main{max-width:28rem;padding:1rem;text-align:center;line-height:1.5}`+
		`a{display:inline-block;margin-top:1rem;padding:.5rem 1rem;border-radius:6px;background:#3b82f6;color:#fff;text-decoration:none}</style>`+
		`</head><body><main><p>`+message+`</p><a href="`+target+`">Open Agent Orchestrator</a></main></body></html>`)
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

// presenceLimiter drops presence heartbeats for a session already touched
// within postgres.PresenceInterval, so open windows and fast clients cost one
// store write per session and interval.
type presenceLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// allow reports whether a heartbeat for key may reach the store at now.
func (l *presenceLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if previous, ok := l.last[key]; ok && now.Sub(previous) < postgres.PresenceInterval {
		return false
	}
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if len(l.last) >= 10_000 {
		for stale, at := range l.last {
			if now.Sub(at) >= postgres.PresenceInterval {
				delete(l.last, stale)
			}
		}
	}
	l.last[key] = now
	return true
}

// forget lets the next heartbeat for key through, after one that failed.
func (l *presenceLimiter) forget(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.last, key)
}

// touchSessionPresence is the desktop app's heartbeat while a session is on
// screen, so reading an agent's output counts as using the session and it is
// not idle-paused under the reader. Viewers of a shared session do not keep
// it awake (the store ignores them).
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
	key := orgID + "/" + sessionID
	if !s.presenceLimiter.allow(key, time.Now()) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := store.TouchSessionPresence(r.Context(), principalFrom(r), orgID, sessionID); err != nil {
		s.presenceLimiter.forget(key)
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
