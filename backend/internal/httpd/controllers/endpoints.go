package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type EndpointSource interface {
	AdvertisedEndpoints() []mobilebridge.Endpoint
}

type EndpointsController struct {
	Source EndpointSource
}

func (c *EndpointsController) Register(r chi.Router) {
	r.Get("/endpoints", c.endpoints)
}

func (c *EndpointsController) endpoints(w http.ResponseWriter, r *http.Request) {
	if c.Source == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/endpoints")
		return
	}
	endpoints := c.Source.AdvertisedEndpoints()
	if endpoints == nil {
		endpoints = []mobilebridge.Endpoint{}
	}
	envelope.WriteJSON(w, http.StatusOK, EndpointsResponse{Endpoints: endpoints})
}
