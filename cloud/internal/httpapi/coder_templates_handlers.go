package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/coder"
	"github.com/go-chi/chi/v5"
)

// CoderTemplateLister lists the Coder templates a client may pick from. It is
// implemented by the coder provider client and is nil when the deployment does
// not offer the coder provider.
type CoderTemplateLister interface {
	ListTemplates(ctx context.Context) ([]coder.Template, error)
}

// coderConnectionForServiceStore reads an organization's encrypted Coder
// connection without a request principal, so the template-list edge can build a
// per-organization lister from the org's own deployment. It is the same store
// method the sandbox resolver uses to provision per-org sessions.
type coderConnectionForServiceStore interface {
	CoderConnectionForService(
		ctx context.Context,
		orgID string,
	) (encrypted, nonce, config []byte, connectionID string, err error)
}

type coderTemplateResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Parameters  []string `json:"parameters"`
}

// listCoderTemplates returns the Coder templates the picker offers for a new
// session, alongside the implicit "Default" (the deployment's configured
// template, which the client selects by sending no templateId). When the caller's
// organization brings its own Coder connection, the templates come from that
// deployment; otherwise they come from the shared, env-configured deployment. The
// endpoint is 403 provider_forbidden when the deployment does not offer coder or
// the caller's organization is not entitled to it.
func (s *Server) listCoderTemplates(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	if !s.requireOrgCoder(w, r, orgID) {
		return
	}
	templates := []coderTemplateResponse{}
	lister, err := s.coderTemplateListerForOrg(r.Context(), orgID)
	if err != nil {
		s.logger.Error("resolve coder template lister", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "coder_unavailable", "Could not load Coder templates.")
		return
	}
	if lister != nil {
		raw, err := lister.ListTemplates(r.Context())
		if err != nil {
			s.logger.Error("list coder templates", "error", err, "request_id", requestID(r))
			writeError(w, r, http.StatusBadGateway, "coder_unavailable", "Could not load Coder templates.")
			return
		}
		for _, t := range raw {
			params := t.Parameters
			if params == nil {
				params = []string{}
			}
			templates = append(templates, coderTemplateResponse{
				ID:          t.ID,
				Name:        t.Name,
				DisplayName: t.DisplayName,
				Description: t.Description,
				Icon:        t.Icon,
				Parameters:  params,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

// coderTemplateListerForOrg picks the Coder template lister for one organization.
// It prefers the organization's own bring-your-own Coder connection — so the
// picker lists the templates on *their* deployment — and falls back to the shared
// deployment lister (which may be nil → an empty list, the pre-existing behavior)
// when the organization has no connection of its own. It reuses
// coder.NewForOrg, the same decrypt-and-build helper the sandbox resolver uses, so
// a per-org lister is constructed exactly as a per-org session is.
func (s *Server) coderTemplateListerForOrg(ctx context.Context, orgID string) (CoderTemplateLister, error) {
	// The organization's connection is read as the service and its token used
	// on the caller's behalf, so the caller must be a member. Every caller is a
	// request handler; without a principal the membership check refuses.
	if store, ok := s.store.(orgMemberStore); ok {
		principal, _ := ctx.Value(principalKey).(domain.Principal)
		if err := store.RequireOrgMember(ctx, principal, orgID); err != nil {
			return nil, err
		}
	}
	if store, ok := s.store.(coderConnectionForServiceStore); ok && s.secretCipher != nil {
		encrypted, nonce, config, _, err := store.CoderConnectionForService(ctx, orgID)
		switch {
		case err == nil:
			cfg, decodeErr := domain.DecodeOrgCoderConfig(config)
			if decodeErr != nil {
				return nil, fmt.Errorf("decode organization coder config: %w", decodeErr)
			}
			client, buildErr := coder.NewForOrg(s.secretCipher, orgID, encrypted, nonce, coder.Config{
				BaseURL:    cfg.BaseURL,
				Owner:      cfg.Owner,
				TemplateID: cfg.TemplateID,
				AgentName:  cfg.AgentName,
				Parameters: cfg.Parameters,
			})
			if buildErr != nil {
				return nil, fmt.Errorf("build organization coder client: %w", buildErr)
			}
			return client, nil
		case errors.Is(err, postgres.ErrNotFound):
			// No per-organization connection: fall through to the deployment lister.
		default:
			return nil, fmt.Errorf("load organization coder connection: %w", err)
		}
	}
	// Deployment default. Nil when the deployment does not offer coder, which the
	// caller renders as an empty list.
	if s.coderTemplates == nil {
		return nil, nil
	}
	if len(s.coderTemplateIDs) > 0 {
		return allowedTemplateLister{inner: s.coderTemplates, allowed: s.coderTemplateIDs}, nil
	}
	return s.coderTemplates, nil
}

// allowedTemplateLister offers only the deployment Coder's templates its
// sessions can boot; the shared Coder also holds test and bring-your-own
// templates.
type allowedTemplateLister struct {
	inner   CoderTemplateLister
	allowed []string
}

func (l allowedTemplateLister) has(templateID string) bool {
	return slices.Contains(l.allowed, strings.TrimSpace(templateID))
}

func (l allowedTemplateLister) ListTemplates(ctx context.Context) ([]coder.Template, error) {
	templates, err := l.inner.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	allowed := templates[:0]
	for _, template := range templates {
		if l.has(template.ID) {
			allowed = append(allowed, template)
		}
	}
	return allowed, nil
}

func (l allowedTemplateLister) TemplateParameters(ctx context.Context, templateID string) ([]coder.TemplateParameter, error) {
	reader, ok := l.inner.(CoderTemplateParameterReader)
	if !ok {
		return nil, coder.ErrTemplateNotFound
	}
	if !l.has(templateID) {
		return nil, coder.ErrTemplateNotFound
	}
	return reader.TemplateParameters(ctx, templateID)
}

func (l allowedTemplateLister) EvaluateTemplateParameters(ctx context.Context, templateID string, values map[string]string) ([]coder.TemplateParameter, error) {
	evaluator, ok := l.inner.(CoderTemplateParameterEvaluator)
	if !ok {
		return nil, coder.ErrDynamicParametersUnsupported
	}
	if !l.has(templateID) {
		return nil, coder.ErrTemplateNotFound
	}
	return evaluator.EvaluateTemplateParameters(ctx, templateID, values)
}

// CoderTemplateParameterReader reads the rich parameters a Coder template's
// active version declares. The coder provider client implements it.
type CoderTemplateParameterReader interface {
	TemplateParameters(ctx context.Context, templateID string) ([]coder.TemplateParameter, error)
}

// CoderTemplateParameterEvaluator evaluates a template's parameters against
// draft values, so parameters a template declares only for certain values of
// another (a dynamic-parameters template) are listed once they apply. The
// coder provider client implements it.
type CoderTemplateParameterEvaluator interface {
	EvaluateTemplateParameters(ctx context.Context, templateID string, values map[string]string) ([]coder.TemplateParameter, error)
}

// getCoderTemplateParameters returns the parameters of one Coder template, for
// the project form's "Template options". Like the template list it reads the
// organization's own Coder connection when it has one, else the deployment's.
func (s *Server) getCoderTemplateParameters(w http.ResponseWriter, r *http.Request) {
	s.writeCoderTemplateParameters(w, r, nil, false)
}

type evaluateCoderTemplateParametersRequest struct {
	Values map[string]string `json:"values"`
}

// evaluateCoderTemplateParameters returns a Coder template's parameters with
// the form's draft values applied, so a parameter the template declares only
// for some value of another (11x's "Other GitHub repository" once "Other" is
// picked) appears as soon as it applies. A classic-flow template, or a Coder
// without dynamic parameters, answers with its static parameters, exactly as
// the GET does.
func (s *Server) evaluateCoderTemplateParameters(w http.ResponseWriter, r *http.Request) {
	var request evaluateCoderTemplateParametersRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Request body must be JSON with a values object.")
		return
	}
	if err := domain.ValidateCoderTemplateParameterValues(request.Values); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	s.writeCoderTemplateParameters(w, r, request.Values, true)
}

func (s *Server) writeCoderTemplateParameters(w http.ResponseWriter, r *http.Request, values map[string]string, evaluate bool) {
	orgID, templateID := chi.URLParam(r, "orgId"), chi.URLParam(r, "templateId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(templateID, "templateId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and templateId must be UUIDs.")
		return
	}
	if !s.requireOrgCoder(w, r, orgID) {
		return
	}
	reader, err := s.coderTemplateParameterReaderForOrg(r.Context(), orgID)
	if err != nil {
		s.logger.Error("resolve coder template parameter reader", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "coder_unavailable", "Could not load the Coder template's options.")
		return
	}
	if reader == nil {
		writeError(w, r, http.StatusNotFound, "coder_template_not_found", "The Coder template was not found.")
		return
	}
	var parameters []coder.TemplateParameter
	if evaluate {
		parameters, _, err = s.evaluatedCoderTemplateParameters(r.Context(), reader, templateID, values, requestID(r))
	} else {
		parameters, err = reader.TemplateParameters(r.Context(), templateID)
	}
	if errors.Is(err, coder.ErrTemplateNotFound) {
		writeError(w, r, http.StatusNotFound, "coder_template_not_found", "The Coder template was not found.")
		return
	}
	if err != nil {
		s.logger.Error("read coder template parameters", "error", err, "request_id", requestID(r))
		writeError(w, r, http.StatusBadGateway, "coder_unavailable", "Could not load the Coder template's options.")
		return
	}
	if parameters == nil {
		parameters = []coder.TemplateParameter{}
	}
	for i := range parameters {
		if parameters[i].Options == nil {
			parameters[i].Options = []coder.TemplateParameterOption{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"parameters": parameters})
}

// evaluatedCoderTemplateParameters reads a template's parameters with values
// applied when the template and Coder support dynamic parameters (evaluated
// reports true), and otherwise its static rich parameters. An evaluation that
// fails for any reason other than an unknown template falls back to the static
// list, so a Coder hiccup never hides the form or blocks a save.
func (s *Server) evaluatedCoderTemplateParameters(
	ctx context.Context,
	reader CoderTemplateParameterReader,
	templateID string,
	values map[string]string,
	reqID string,
) (parameters []coder.TemplateParameter, evaluated bool, err error) {
	if evaluator, ok := reader.(CoderTemplateParameterEvaluator); ok {
		parameters, err = evaluator.EvaluateTemplateParameters(ctx, templateID, values)
		switch {
		case err == nil:
			return parameters, true, nil
		case errors.Is(err, coder.ErrTemplateNotFound):
			return nil, false, err
		case !errors.Is(err, coder.ErrDynamicParametersUnsupported):
			s.logger.Warn("evaluate coder template parameters; using the static list", "error", err, "request_id", reqID)
		}
	}
	parameters, err = reader.TemplateParameters(ctx, templateID)
	return parameters, false, err
}

// coderTemplateParameterReaderForOrg resolves the same per-organization (or
// deployment) Coder client the template list uses, as a parameter reader. Nil
// means no Coder is configured for the organization.
func (s *Server) coderTemplateParameterReaderForOrg(ctx context.Context, orgID string) (CoderTemplateParameterReader, error) {
	lister, err := s.coderTemplateListerForOrg(ctx, orgID)
	if err != nil || lister == nil {
		return nil, err
	}
	reader, ok := lister.(CoderTemplateParameterReader)
	if !ok {
		return nil, nil
	}
	return reader, nil
}

// coderTemplateParameterProblem checks a project's template parameter values
// against the parameters the template declares with those values applied
// (parameters a dynamic template declares conditionally count once their
// trigger is set), and, for an evaluated template, against Coder's own checks
// of each value. It returns the user-facing problem, or "" when the values are
// acceptable. It is best effort: when the template's parameters cannot be read
// (no Coder, Coder unreachable) it reports no problem, so a transient Coder
// read never blocks a project save.
func (s *Server) coderTemplateParameterProblem(
	ctx context.Context,
	orgID, templateID string,
	parameters map[string]string,
	reqID string,
) string {
	if len(parameters) == 0 || templateID == "" {
		return ""
	}
	reader, err := s.coderTemplateParameterReaderForOrg(ctx, orgID)
	if err != nil || reader == nil {
		if err != nil {
			s.logger.Warn("validate coder template parameters: resolve reader", "error", err, "request_id", reqID)
		}
		return ""
	}
	declared, evaluated, err := s.evaluatedCoderTemplateParameters(ctx, reader, templateID, parameters, reqID)
	if err != nil {
		s.logger.Warn("validate coder template parameters: read template", "error", err, "request_id", reqID)
		return ""
	}
	names := make([]string, 0, len(declared))
	for _, parameter := range declared {
		names = append(names, parameter.Name)
	}
	if unknown := domain.UnknownCoderTemplateParameters(parameters, names); len(unknown) > 0 {
		return unknownCoderTemplateParametersMessage(unknown)
	}
	if !evaluated {
		return ""
	}
	var problems []string
	for _, parameter := range declared {
		if _, set := parameters[parameter.Name]; !set {
			// A parameter left at its default can still report a problem
			// caused by another value; that value's own diagnostic names it.
			continue
		}
		for _, diagnostic := range parameter.Diagnostics {
			if diagnostic.Severity != "error" {
				continue
			}
			label := strings.TrimSpace(parameter.DisplayName)
			if label == "" {
				label = parameter.Name
			}
			problem := label + ": " + strings.TrimSuffix(diagnostic.Summary, ".")
			if diagnostic.Detail != "" {
				problem += " (" + diagnostic.Detail + ")"
			}
			problems = append(problems, problem)
			break
		}
	}
	if len(problems) == 0 {
		return ""
	}
	slices.Sort(problems)
	return "The Coder template rejected these values: " + strings.Join(problems, "; ") + "."
}

// validateProjectCoderParameters checks the template parameters a free-form
// project config carries under coder.parameters: they need a chosen template,
// and (best effort) must be parameters that template declares. It writes 422
// validation_error and returns false on rejection. Shape is validated
// separately by validateProjectCoderConfig.
func (s *Server) validateProjectCoderParameters(w http.ResponseWriter, r *http.Request, orgID string, config json.RawMessage) bool {
	cfg, ok := domain.DecodeProjectCoderConfig(config)
	if !ok || len(cfg.Parameters) == 0 {
		return true
	}
	templateID := strings.TrimSpace(cfg.TemplateID)
	if templateID == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "Coder template parameters require choosing a template.")
		return false
	}
	if problem := s.coderTemplateParameterProblem(r.Context(), orgID, templateID, cfg.Parameters, requestID(r)); problem != "" {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", problem)
		return false
	}
	return true
}

// unknownCoderTemplateParametersMessage explains rejected parameter names.
func unknownCoderTemplateParametersMessage(unknown []string) string {
	return "The Coder template does not declare these parameters: " + strings.Join(unknown, ", ") + "."
}
