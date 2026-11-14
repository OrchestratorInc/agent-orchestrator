package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/webhook"
)

// WebhookStore is the configured-webhook store (*webhook.Store).
type WebhookStore interface {
	List() []webhook.Hook
	Get(name string) (webhook.Hook, bool)
	Put(h webhook.Hook, now time.Time) (webhook.Hook, error)
	Patch(name string, f webhook.Filters) (webhook.Hook, error)
	Delete(name string) (bool, error)
}

// WebhookDelivery reports and tests deliveries (*webhook.Dispatcher).
type WebhookDelivery interface {
	Test(ctx context.Context, name string) error
	Statuses() map[string]webhook.Status
}

// WebhooksController manages outbound webhooks. Its routes are loopback-only.
type WebhooksController struct {
	Store    WebhookStore
	Delivery WebhookDelivery
}

// Register mounts the webhook routes.
func (c *WebhooksController) Register(r chi.Router) {
	r.Get("/webhooks", c.list)
	r.Put("/webhooks/{name}", c.put)
	r.Patch("/webhooks/{name}", c.patch)
	r.Delete("/webhooks/{name}", c.delete)
	r.Post("/webhooks/{name}/test", c.test)
}

func (c *WebhooksController) ready(w http.ResponseWriter, r *http.Request) bool {
	if c.Store == nil || c.Delivery == nil {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return false
	}
	return true
}

func (c *WebhooksController) list(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	statuses := c.Delivery.Statuses()
	out := WebhookListResponse{Webhooks: []WebhookView{}, Kinds: webhook.Kinds}
	for _, h := range c.Store.List() {
		out.Webhooks = append(out.Webhooks, webhookView(h, statuses[h.Name]))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *WebhooksController) put(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	var in WebhookPutRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	h, err := c.Store.Put(webhook.Hook{
		Name: chi.URLParam(r, "name"), URL: in.URL, Headers: in.Headers,
		Projects: in.Projects, Events: in.Events, QuietAtDesk: in.QuietAtDesk,
	}, time.Now())
	if err != nil {
		writeWebhookError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, webhookView(h, c.Delivery.Statuses()[h.Name]))
}

func (c *WebhooksController) patch(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	var in WebhookPatchRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	h, err := c.Store.Patch(chi.URLParam(r, "name"), webhook.Filters{Projects: in.Projects, Events: in.Events, QuietAtDesk: in.QuietAtDesk})
	if err != nil {
		writeWebhookError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, webhookView(h, c.Delivery.Statuses()[h.Name]))
}

func (c *WebhooksController) delete(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	ok, err := c.Store.Delete(chi.URLParam(r, "name"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !ok {
		writeWebhookError(w, r, webhook.ErrNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *WebhooksController) test(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r) {
		return
	}
	err := c.Delivery.Test(r.Context(), chi.URLParam(r, "name"))
	if errors.Is(err, webhook.ErrNotFound) {
		writeWebhookError(w, r, err)
		return
	}
	out := WebhookTestResponse{OK: err == nil}
	if err != nil {
		out.Error = err.Error()
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func writeWebhookError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, webhook.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "WEBHOOK_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, webhook.ErrInvalidName), errors.Is(err, webhook.ErrInvalidURL),
		errors.Is(err, webhook.ErrInvalidEvent), errors.Is(err, webhook.ErrInvalidHeader):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_WEBHOOK", err.Error(), nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

func webhookView(h webhook.Hook, st webhook.Status) WebhookView {
	host := ""
	if u, err := url.Parse(h.URL); err == nil {
		host = u.Host
	}
	names := make([]string, 0, len(h.Headers))
	for k := range h.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	return WebhookView{
		Name: h.Name, Host: host, HeaderNames: names,
		Projects: nonNil(h.Projects), Events: nonNil(h.Events),
		QuietAtDesk: h.QuietAtDesk, CreatedAt: h.CreatedAt, Status: st,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
