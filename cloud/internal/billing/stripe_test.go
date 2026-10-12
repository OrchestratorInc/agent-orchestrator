package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func sign(payload []byte, secret string, at time.Time) string {
	timestamp := fmt.Sprint(at.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(payload)
	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhook(t *testing.T) {
	payload := []byte(`{"id":"evt_1","type":"customer.subscription.updated","data":{"object":{"id":"sub_1","object":"subscription","customer":"cus_1"}}}`)
	now := time.Unix(1_800_000_000, 0)
	event, err := VerifyWebhook(payload, sign(payload, "whsec_test", now), "whsec_test", now)
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "evt_1" || event.Data.Object.ID != "sub_1" || event.Data.Object.Customer != "cus_1" {
		t.Fatalf("event = %+v", event)
	}
	for name, header := range map[string]string{
		"wrong secret":  sign(payload, "whsec_other", now),
		"too old":       sign(payload, "whsec_test", now.Add(-10*time.Minute)),
		"no signature":  "t=" + fmt.Sprint(now.Unix()),
		"garbage":       "nonsense",
		"tampered body": sign([]byte(`{"id":"evt_2"}`), "whsec_test", now),
	} {
		if _, err := VerifyWebhook(payload, header, "whsec_test", now); !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("%s: err = %v, want ErrInvalidSignature", name, err)
		}
	}
	if _, err := VerifyWebhook(payload, sign(payload, "", now), "", now); !errors.Is(err, ErrInvalidSignature) {
		t.Error("an empty endpoint secret must reject every webhook")
	}
}

func TestGetSubscriptionReadsLimitsAndPeriodFromItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, _ := r.BasicAuth(); user != "rk_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v1/subscriptions/sub_1" || r.URL.Query().Get("expand[]") != "items.data.price.product" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"id":"sub_1","customer":"cus_1","status":"active","metadata":{"ao_org_id":"org-1"},
			"items":{"data":[{"current_period_start":1800000000,"current_period_end":1802592000,
			"price":{"product":{"metadata":{"ao_plan":"pro","ao_max_active_sandboxes":"4","ao_orchestrator_slots":"1",
			"ao_window_hours":"5","ao_window_session_hours":"12","ao_weekly_session_hours":"40","ao_manual_resets_per_month":"1"}}}}]}}`)
	}))
	defer server.Close()
	subscription, err := NewClient("rk_test", server.URL).GetSubscription(context.Background(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if subscription.Status != "active" || subscription.OrgID != "org-1" || subscription.Limits == nil ||
		subscription.Limits.Plan != "pro" || subscription.Limits.WorkerSlots() != 3 ||
		subscription.PeriodStart == nil || subscription.PeriodStart.Unix() != 1800000000 {
		t.Fatalf("subscription = %+v limits=%+v err=%v", subscription, subscription.Limits, subscription.LimitsErr)
	}
}

func TestCreateCheckoutSessionSendsSubscriptionForm(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_, _ = io.WriteString(w, `{"id":"cs_test","url":"https://checkout.stripe.com/c/pay/cs_test","expires_at":1800003600}`)
	}))
	defer server.Close()
	session, err := NewClient("rk_test", server.URL).CreateCheckoutSession(context.Background(), CheckoutRequest{
		OrgID: "org-1", CustomerID: "cus_1", PriceID: "price_pro", SuccessURL: "https://a/ok", CancelURL: "https://a/no",
		ExpiresAt: time.Unix(1800003600, 0),
	})
	if err != nil || !strings.HasPrefix(session.URL, "https://checkout.stripe.com/") || session.ID != "cs_test" ||
		session.ExpiresAt.Unix() != 1800003600 {
		t.Fatalf("session = %+v err = %v", session, err)
	}
	for key, want := range map[string]string{
		"mode": "subscription", "customer": "cus_1", "client_reference_id": "org-1",
		"line_items[0][price]": "price_pro", "subscription_data[metadata][ao_org_id]": "org-1",
		"expires_at": "1800003600",
	} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
}

func TestAPIErrorsSurfaceStripeMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"resource_missing","message":"No such price"}}`)
	}))
	defer server.Close()
	_, err := NewClient("rk_test", server.URL).CreatePortalSession(context.Background(), "cus_1", "", "", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "resource_missing" || apiErr.Status != 400 {
		t.Fatalf("err = %v", err)
	}
}

// Every request pins the API version whose shapes the client reads.
func TestRequestsPinTheStripeAPIVersion(t *testing.T) {
	var version string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version = r.Header.Get("Stripe-Version")
		_, _ = io.WriteString(w, `{"id":"price_1","unit_amount":2000,"currency":"usd","recurring":{"interval":"month"},
			"product":{"metadata":{"ao_plan":"pro","ao_max_active_sandboxes":"4","ao_orchestrator_slots":"1",
			"ao_window_hours":"5","ao_window_session_hours":"12","ao_weekly_session_hours":"40",
			"ao_manual_resets_per_month":"1","ao_features":"Priority-Support, sso ,sso"}}}`)
	}))
	defer server.Close()
	price, err := NewClient("rk_test", server.URL).GetPrice(context.Background(), "price_1")
	if err != nil || version != APIVersion {
		t.Fatalf("version = %q err = %v", version, err)
	}
	if price.Limits == nil || strings.Join(price.Limits.Features, ",") != "priority-support,sso" {
		t.Fatalf("plan features = %+v", price.Limits)
	}
}

