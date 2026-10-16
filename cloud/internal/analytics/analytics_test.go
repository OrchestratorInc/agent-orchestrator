package analytics

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSanitizeAllowlist(t *testing.T) {
	got := Sanitize(InviteSent, map[string]any{
		"role":        "member",
		"org_id_hash": "abc",
		"email":       "invitee@example.com",
		"org_name":    "Acme",
	})
	if len(got) != 2 || got["role"] != "member" || got["org_id_hash"] != "abc" {
		t.Fatalf("sanitized = %#v", got)
	}
	if Sanitize("not.an.event", map[string]any{"role": "x"}) != nil {
		t.Fatal("unknown event must be dropped")
	}
	for event, keys := range allowlist {
		for key := range keys {
			if strings.Contains(key, "email") || strings.Contains(key, "name") {
				t.Errorf("%s allowlists %q", event, key)
			}
		}
	}
	for _, event := range []string{PricingViewed, CheckoutStarted, SubscriptionCreated} {
		if Sanitize(event, map[string]any{"plan": "pro", "email": "a@b.co"})["email"] != nil {
			t.Errorf("%s leaked email", event)
		}
	}
}

func TestPostHogCaptureAttributesToWorkOSUserWithoutEmail(t *testing.T) {
	bodies := make(chan map[string]any, 1)
	client := &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies <- body
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	sink := NewPostHog("phc_test", "https://us.i.posthog.com/", "production", client, nil)
	sink.Capture("user_01H", InviteSent, map[string]any{"role": "admin", "email": "invitee@example.com"})
	select {
	case body := <-bodies:
		if body["event"] != "invite.sent" || body["distinct_id"] != "user_01H" {
			t.Fatalf("body = %#v", body)
		}
		props := body["properties"].(map[string]any)
		if props["ao_cloud_user_id"] != "user_01H" || props["role"] != "admin" || props["client"] != "cloud" {
			t.Fatalf("props = %#v", props)
		}
		if strings.Contains(strings.ToLower(toJSON(body)), "invitee@example.com") {
			t.Fatal("invitee email leaked")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request sent")
	}
}

func TestNoKeyIsNoop(t *testing.T) {
	if _, ok := NewPostHog("", "https://us.i.posthog.com", "x", nil, nil).(Noop); !ok {
		t.Fatal("expected Noop without a key")
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
