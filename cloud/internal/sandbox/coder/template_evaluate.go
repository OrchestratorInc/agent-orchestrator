package coder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/google/uuid"
)

// ErrDynamicParametersUnsupported reports a template whose parameters cannot be
// evaluated against draft values: it uses Coder's classic parameter flow, or the
// Coder deployment predates the dynamic-parameters API. Callers fall back to
// the version's static rich parameters.
var ErrDynamicParametersUnsupported = errors.New("coder: template does not evaluate dynamic parameters")

// TemplateParameterDiagnostic is a problem Coder found with a parameter's
// value, such as a value that is not one of its options.
type TemplateParameterDiagnostic struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
}

type dynamicParameterValue struct {
	Value string `json:"value"`
	Valid bool   `json:"valid"`
}

type dynamicParameterDiagnostic struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
}

type dynamicParametersResponse struct {
	Diagnostics []dynamicParameterDiagnostic `json:"diagnostics"`
	Parameters  []struct {
		Name         string                `json:"name"`
		DisplayName  string                `json:"display_name"`
		Description  string                `json:"description"`
		Type         string                `json:"type"`
		FormType     string                `json:"form_type"`
		Mutable      bool                  `json:"mutable"`
		Required     bool                  `json:"required"`
		DefaultValue dynamicParameterValue `json:"default_value"`
		Options      []struct {
			Name        string                `json:"name"`
			Description string                `json:"description"`
			Value       dynamicParameterValue `json:"value"`
		} `json:"options"`
		Diagnostics []dynamicParameterDiagnostic `json:"diagnostics"`
	} `json:"parameters"`
}

// EvaluateTemplateParameters returns the parameters the template's active
// version declares once values are applied. A template can declare a
// parameter only when another one has a given value (11x's "Other GitHub
// repository" appears once "Other" is selected), so the static rich parameters,
// which Coder evaluates with defaults, do not list it. Each parameter carries
// Coder's diagnostics for its value. It returns
// ErrDynamicParametersUnsupported for a classic-flow template or a Coder
// without the dynamic-parameters API, and an error when Coder cannot evaluate
// the template at all.
func (c *Client) EvaluateTemplateParameters(ctx context.Context, templateID string, values map[string]string) ([]TemplateParameter, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(templateID))
	if err != nil {
		return nil, errors.New("coder: template ID must be a UUID")
	}
	var template struct {
		ActiveVersionID         string `json:"active_version_id"`
		UseClassicParameterFlow bool   `json:"use_classic_parameter_flow"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/templates/"+url.PathEscape(parsed.String()), nil, &template); err != nil {
		if errors.Is(err, sandbox.ErrNotFound) {
			return nil, ErrTemplateNotFound
		}
		return nil, fmt.Errorf("coder: read template: %w", err)
	}
	if template.UseClassicParameterFlow {
		return nil, ErrDynamicParametersUnsupported
	}
	if strings.TrimSpace(template.ActiveVersionID) == "" {
		return []TemplateParameter{}, nil
	}
	owner := strings.TrimSpace(c.owner)
	if owner == "" {
		owner = "me"
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/users/"+url.PathEscape(owner), nil, &user); err != nil {
		return nil, fmt.Errorf("coder: read workspace owner: %w", err)
	}
	inputs := values
	if inputs == nil {
		inputs = map[string]string{}
	}
	var evaluated dynamicParametersResponse
	err = c.do(ctx, http.MethodPost,
		"/api/v2/templateversions/"+url.PathEscape(template.ActiveVersionID)+"/dynamic-parameters/evaluate",
		map[string]any{"id": 1, "owner_id": user.ID, "inputs": inputs}, &evaluated)
	if errors.Is(err, sandbox.ErrNotFound) || (err != nil && strings.Contains(err.Error(), "API returned 405")) {
		return nil, ErrDynamicParametersUnsupported
	}
	if err != nil {
		return nil, fmt.Errorf("coder: evaluate template parameters: %w", err)
	}
	for _, diagnostic := range evaluated.Diagnostics {
		if diagnostic.Severity == "error" {
			return nil, fmt.Errorf("coder: evaluate template parameters: %s", strings.TrimSpace(diagnostic.Summary))
		}
	}
	parameters := make([]TemplateParameter, 0, len(evaluated.Parameters))
	for _, p := range evaluated.Parameters {
		if p.Name == "" {
			continue
		}
		options := make([]TemplateParameterOption, 0, len(p.Options))
		for _, option := range p.Options {
			options = append(options, TemplateParameterOption{Name: option.Name, Value: option.Value.Value, Description: option.Description})
		}
		var diagnostics []TemplateParameterDiagnostic
		for _, diagnostic := range p.Diagnostics {
			diagnostics = append(diagnostics, TemplateParameterDiagnostic{
				Severity: diagnostic.Severity, Summary: strings.TrimSpace(diagnostic.Summary), Detail: strings.TrimSpace(diagnostic.Detail),
			})
		}
		parameters = append(parameters, TemplateParameter{
			Name: p.Name, DisplayName: p.DisplayName, Description: p.Description,
			Type: p.Type, FormType: p.FormType, Mutable: p.Mutable, Required: p.Required,
			DefaultValue: p.DefaultValue.Value, Options: options, Order: len(parameters),
			Diagnostics: diagnostics,
		})
	}
	return parameters, nil
}
