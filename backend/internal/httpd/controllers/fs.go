package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	fsbrowsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/fsbrowser"
)

type DirectoryBrowserService interface {
	List(ctx context.Context, path string) (fsbrowsersvc.Listing, error)
}

type FSController struct {
	Svc DirectoryBrowserService
}

func (c *FSController) Register(r chi.Router) {
	r.Get("/fs/dirs", c.listDirs)
}

func (c *FSController) listDirs(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/fs/dirs")
		return
	}
	listing, err := c.Svc.List(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}

	out := ListDirsResponse{Path: listing.Path, Parent: listing.Parent, Entries: make([]FSEntry, len(listing.Entries)), Truncated: listing.Truncated}
	for i, entry := range listing.Entries {
		out.Entries[i] = FSEntry{Name: entry.Name, Path: entry.Path, GitRepo: entry.GitRepo}
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}
