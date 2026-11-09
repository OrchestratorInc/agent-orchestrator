// Package controllers holds the HTTP-facing controllers for the /api/v1
// surface. Each controller groups one resource's routes, exposes a Register
// method, and depends on exactly one resource-level Manager interface — never
// directly on stores, lifecycle reducers, or adapters.
package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// ProjectsController owns the /projects routes. The controller depends only on
// projectsvc.Manager; nil keeps routes registered but returns OpenAPI-backed 501s.
type ProjectsController struct {
	Mgr projectsvc.Manager
}

// Register mounts the project routes on the supplied router.
func (c *ProjectsController) Register(r chi.Router) {
	r.Get("/projects", c.list)
	r.Post("/projects", c.add)
	r.Post("/projects/clone", c.clone)
	r.Post("/projects/clone/prepare", c.prepareClone)
	r.Post("/projects/clone/prepare/stream", c.prepareCloneStream)
	r.Post("/projects/clone/check", c.checkCloneDestination)
	r.Post("/projects/clone/cleanup", c.cleanupPreparedClone)
	r.Post("/projects/initialize", c.initialize)
	r.Get("/projects/{id}", c.get)
	r.Put("/projects/{id}", c.updateSettings)
	r.Put("/projects/{id}/config", c.setConfig)
	r.Get("/projects/{id}/conductor", c.conductorImport)
	r.Patch("/projects/{id}/permissions", c.setPermissions)
	r.Delete("/projects/{id}/permissions", c.forgetPermissions)
	r.Delete("/projects/{id}", c.remove)
}

func (c *ProjectsController) checkCloneDestination(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/clone/check")
		return
	}
	var in projectsvc.CloneInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	result, err := c.Mgr.CheckCloneDestination(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func (c *ProjectsController) prepareClone(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/clone/prepare")
		return
	}
	var in projectsvc.CloneInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	result, err := c.Mgr.PrepareClone(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

// prepareCloneStream runs the same clone as prepareClone and reports each git
// step as a server-sent event while it runs, so a long clone can show what it
// is doing. The final event carries the result or the error envelope.
func (c *ProjectsController) prepareCloneStream(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/clone/prepare/stream")
		return
	}
	var in projectsvc.CloneInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SSE_UNSUPPORTED", "Streaming is not supported by this server", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Progress is reported from git's stderr reader; the lock keeps a late
	// report from interleaving with, or following, the final event.
	var mu sync.Mutex
	done := false
	send := func(event ClonePrepareStreamEvent, final bool) {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return
		}
		done = final
		data, err := json.Marshal(event)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
		flusher.Flush()
	}
	result, err := c.Mgr.PrepareCloneWithProgress(r.Context(), in, func(progress projectsvc.CloneProgress) {
		send(ClonePrepareStreamEvent{Progress: &progress}, false)
	})
	if err != nil {
		apiErr := envelope.StreamError(r, err)
		send(ClonePrepareStreamEvent{Error: &apiErr}, true)
		return
	}
	send(ClonePrepareStreamEvent{Result: &result}, true)
}

func (c *ProjectsController) cleanupPreparedClone(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/clone/cleanup")
		return
	}
	var in projectsvc.ClonePreparationCleanupInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.Mgr.CleanupPreparedClone(r.Context(), in); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ProjectsController) clone(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/clone")
		return
	}
	var in projectsvc.CloneInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	p, err := c.Mgr.Clone(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ProjectResponse{Project: p})
}

func (c *ProjectsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects")
		return
	}
	projects, err := c.Mgr.List(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if projects == nil {
		projects = []projectsvc.Summary{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListProjectsResponse{Projects: projects})
}

func (c *ProjectsController) add(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects")
		return
	}
	var in projectsvc.AddInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	p, err := c.Mgr.Add(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ProjectResponse{Project: p})
}

func (c *ProjectsController) initialize(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/initialize")
		return
	}
	var in projectsvc.InitializeRepositoryInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	result, err := c.Mgr.InitializeRepository(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}
func (c *ProjectsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}")
		return
	}
	got, err := c.Mgr.Get(r.Context(), projectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp, err := newGetProjectResponse(got)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *ProjectsController) updateSettings(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/projects/{id}")
		return
	}
	var in projectsvc.UpdateSettingsInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	p, err := c.Mgr.UpdateSettings(r.Context(), projectID(r), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectResponse{Project: p})
}

func (c *ProjectsController) setConfig(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/projects/{id}/config")
		return
	}
	var in projectsvc.SetConfigInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	p, err := c.Mgr.SetConfig(r.Context(), projectID(r), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectResponse{Project: p})
}

func (c *ProjectsController) conductorImport(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}/conductor")
		return
	}
	plan, err := c.Mgr.ConductorImport(r.Context(), projectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, plan)
}

func (c *ProjectsController) remove(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/projects/{id}")
		return
	}
	result, err := c.Mgr.Remove(r.Context(), projectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func projectID(r *http.Request) domain.ProjectID {
	return domain.ProjectID(chi.URLParam(r, "id"))
}

func decodeJSON(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}

// decodeJSONStrict rejects request bodies that include keys outside the target
// type. It is used where misspelled or retired fields must surface as a 400
// instead of being silently dropped.
func decodeJSONStrict(r *http.Request, out any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func (c *ProjectsController) setPermissions(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/projects/{id}/permissions")
		return
	}
	var in projectsvc.SetPermissionsInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	p, err := c.Mgr.SetPermissions(r.Context(), projectID(r), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectResponse{Project: p})
}

func (c *ProjectsController) forgetPermissions(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/projects/{id}/permissions")
		return
	}
	p, err := c.Mgr.ForgetPermissions(r.Context(), projectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ProjectResponse{Project: p})
}
