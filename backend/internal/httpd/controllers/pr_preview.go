package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/prpreview"
)

// PRPreviewService reads any GitHub pull request a chat links to.
type PRPreviewService interface {
	Preview(ctx context.Context, rawURL string) (prpreview.Preview, error)
}

// PRPreviewController serves the pull request preview endpoint.
type PRPreviewController struct {
	Svc PRPreviewService
}

// Register mounts the pr-preview route on the supplied router.
func (c *PRPreviewController) Register(r chi.Router) {
	r.Get("/pr-preview", c.preview)
}

func (c *PRPreviewController) preview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/pr-preview")
		return
	}
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if rawURL == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "URL_REQUIRED", "url query parameter is required", nil)
		return
	}
	p, err := c.Svc.Preview(r.Context(), rawURL)
	if err != nil {
		// The hover ended; the read carries on to warm the cache.
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return
		}
		if errors.Is(err, prpreview.ErrFetchFailed) {
			envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "PR_PREVIEW_FETCH_FAILED", "The pull request could not be read", nil)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	resp := PRPreviewResponse{
		URL:          p.URL,
		Repo:         p.Repo,
		Number:       p.Number,
		Title:        p.Title,
		State:        p.State,
		Checks:       p.Checks,
		Author:       p.Author,
		SourceBranch: p.SourceBranch,
		TargetBranch: p.TargetBranch,
		Additions:    p.Additions,
		Deletions:    p.Deletions,
		ChangedFiles: p.ChangedFiles,
	}
	if !p.StateChangedAt.IsZero() {
		at := p.StateChangedAt.UTC()
		resp.StateChangedAt = &at
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
