package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/linkpreview"
)

type LinkPreviewService interface {
	Preview(ctx context.Context, rawURL string) (linkpreview.Preview, error)
}

type LinkPreviewController struct {
	Svc LinkPreviewService
}

func (c *LinkPreviewController) Register(r chi.Router) {
	r.Get("/link-preview", c.preview)
}

func (c *LinkPreviewController) preview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/link-preview")
		return
	}
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "URL_REQUIRED", "url query parameter is required", nil)
		return
	}
	p, err := c.Svc.Preview(r.Context(), rawURL)
	if err != nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return
		}
		if errors.Is(err, linkpreview.ErrFetchFailed) {
			envelope.CaptureError(r, apierr.Internal("LINK_PREVIEW_FETCH_FAILED", "Link preview fetch failed"))
			envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "LINK_PREVIEW_FETCH_FAILED", "The page could not be fetched for a link preview", nil)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LinkPreviewResponse{
		URL:            p.URL,
		Title:          p.Title,
		Description:    p.Description,
		ImageURL:       p.ImageURL,
		SiteName:       p.SiteName,
		FaviconURL:     p.FaviconURL,
		FaviconDarkURL: p.FaviconDarkURL,
	})
}
