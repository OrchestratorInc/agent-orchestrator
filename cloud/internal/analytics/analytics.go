// Package analytics sends the small set of server-side product events AO Cloud
// emits (invites and, once billing exists, pricing and subscription events) to
// PostHog. Every event is attributed to the signed-in WorkOS user ID, the same
// distinct ID the desktop passes to posthog.identify, so cloud events join the
// desktop person. Email is never sent from here: it is set once, by the
// desktop, as a person property at identify time.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Event names. pricing.viewed, checkout.started and subscription.created have
// no emit site yet (the cloud has no pricing page or billing code); the names
// and property allowlists are fixed here so wiring them later cannot widen
// what is sent.
const (
	InviteSent          = "invite.sent"
	PricingViewed       = "pricing.viewed"
	CheckoutStarted     = "checkout.started"
	SubscriptionCreated = "subscription.created"
)

var allowlist = map[string]map[string]struct{}{
	InviteSent:          {"role": {}, "org_id_hash": {}},
	PricingViewed:       {"plan": {}, "source": {}},
	CheckoutStarted:     {"plan": {}, "interval": {}},
	SubscriptionCreated: {"plan": {}, "interval": {}, "status": {}},
}

// Sink receives events. Implementations must not block the request.
type Sink interface {
	Capture(distinctID, event string, props map[string]any)
}

// Noop drops everything; the default when no PostHog key is configured.
type Noop struct{}

// Capture implements Sink.
func (Noop) Capture(string, string, map[string]any) {}

// Sanitize keeps only allowlisted scalar properties. Unknown events yield nil.
func Sanitize(event string, props map[string]any) map[string]any {
	allowed, ok := allowlist[event]
	if !ok {
		return nil
	}
	out := map[string]any{}
	for key := range allowed {
		switch v := props[key].(type) {
		case string:
			if v = strings.TrimSpace(v); v != "" && len(v) <= 64 {
				out[key] = v
			}
		case bool, int, int64, float64:
			out[key] = v
		}
	}
	return out
}

// PostHog posts events to the PostHog capture API.
type PostHog struct {
	key, host, environment string
	client                 *http.Client
	log                    *slog.Logger
}

// NewPostHog returns a Sink, or Noop when key or host is empty.
func NewPostHog(key, host, environment string, client *http.Client, log *slog.Logger) Sink {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(host) == "" {
		return Noop{}
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	if log == nil {
		log = slog.Default()
	}
	return &PostHog{key: key, host: strings.TrimRight(host, "/"), environment: environment, client: client, log: log}
}

// Capture implements Sink. Fire and forget: analytics never fails a request.
func (p *PostHog) Capture(distinctID, event string, props map[string]any) {
	clean := Sanitize(event, props)
	if clean == nil || strings.TrimSpace(distinctID) == "" {
		return
	}
	clean["client"] = "cloud"
	clean["environment"] = p.environment
	clean["ao_cloud_user_id"] = distinctID
	body, err := json.Marshal(map[string]any{
		"api_key":     p.key,
		"event":       event,
		"distinct_id": distinctID,
		"properties":  clean,
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	// ponytail: one goroutine per event, no queue or retry; events here are
	// per-human actions (invites), nowhere near a volume that needs one.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/capture/", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			p.log.Warn("analytics export failed", "event", event, "error", err)
			return
		}
		_ = resp.Body.Close()
	}()
}
