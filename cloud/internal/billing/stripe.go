// Package billing talks to Stripe for AO Cloud subscriptions: creating
// customers, Checkout and Customer Portal sessions, reading a subscription
// with its plan limits, and verifying webhook signatures.
//
// It uses the standard library rather than the Stripe SDK: the control plane
// needs six calls, and a small client keeps the dependency surface narrow.
package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

const defaultBaseURL = "https://api.stripe.com"

// APIVersion pins the Stripe API version every request is made with, so a
// change to the account's default version cannot change the shapes this
// client reads. 2025-03-31.basil reports the billing period on subscription
// items and an invoice's subscription under parent.subscription_details;
// GetSubscription and Event.InvoiceSubscription read those (and the older
// shapes, for webhook endpoints still on an earlier version).
const APIVersion = "2025-03-31.basil"

// Client is a minimal Stripe API client authenticated with a (restricted)
// secret key.
type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

// NewClient creates a Client. baseURL may be empty for the live Stripe API.
func NewClient(key, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		key:     key,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// APIError is a non-2xx Stripe response.
type APIError struct {
	Status  int
	Type    string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("stripe %d %s %s: %s", e.Status, e.Type, e.Code, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values, idempotencyKey string, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.SetBasicAuth(c.key, "")
	request.Header.Set("Stripe-Version", APIVersion)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("stripe %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var envelope struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		return &APIError{
			Status: response.StatusCode, Type: envelope.Error.Type,
			Code: envelope.Error.Code, Message: envelope.Error.Message,
		}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// CreateCustomer creates a customer for an organization. The idempotency key
// makes a retried call return the same customer instead of a second one.
func (c *Client) CreateCustomer(ctx context.Context, orgID, name string) (string, error) {
	form := url.Values{}
	form.Set("metadata[ao_org_id]", orgID)
	if name != "" {
		form.Set("name", name)
	}
	var customer struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/customers", form, "ao-customer-"+orgID, &customer); err != nil {
		return "", err
	}
	if customer.ID == "" {
		return "", errors.New("stripe returned a customer without an id")
	}
	return customer.ID, nil
}

// CheckoutRequest describes a subscription Checkout session.
type CheckoutRequest struct {
	OrgID      string
	CustomerID string
	PriceID    string
	SuccessURL string
	CancelURL  string
	// ExpiresAt bounds how long the session stays open (Stripe allows 30
	// minutes to 24 hours). Zero keeps Stripe's default of 24 hours.
	ExpiresAt time.Time
}

// CheckoutSession is a created Checkout session.
type CheckoutSession struct {
	ID        string
	URL       string
	ExpiresAt time.Time
}

// CreateCheckoutSession creates a hosted Checkout page that subscribes the
// customer to the price.
func (c *Client) CreateCheckoutSession(ctx context.Context, input CheckoutRequest) (CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("customer", input.CustomerID)
	form.Set("client_reference_id", input.OrgID)
	form.Set("line_items[0][price]", input.PriceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("subscription_data[metadata][ao_org_id]", input.OrgID)
	form.Set("success_url", input.SuccessURL)
	form.Set("cancel_url", input.CancelURL)
	form.Set("allow_promotion_codes", "true")
	if !input.ExpiresAt.IsZero() {
		form.Set("expires_at", strconv.FormatInt(input.ExpiresAt.Unix(), 10))
	}
	var session struct {
		ID        string `json:"id"`
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions", form, "", &session); err != nil {
		return CheckoutSession{}, err
	}
	if session.URL == "" || session.ID == "" {
		return CheckoutSession{}, errors.New("stripe returned a checkout session without an id or url")
	}
	created := CheckoutSession{ID: session.ID, URL: session.URL}
	if session.ExpiresAt > 0 {
		created.ExpiresAt = time.Unix(session.ExpiresAt, 0).UTC()
	} else {
		created.ExpiresAt = time.Now().Add(24 * time.Hour).UTC()
	}
	return created, nil
}

// ExpireCheckoutSession closes an open Checkout session so it can no longer
// be paid, and returns the session's status afterwards: "expired", or
// "complete" when it was paid before it could be closed.
func (c *Client) ExpireCheckoutSession(ctx context.Context, sessionID string) (string, error) {
	path := "/v1/checkout/sessions/" + url.PathEscape(sessionID)
	var session struct {
		Status string `json:"status"`
	}
	err := c.do(ctx, http.MethodPost, path+"/expire", url.Values{}, "", &session)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		// Only an open session can be expired: read what became of it.
		err = c.do(ctx, http.MethodGet, path, nil, "", &session)
	}
	if err != nil {
		return "", err
	}
	return session.Status, nil
}

// CreatePortalSession returns the URL of the Customer Portal for a customer.
// configuration selects a portal configuration (bpc_...); empty uses the
// account's default, which exists only once it is saved in the dashboard.
// cancelSubscriptionID, when set, opens the portal straight on cancelling that
// subscription and returns to returnURL once it is done.
func (c *Client) CreatePortalSession(
	ctx context.Context, customerID, returnURL, configuration, cancelSubscriptionID string,
) (string, error) {
	form := url.Values{}
	form.Set("customer", customerID)
	if configuration != "" {
		form.Set("configuration", configuration)
	}
	if returnURL != "" {
		form.Set("return_url", returnURL)
	}
	if cancelSubscriptionID != "" {
		form.Set("flow_data[type]", "subscription_cancel")
		form.Set("flow_data[subscription_cancel][subscription]", cancelSubscriptionID)
		if returnURL != "" {
			form.Set("flow_data[after_completion][type]", "redirect")
			form.Set("flow_data[after_completion][redirect][return_url]", returnURL)
		}
	}
	var session struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/billing_portal/sessions", form, "", &session); err != nil {
		return "", err
	}
	if session.URL == "" {
		return "", errors.New("stripe returned a portal session without a url")
	}
	return session.URL, nil
}

// Subscription is the part of a Stripe subscription billing needs.
type Subscription struct {
	ID          string
	CustomerID  string
	Status      string
	OrgID       string
	Created     *time.Time
	PeriodStart *time.Time
	PeriodEnd   *time.Time
	// CancelAt is when a subscription set to cancel ends; nil while it renews.
	CancelAt *time.Time
	// Limits are parsed from the subscribed product's metadata; LimitsErr is
	// set instead when that metadata is incomplete.
	Limits    *domain.PlanLimits
	LimitsErr error
}

// GetSubscription fetches a subscription with its product, and reads the plan
// limits from the product metadata. Webhooks call this rather than trusting
// event payloads, so events arriving out of order still leave the latest
// state.
func (c *Client) GetSubscription(ctx context.Context, subscriptionID string) (Subscription, error) {
	var raw struct {
		ID                 string            `json:"id"`
		Customer           string            `json:"customer"`
		Status             string            `json:"status"`
		Created            int64             `json:"created"`
		Metadata           map[string]string `json:"metadata"`
		CurrentPeriodStart int64             `json:"current_period_start"`
		CurrentPeriodEnd   int64             `json:"current_period_end"`
		CancelAt           int64             `json:"cancel_at"`
		CancelAtPeriodEnd  bool              `json:"cancel_at_period_end"`
		Items              struct {
			Data []struct {
				CurrentPeriodStart int64 `json:"current_period_start"`
				CurrentPeriodEnd   int64 `json:"current_period_end"`
				Price              struct {
					Product struct {
						Metadata map[string]string `json:"metadata"`
					} `json:"product"`
				} `json:"price"`
			} `json:"data"`
		} `json:"items"`
	}
	path := "/v1/subscriptions/" + url.PathEscape(subscriptionID) + "?expand[]=items.data.price.product"
	if err := c.do(ctx, http.MethodGet, path, nil, "", &raw); err != nil {
		return Subscription{}, err
	}
	subscription := Subscription{
		ID: raw.ID, CustomerID: raw.Customer, Status: raw.Status, OrgID: raw.Metadata["ao_org_id"],
	}
	if raw.Created > 0 {
		created := time.Unix(raw.Created, 0).UTC()
		subscription.Created = &created
	}
	// Newer API versions report the billing period on the items rather than
	// on the subscription; accept either.
	start, end := raw.CurrentPeriodStart, raw.CurrentPeriodEnd
	if len(raw.Items.Data) > 0 {
		item := raw.Items.Data[0]
		if start == 0 {
			start = item.CurrentPeriodStart
		}
		if end == 0 {
			end = item.CurrentPeriodEnd
		}
		limits, err := domain.ParsePlanLimits(item.Price.Product.Metadata)
		if err != nil {
			subscription.LimitsErr = err
		} else {
			subscription.Limits = &limits
		}
	} else {
		subscription.LimitsErr = errors.New("subscription has no items")
	}
	if start > 0 {
		value := time.Unix(start, 0).UTC()
		subscription.PeriodStart = &value
	}
	if end > 0 {
		value := time.Unix(end, 0).UTC()
		subscription.PeriodEnd = &value
	}
	switch {
	case raw.CancelAt > 0:
		value := time.Unix(raw.CancelAt, 0).UTC()
		subscription.CancelAt = &value
	case raw.CancelAtPeriodEnd && subscription.PeriodEnd != nil:
		value := *subscription.PeriodEnd
		subscription.CancelAt = &value
	}
	return subscription, nil
}

// productSubscriptionPageLimit is how many objects one list call returns; Stripe's
// maximum.
const productSubscriptionPageLimit = 100

// ProductSubscriptions lists the IDs of the subscriptions on any price (active
// or archived) of a product, skipping ones that have ended. A product.updated
// webhook uses it to re-read subscriptions whose plan limits live in that
// product's metadata.
func (c *Client) ProductSubscriptions(ctx context.Context, productID string) ([]string, error) {
	var priceIDs []string
	err := c.list(ctx, "/v1/prices", url.Values{"product": {productID}}, func(id, _ string) {
		priceIDs = append(priceIDs, id)
	})
	if err != nil {
		return nil, err
	}
	var subscriptionIDs []string
	for _, priceID := range priceIDs {
		err := c.list(ctx, "/v1/subscriptions", url.Values{"price": {priceID}, "status": {"all"}}, func(id, status string) {
			if status != "canceled" && status != "incomplete_expired" {
				subscriptionIDs = append(subscriptionIDs, id)
			}
		})
		if err != nil {
			return nil, err
		}
	}
	return subscriptionIDs, nil
}

// list pages through a Stripe list endpoint, calling visit with each object's
// ID and status (empty for objects without one).
func (c *Client) list(ctx context.Context, path string, query url.Values, visit func(id, status string)) error {
	query.Set("limit", strconv.Itoa(productSubscriptionPageLimit))
	for {
		var page struct {
			Data []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"data"`
			HasMore bool `json:"has_more"`
		}
		if err := c.do(ctx, http.MethodGet, path+"?"+query.Encode(), nil, "", &page); err != nil {
			return err
		}
		for _, item := range page.Data {
			visit(item.ID, item.Status)
		}
		if !page.HasMore || len(page.Data) == 0 {
			return nil
		}
		query.Set("starting_after", page.Data[len(page.Data)-1].ID)
	}
}

// Price is a plan's price as shown to a customer choosing a plan.
type Price struct {
	ID         string             `json:"id"`
	UnitAmount int64              `json:"unitAmount"`
	Currency   string             `json:"currency"`
	Interval   string             `json:"interval"`
	Limits     *domain.PlanLimits `json:"limits,omitempty"`
}

// GetPrice fetches a price with its product, for the plan picker.
func (c *Client) GetPrice(ctx context.Context, priceID string) (Price, error) {
	var raw struct {
		ID         string `json:"id"`
		UnitAmount int64  `json:"unit_amount"`
		Currency   string `json:"currency"`
		Recurring  struct {
			Interval string `json:"interval"`
		} `json:"recurring"`
		Product struct {
			Metadata map[string]string `json:"metadata"`
		} `json:"product"`
	}
	path := "/v1/prices/" + url.PathEscape(priceID) + "?expand[]=product"
	if err := c.do(ctx, http.MethodGet, path, nil, "", &raw); err != nil {
		return Price{}, err
	}
	price := Price{ID: raw.ID, UnitAmount: raw.UnitAmount, Currency: raw.Currency, Interval: raw.Recurring.Interval}
	if limits, err := domain.ParsePlanLimits(raw.Product.Metadata); err == nil {
		price.Limits = &limits
	}
	return price, nil
}

// Event is a verified webhook event. Only the fields billing reads are
// decoded; the object is re-fetched from Stripe before anything is written.
type Event struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object struct {
			ID                string `json:"id"`
			Object            string `json:"object"`
			Customer          string `json:"customer"`
			Subscription      string `json:"subscription"`
			ClientReferenceID string `json:"client_reference_id"`
			// Parent carries an invoice's subscription from API version
			// 2025-03-31.basil on, which dropped the top-level field.
			Parent struct {
				SubscriptionDetails struct {
					Subscription string `json:"subscription"`
				} `json:"subscription_details"`
			} `json:"parent"`
		} `json:"object"`
	} `json:"data"`
}

// InvoiceSubscription is the subscription an invoice event belongs to, in
// either the current (parent.subscription_details) or the pre-2025-03-31
// (top-level subscription) shape: the webhook endpoint's API version, not
// the client's, decides which one Stripe sends.
func (e Event) InvoiceSubscription() string {
	if id := e.Data.Object.Parent.SubscriptionDetails.Subscription; id != "" {
		return id
	}
	return e.Data.Object.Subscription
}

// ErrInvalidSignature rejects a webhook that was not signed with the
// endpoint's secret, or was signed too long ago to be a live delivery.
var ErrInvalidSignature = errors.New("invalid Stripe webhook signature")

// webhookTolerance bounds how old a signed webhook may be, against replays.
const webhookTolerance = 5 * time.Minute

// VerifyWebhook checks the Stripe-Signature header against the raw request
// body and returns the decoded event.
func VerifyWebhook(payload []byte, header, secret string, now time.Time) (Event, error) {
	if secret == "" {
		return Event{}, ErrInvalidSignature
	}
	var timestamp string
	var signatures []string
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			timestamp = value
		case "v1":
			signatures = append(signatures, value)
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || len(signatures) == 0 {
		return Event{}, ErrInvalidSignature
	}
	signedAt := time.Unix(seconds, 0)
	if now.Sub(signedAt) > webhookTolerance || signedAt.Sub(now) > webhookTolerance {
		return Event{}, ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	valid := false
	for _, signature := range signatures {
		decoded, err := hex.DecodeString(signature)
		if err == nil && hmac.Equal(decoded, expected) {
			valid = true
		}
	}
	if !valid {
		return Event{}, ErrInvalidSignature
	}
	var event Event
	if err := json.Unmarshal(payload, &event); err != nil || event.ID == "" || event.Type == "" {
		return Event{}, fmt.Errorf("decode Stripe event: %w", errors.Join(err, ErrInvalidSignature))
	}
	return event, nil
}