// An invoice names its subscription under parent.subscription_details from
// 2025-03-31.basil on, and at the top level before; both are read.
func TestInvoiceSubscriptionReadsBothShapes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for name, payload := range map[string]string{
		"basil":  `{"id":"evt_1","type":"invoice.paid","data":{"object":{"id":"in_1","parent":{"subscription_details":{"subscription":"sub_1"}}}}}`,
		"legacy": `{"id":"evt_1","type":"invoice.paid","data":{"object":{"id":"in_1","subscription":"sub_1"}}}`,
	} {
		event, err := VerifyWebhook([]byte(payload), sign([]byte(payload), "whsec_test", now), "whsec_test", now)
		if err != nil || event.InvoiceSubscription() != "sub_1" {
			t.Errorf("%s: subscription = %q err = %v", name, event.InvoiceSubscription(), err)
		}
	}
}

func TestGetSubscriptionReportsWhenACancelledPlanEnds(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         int64
	}{
		{"renewing", `"cancel_at_period_end":false`, 0},
		{"at period end", `"cancel_at_period_end":true`, 1802592000},
		{"at a set time", `"cancel_at":1801000000,"cancel_at_period_end":false`, 1801000000},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"id":"sub_1","customer":"cus_1","status":"active",`+tc.fields+`,
				"items":{"data":[{"current_period_start":1800000000,"current_period_end":1802592000,"price":{"product":{"metadata":{}}}}]}}`)
		}))
		subscription, err := NewClient("rk_test", server.URL).GetSubscription(context.Background(), "sub_1")
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		got := int64(0)
		if subscription.CancelAt != nil {
			got = subscription.CancelAt.Unix()
		}
		if got != tc.want {
			t.Errorf("%s: cancel at %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Expiring a Checkout that was already paid reports it complete instead of
// failing, so the caller does not start a second one.
func TestExpireCheckoutSessionReportsACompletedSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/cs_open/expire":
			_, _ = io.WriteString(w, `{"id":"cs_open","status":"expired"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/cs_paid/expire":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"Only open sessions can be expired"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/cs_paid":
			_, _ = io.WriteString(w, `{"id":"cs_paid","status":"complete"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewClient("rk_test", server.URL)
	if status, err := client.ExpireCheckoutSession(context.Background(), "cs_open"); err != nil || status != "expired" {
		t.Fatalf("open: %q %v", status, err)
	}
	if status, err := client.ExpireCheckoutSession(context.Background(), "cs_paid"); err != nil || status != "complete" {
		t.Fatalf("paid: %q %v", status, err)
	}
}

func TestGetSubscriptionReadsCreated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"sub_1","customer":"cus_1","status":"active","created":1790000000,"items":{"data":[]}}`)
	}))
	defer server.Close()
	subscription, err := NewClient("rk_test", server.URL).GetSubscription(context.Background(), "sub_1")
	if err != nil || subscription.Created == nil || subscription.Created.Unix() != 1790000000 {
		t.Fatalf("subscription = %+v err = %v", subscription, err)
	}
}

func TestPortalCancelFlowOpensOnTheSubscription(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_, _ = io.WriteString(w, `{"url":"https://billing.stripe.com/p/session/x"}`)
	}))
	defer server.Close()
	if _, err := NewClient("rk_test", server.URL).CreatePortalSession(
		context.Background(), "cus_1", "https://a/return", "bpc_1", "sub_1"); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"customer": "cus_1", "configuration": "bpc_1", "return_url": "https://a/return",
		"flow_data[type]": "subscription_cancel", "flow_data[subscription_cancel][subscription]": "sub_1",
		"flow_data[after_completion][type]": "redirect", "flow_data[after_completion][redirect][return_url]": "https://a/return",
	} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
}

func TestProductSubscriptionsPagesAndSkipsEndedOnes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case r.URL.Path == "/v1/prices" && query.Get("product") == "prod_1":
			_, _ = io.WriteString(w, `{"data":[{"id":"price_a"},{"id":"price_b"}],"has_more":false}`)
		case r.URL.Path == "/v1/subscriptions" && query.Get("price") == "price_a" && query.Get("starting_after") == "":
			if query.Get("status") != "all" || query.Get("limit") != "100" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"sub_1","status":"active"},{"id":"sub_2","status":"canceled"}],"has_more":true}`)
		case r.URL.Path == "/v1/subscriptions" && query.Get("price") == "price_a" && query.Get("starting_after") == "sub_2":
			_, _ = io.WriteString(w, `{"data":[{"id":"sub_3","status":"past_due"}],"has_more":false}`)
		case r.URL.Path == "/v1/subscriptions" && query.Get("price") == "price_b":
			_, _ = io.WriteString(w, `{"data":[{"id":"sub_4","status":"incomplete_expired"},{"id":"sub_5","status":"trialing"}],"has_more":false}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ids, err := NewClient("rk_test", server.URL).ProductSubscriptions(context.Background(), "prod_1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "sub_1,sub_3,sub_5" {
		t.Fatalf("subscriptions = %v, want sub_1,sub_3,sub_5", ids)
	}
}
