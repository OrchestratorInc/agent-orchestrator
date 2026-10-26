package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type createStandaloneProjectRequest struct {
	DisplayName string         `json:"displayName"`
	Config      map[string]any `json:"config,omitempty"`
}

// standaloneProjectNamespace derives a standalone project's placeholder
// repository id from its organization and idempotency key, so a retried
// create replays the first one instead of conflicting with it.
var standaloneProjectNamespace = uuid.MustParse("6f1d8c2e-6a4b-4f7e-9c1d-2b7f0a9e5c31")

// createStandaloneProject creates a project with no repository: its sessions
// start in an empty, git-initialized folder (for a workspace template that
// brings its own code, or work that needs no repository). It takes the same
// settings as a repository project except additional repositories, and it
// cannot open pull requests.
func (s *Server) createStandaloneProject(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var request createStandaloneProjectRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	if n := utf8.RuneCountInString(request.DisplayName); n < 1 || n > 120 {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The project name must contain 1-120 characters.")
		return
	}
	if request.Config == nil {
		request.Config = map[string]any{}
	}
	if _, hasRepos := request.Config["repos"]; hasRepos {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "A project without a repository cannot have additional repositories.")
		return
	}
	request.Config["source"] = domain.StandaloneProjectSource
	config, err := json.Marshal(request.Config)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "Project configuration is invalid.")
		return
	}
	if err := validateProjectCoderConfig(config); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	repositoryURL := domain.StandaloneRepositoryURL(uuid.NewSHA1(standaloneProjectNamespace, []byte(orgID+"\x00"+key)).String())
	config, problem := normalizeNewProjectRepoConfig(repositoryURL, config)
	if problem != nil {
		problem.write(w, r)
		return
	}
	if len(domain.ProjectAdditionalRepos(config)) > 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "A project without a repository cannot have additional repositories.")
		return
	}
	if !s.requireAdminForProjectScripts(w, r, orgID, config) {
		return
	}
	if !s.validateProjectSandboxProvider(w, r, principalFrom(r), orgID, config) {
		return
	}
	if !s.validateProjectCoderParameters(w, r, orgID, config) {
		return
	}
	if config, err = s.pinProjectProvider(r.Context(), principalFrom(r), orgID, config); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "Project configuration is invalid.")
		return
	}
	project, err := s.store.CreateProject(r.Context(), principalFrom(r), orgID, key, domain.CreateProject{
		DisplayName:   request.DisplayName,
		RepositoryURL: repositoryURL,
		DefaultBranch: "main",
		Config:        config,
	})
	if err != nil {
		s.writeProjectStoreError(w, r, err)
		return
	}
	s.logger.Info("standalone project created", "org_id", orgID, "user_id", principalFrom(r).UserID, "project_id", project.ID)
	s.recordActivity(r, domain.ActivityEvent{OrgID: orgID, Kind: domain.ActivityProjectCreated,
		Attrs: map[string]any{"project_id": project.ID}})
	writeJSON(w, http.StatusCreated, map[string]any{"project": toProjectResponse(project)})
}
