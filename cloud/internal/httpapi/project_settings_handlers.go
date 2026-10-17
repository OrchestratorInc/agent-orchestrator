package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/go-chi/chi/v5"
)

type projectSettingsStore interface {
	UpdateProjectSettings(context.Context, domain.Principal, string, string, domain.ProjectSettingsPatch) (domain.Project, error)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := chi.URLParam(r, "orgId"), chi.URLParam(r, "projectId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(projectID, "projectId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and projectId must be UUIDs.")
		return
	}
	project, err := s.store.GetProject(r.Context(), principalFrom(r), orgID, projectID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": toProjectResponse(project)})
}

func (s *Server) updateProjectSettings(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := chi.URLParam(r, "orgId"), chi.URLParam(r, "projectId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(projectID, "projectId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and projectId must be UUIDs.")
		return
	}
	var raw json.RawMessage
	if err := decodeJSON(w, r, &raw); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	patch, err := domain.ParseProjectSettingsPatch(raw)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	store, ok := s.store.(projectSettingsStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Cloud project settings are unavailable.")
		return
	}
	if !s.requireAdminForProjectScripts(w, r, orgID, patch.Config) {
		return
	}
	if parameters, patched := patchedCoderParameters(patch.Config); patched && len(parameters) > 0 {
		// Template parameters belong to the template chosen at project creation
		// and are checked (best effort) against what that template declares.
		project, err := s.store.GetProject(r.Context(), principalFrom(r), orgID, projectID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		coderConfig, _ := domain.DecodeProjectCoderConfig(project.Config)
		if strings.TrimSpace(coderConfig.TemplateID) == "" {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "Coder template parameters require a project with a chosen Coder template.")
			return
		}
		// The patch replaces the stored map as a whole, so it is the full set
		// the template is evaluated with.
		if problem := s.coderTemplateParameterProblem(
			r.Context(), orgID, strings.TrimSpace(coderConfig.TemplateID), parameters, requestID(r),
		); problem != "" {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_error", problem)
			return
		}
	}
	project, err := store.UpdateProjectSettings(r.Context(), principalFrom(r), orgID, projectID, patch)
	if errors.Is(err, postgres.ErrInvalid) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": toProjectResponse(project)})
}

// patchedCoderParameters returns the coder.parameters a settings patch sets,
// and whether it sets them at all. Shape was already validated by
// ParseProjectSettingsPatch.
func patchedCoderParameters(config json.RawMessage) (map[string]string, bool) {
	if len(config) == 0 {
		return nil, false
	}
	var envelope struct {
		Coder *struct {
			Parameters map[string]string `json:"parameters"`
		} `json:"coder"`
	}
	if err := json.Unmarshal(config, &envelope); err != nil || envelope.Coder == nil || envelope.Coder.Parameters == nil {
		return nil, false
	}
	return envelope.Coder.Parameters, true
}

// requireAdminForProjectScripts lets only organization owners and admins set
// a project's Setup or Cleanup commands (or clear them). They run in every
// later session of the project, including other members' sessions with those
// members' own credentials, so they are an admin setting like the Coder
// connection. It reports whether the request may continue and writes the
// error when not.
func (s *Server) requireAdminForProjectScripts(w http.ResponseWriter, r *http.Request, orgID string, config json.RawMessage) bool {
	if !configSetsProjectScripts(config) {
		return true
	}
	store, ok := s.store.(orgAdminStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Project scripts are unavailable.")
		return false
	}
	if err := store.RequireOrgAdmin(r.Context(), principalFrom(r), orgID); err != nil {
		s.writeStoreError(w, r, err)
		return false
	}
	return true
}

// configSetsProjectScripts reports whether a project config (or settings
// patch) carries the postCreate or preRemove key.
func configSetsProjectScripts(config json.RawMessage) bool {
	if len(config) == 0 {
		return false
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(config, &keys) != nil {
		return false
	}
	_, setup := keys["postCreate"]
	_, cleanup := keys["preRemove"]
	return setup || cleanup
}
